package service

import (
	"context"
	"log"
	"time"
)

// PollInterval is how often the poller fetches snapshots for the watchlist
// (SPEC.md §2.3).
const PollInterval = 15 * time.Second

// PriceTTL is how long a cached price survives before GET
// /market-data/prices/:symbol starts treating it as not-cached (SPEC.md
// §2.5) -- 3x PollInterval, so one missed tick doesn't immediately look like
// an outage.
const PriceTTL = 3 * PollInterval

// Poller periodically fetches latest prices for a fixed symbol list and
// writes them to the cache. Symbols default to DefaultWatchlist in
// production (SPEC.md §2.7) but are passed in explicitly so tests can use a
// smaller set.
type Poller struct {
	snapshots SnapshotClient
	cache     PriceCache
	symbols   []string
	interval  time.Duration
}

func NewPoller(snapshots SnapshotClient, cache PriceCache, symbols []string, interval time.Duration) *Poller {
	return &Poller{snapshots: snapshots, cache: cache, symbols: symbols, interval: interval}
}

// Run ticks every p.interval until ctx is done, calling Poll each time. A
// failed tick is logged and never stops the loop -- the next tick retries
// automatically (SPEC.md §2.9, no separate retry/backoff logic).
func (p *Poller) Run(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := p.Poll(ctx); err != nil {
				log.Printf("market-data: poll tick failed: %v", err)
			}
		}
	}
}

// Poll runs one tick: one batched GetSnapshots call for all watched symbols
// (SPEC.md §2.2 -- atomic per tick, unlike Ingest's per-symbol handling,
// since there's only one request that can fail here). A symbol with no
// LatestTrade in the response is skipped for this tick, not treated as an
// error (SPEC.md §2.4) -- its previously cached value survives until TTL or
// the next successful tick. Exported so tests can drive a single tick
// synchronously instead of waiting on Run's ticker.
func (p *Poller) Poll(ctx context.Context) error {
	snapshots, err := p.snapshots.GetSnapshots(ctx, p.symbols)
	if err != nil {
		return err
	}

	for _, symbol := range p.symbols {
		snap, ok := snapshots[symbol]
		if !ok || snap.LatestTrade == nil {
			continue
		}

		price := Price{Symbol: symbol, Price: snap.LatestTrade.Price, Timestamp: snap.LatestTrade.Timestamp}
		if err := p.cache.SetPrice(ctx, symbol, price, PriceTTL); err != nil {
			log.Printf("market-data: cache price for %s: %v", symbol, err)
			continue
		}
		if err := p.cache.PublishPrice(ctx, symbol, price); err != nil {
			log.Printf("market-data: publish price for %s: %v", symbol, err)
		}
	}

	return nil
}

package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kpeguero/quantsim/services/market-data/internal/alpaca"
	"github.com/kpeguero/quantsim/services/market-data/internal/service"
	"github.com/kpeguero/quantsim/services/market-data/internal/service/mock"
)

func TestPoller_SuccessfulTick_CachesAndPublishesAllSymbols(t *testing.T) {
	snapMock := mock.NewSnapshotClient()
	snapMock.Snapshots["AAPL"] = alpaca.Snapshot{LatestTrade: &alpaca.Trade{Price: 100, Timestamp: time.Now()}}
	snapMock.Snapshots["MSFT"] = alpaca.Snapshot{LatestTrade: &alpaca.Trade{Price: 200, Timestamp: time.Now()}}
	cacheMock := mock.NewPriceCache()
	poller := service.NewPoller(snapMock, cacheMock, []string{"AAPL", "MSFT"}, service.PollInterval)

	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cacheMock.Prices) != 2 {
		t.Fatalf("expected 2 cached prices, got %d", len(cacheMock.Prices))
	}
	if cacheMock.Prices["AAPL"].Price != 100 {
		t.Errorf("AAPL cached price = %v, want 100", cacheMock.Prices["AAPL"].Price)
	}
	if cacheMock.Prices["MSFT"].Price != 200 {
		t.Errorf("MSFT cached price = %v, want 200", cacheMock.Prices["MSFT"].Price)
	}
	if len(cacheMock.Published) != 2 {
		t.Fatalf("expected 2 published prices, got %d", len(cacheMock.Published))
	}

	if len(snapMock.LastSymbols) != 2 {
		t.Fatalf("expected one batched call with 2 symbols, got %v", snapMock.LastSymbols)
	}
}

func TestPoller_SkipsSymbolMissingLatestTrade(t *testing.T) {
	snapMock := mock.NewSnapshotClient()
	snapMock.Snapshots["AAPL"] = alpaca.Snapshot{LatestTrade: &alpaca.Trade{Price: 100, Timestamp: time.Now()}}
	snapMock.Snapshots["NEWCO"] = alpaca.Snapshot{} // no latest trade yet
	cacheMock := mock.NewPriceCache()
	poller := service.NewPoller(snapMock, cacheMock, []string{"AAPL", "NEWCO"}, service.PollInterval)

	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cacheMock.Prices) != 1 {
		t.Fatalf("expected 1 cached price (AAPL only), got %d: %+v", len(cacheMock.Prices), cacheMock.Prices)
	}
	if _, ok := cacheMock.Prices["NEWCO"]; ok {
		t.Error("expected NEWCO to be skipped, not cached")
	}
}

func TestPoller_SnapshotClientError_AbortsTickNoCacheWrites(t *testing.T) {
	snapMock := mock.NewSnapshotClient()
	snapMock.Err = errors.New("upstream down")
	cacheMock := mock.NewPriceCache()
	poller := service.NewPoller(snapMock, cacheMock, []string{"AAPL"}, service.PollInterval)

	err := poller.Poll(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if len(cacheMock.Prices) != 0 {
		t.Fatalf("expected no cache writes on upstream failure, got %d", len(cacheMock.Prices))
	}
	if len(cacheMock.Published) != 0 {
		t.Fatalf("expected no publishes on upstream failure, got %d", len(cacheMock.Published))
	}
}

package service

import (
	"context"
	"sort"

	"golang.org/x/sync/errgroup"
)

// FetchHistories fetches every symbol's bars concurrently.
//
// Which failure is reported is deliberate, and the whole reason this is not
// three lines. symbols is sorted here rather than assumed sorted, and errs is
// scanned in that order, so two unavailable symbols always name the same one
// no matter how the goroutines happened to be scheduled. errgroup.Wait's own
// error is whichever goroutine lost the race, which would make the answer
// depend on scheduling -- so it is discarded.
//
// This is a zero-value errgroup.Group and deliberately NOT WithContext: a
// failing fetch must not cancel its siblings, because a sibling cancelled
// mid-flight would report a context error in place of the real one it was
// about to return, reintroducing exactly the nondeterminism the ordered scan
// exists to remove. Step 19 reached the same conclusion the same way.
//
// Errors pass through untouched -- the clients already name the symbol, so
// re-wrapping here would say it twice.
func FetchHistories(ctx context.Context, history HistoryClient, symbols []string) (map[string][]Bar, error) {
	ordered := make([]string, len(symbols))
	copy(ordered, symbols)
	sort.Strings(ordered)

	bars := make([][]Bar, len(ordered))
	errs := make([]error, len(ordered))

	var g errgroup.Group
	for i, symbol := range ordered {
		g.Go(func() error {
			var err error
			bars[i], err = history.History(ctx, symbol)
			errs[i] = err
			return err
		})
	}
	_ = g.Wait()

	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}

	out := make(map[string][]Bar, len(ordered))
	for i, symbol := range ordered {
		out[symbol] = bars[i]
	}
	return out, nil
}

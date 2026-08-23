package cachekeys_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/kpeguero/quantsim/pkg/cachekeys"
)

// Asserted against a written-out literal, never against the function under
// test. Two services depend on this exact string, and a test that builds its
// expectation by calling Insights would agree with any format change,
// including one that silently stops trading-engine invalidating anything
// ai-insights ever reads.
func TestInsights_IsTheNamespacedUUID(t *testing.T) {
	id := uuid.MustParse("2735b945-6107-4aa5-9553-b5fbb4e85b27")

	if got, want := cachekeys.Insights(id), "insights:2735b945-6107-4aa5-9553-b5fbb4e85b27"; got != want {
		t.Errorf("Insights() = %q, want %q", got, want)
	}
}

// uuid.UUID.String() is canonical lowercase, so two services that parsed the
// same subject cannot disagree about the key even if one saw it uppercased.
func TestInsights_IsCanonicalRegardlessOfInputCasing(t *testing.T) {
	lower := uuid.MustParse("2735b945-6107-4aa5-9553-b5fbb4e85b27")
	upper := uuid.MustParse("2735B945-6107-4AA5-9553-B5FBB4E85B27")

	if cachekeys.Insights(lower) != cachekeys.Insights(upper) {
		t.Errorf("casing changed the key: %q vs %q",
			cachekeys.Insights(lower), cachekeys.Insights(upper))
	}
}

// The repo shares ONE Redis across services and keeps them apart by prefix
// convention alone (.env.example). market-data owns "price:" and "prices:",
// auth owns "revoked:". A collision would not error; it would silently serve
// one service's value to another, which is the worst shape a bug can have.
//
// Moved here from ai-insights' cache package in Step 24, along with the key.
func TestInsights_CollidesWithNoOtherNamespace(t *testing.T) {
	id := uuid.MustParse("6f1e0e5a-1b2c-4d3e-8f90-abcdef012345")
	got := cachekeys.Insights(id)

	// A bare user id as a key is the failure this guards: any other service
	// that ever keys on a user id would then read and write the same entry.
	if got == id.String() {
		t.Fatal("the key is a bare user id, in a Redis shared with three other services")
	}
	for _, taken := range []string{"price:", "prices:", "revoked:", "narrative:"} {
		if strings.HasPrefix(got, taken) {
			t.Errorf("key %q lands in %q, which another service owns", got, taken)
		}
	}
}

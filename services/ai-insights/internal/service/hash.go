package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// zeroTime is the value computed_at is flattened to before hashing.
var zeroTime time.Time

// hashLength is how much of the digest is kept. Sixteen hex characters is 64
// bits, which is far more than enough to tell one user's handful of daily
// reports apart, and short enough to read in a log line or a cache key.
const hashLength = 16

// ReportHash identifies a report by its content, so a narrative describing it
// can be cached against it (SPEC.md §2.10).
//
// It lives here, beside the struct, and it EXCLUDES exactly one field by
// zeroing it rather than by listing the fields to include (plan D2). That
// direction matters. computed_at is cache age rather than a measurement and
// changes on every recomputation, so hashing the report as serialized would
// mint a fresh key every five minutes and produce a cache that never hit once
// -- a defect that looks like working code and shows up only on the bill.
// Every other field participates, and a measurement added to this struct in
// some later step participates BY DEFAULT. An include-list would instead keep
// serving stale prose for whichever figure nobody remembered to add, which is
// the failure that stays quiet.
//
// json.Marshal over a Go struct is already canonical -- fields in declaration
// order, map keys sorted -- so no separate canonicalisation is needed. There
// are no maps in this report today; if one is ever added, that property is
// what keeps this stable.
func ReportHash(r PortfolioInsights) string {
	r.ComputedAt = zeroTime

	// A marshalling failure here cannot happen for this struct -- it is plain
	// data with no channels, functions or NaNs reachable from it. If it ever
	// does, an empty hash would silently collapse every report to one cache
	// entry, so the digest of the error text is used instead: it is stable for
	// the same failure and shares nothing with a real report's hash.
	data, err := json.Marshal(r)
	if err != nil {
		data = []byte("unhashable:" + err.Error())
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:hashLength]
}

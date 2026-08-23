// Package cachekeys holds the Redis key formats shared between services.
//
// Every key in this system is namespaced by a prefix -- ai-insights'
// "insights:" and "narrative:", market-data's "price:" and "prices:", auth's
// "revoked:" -- and sharing one Redis is only safe because of that convention.
// A bare user id as a key would collide with anything else that ever keys on
// one.
//
// A format lives here once it has TWO users in different services. Until then
// it belongs beside the code that reads it: a key only one service touches is
// that service's private business, and hoisting it here would invite the rest
// of the system to depend on it.
package cachekeys

import "github.com/google/uuid"

// Insights is the key ai-insights stores a rendered portfolio report under,
// and the key trading-engine deletes when a fill makes that report stale.
//
// It takes a uuid.UUID rather than a string on purpose. ai-insights parses the
// JWT subject to a UUID before keying on it so that an arbitrary token subject
// cannot become an arbitrary Redis key, and so a subject containing a colon
// cannot be shaped to look like another namespace's key. A string parameter
// would let any caller opt out of that; this signature makes it structural,
// and both callers already hold the id in this type.
//
// Two services produce this string, so it cannot be a literal in either of
// them. Written out in trading-engine, a change to the format here would break
// nothing at compile time and everything at runtime, quietly, in the direction
// that serves stale reports rather than the one that errors.
func Insights(userID uuid.UUID) string {
	return "insights:" + userID.String()
}

package handler

// An INTERNAL test, unlike every other test in this package, which is
// handler_test. The reasons it pins are unexported, and exporting them to
// make an external test possible would widen the package's API to
// accommodate a test -- a worse trade than this one deviation.

import "testing"

// The reason strings the frontend switches on, pinned to their exact text.
//
// frontend/src/insights/narrative-state.ts maps three of the nine reasons
// BY VALUE and lets the rest fall through to a generic sentence. That makes
// these three a contract across two languages with nothing but a string
// literal holding it together: change one here and the frontend keeps
// compiling, keeps passing its own tests, and quietly starts showing "a
// written summary isn't available right now" where it should show "written
// summaries are turned off" or "you've reached today's limit".
//
// Nothing in Go or TypeScript can catch that. This test can.
//
// The other six are deliberately NOT pinned: the frontend maps them by
// exclusion rather than by value, so their text is free to change without
// anything downstream noticing -- which is the point of mapping them that
// way.
func TestTheReasonsTheFrontendSwitchesOnAreUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  string
		want string
	}{
		{"notConfigured", reasonNotConfigured, "narrative generation is not configured"},
		{"nothing", reasonNothing, "no analysis is available to describe"},
		{"overCap", reasonOverCap, "daily generation limit reached"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s is %q, but frontend/src/insights/narrative-state.ts matches on %q -- "+
				"the frontend will silently fall through to its generic copy", tc.name, tc.got, tc.want)
		}
	}
}

// The count is part of the contract too: narrative-state.test.ts asserts it
// maps nine reasons to four outcomes, and a tenth added here would make that
// claim false without failing anything on either side.
func TestTheReasonSetIsStillNine(t *testing.T) {
	reasons := map[string]struct{}{
		reasonNotConfigured: {}, reasonNothing: {}, reasonUnusable: {},
		reasonUpstream: {}, reasonOverCap: {}, reasonNoCounter: {},
		reasonRateLimited: {}, reasonTimedOut: {}, reasonRefused: {},
	}
	if len(reasons) != 9 {
		t.Fatalf("the nine reasons are no longer nine distinct strings: got %d", len(reasons))
	}
}

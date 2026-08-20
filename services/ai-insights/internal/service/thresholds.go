package service

// The behavioral thresholds (SPEC.md §2.7).
//
// NONE OF THESE NUMBERS IS PRINCIPLED. They are starting guesses, chosen to
// fire on obviously-bad behaviour and stay quiet otherwise, in the same spirit
// as Step 19 §2.4's symbol cap of 10. They live here, together and named,
// precisely so that changing one is a deliberate edit to a documented value
// rather than a magic number moved somewhere in a rule -- and so that the day
// there is real usage to calibrate against, the whole calibration surface is
// one file.
//
// Exported so a future tuning step, or a test arguing a threshold is wrong,
// names the constant rather than repeating the literal.
const (
	// OvertradingTurnoverRatio is the turnover multiple above which trading
	// volume is flagged. Turnover rather than a trade count because ten trades
	// on a 100k portfolio and ten trades of a hundred dollars each are not the
	// same behaviour, and a count cannot tell them apart.
	OvertradingTurnoverRatio = 2.0

	// OvertradingWindowDays is the trailing window, in CALENDAR days, not
	// trading days. "The last 30 days" is what a user means; a 30-trading-day
	// window would silently stretch across six weeks of wall-clock time.
	OvertradingWindowDays = 30

	// PanicSellDropPct is how far a symbol must have fallen on the previous
	// trading day for a sell into it to count. Expressed as a positive
	// percentage; the comparison negates it.
	PanicSellDropPct = 5.0

	// PanicSellMinOccurrences and PanicSellMinShareOfSells are alternatives,
	// not both required: three occurrences is enough on its own, and so is a
	// third of all sells. The share matters for an account that sells rarely,
	// where three occurrences may never accumulate but two out of four sells
	// is still a pattern.
	PanicSellMinOccurrences  = 3
	PanicSellMinShareOfSells = 0.30

	// The risk-profile bands. A band rather than a score because a two-input
	// score to two decimal places would imply a precision these inputs do not
	// have.
	//
	// Aggressive is an OR and conservative is an AND, deliberately: one
	// alarming figure is enough to stop calling a portfolio safe, while
	// calling it conservative should require both to be calm.
	AggressiveVolatilityPct   = 25.0
	AggressiveHHI             = 0.5
	ConservativeVolatilityPct = 12.0
	ConservativeHHI           = 0.25
)

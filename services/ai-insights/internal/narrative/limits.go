package narrative

// The two values SPEC.md §6 left open, and the caps around them. They are all
// here, in one file, because T12's manual pass is expected to move at least
// one of them once there is real output to judge (plan D6).
const (
	// MaxSectionChars bounds one section's prose. Three or four sentences is
	// the brief (SPEC.md §2.6); this is several times that, so hitting it
	// means the model has run away rather than been slightly wordy.
	MaxSectionChars = 1200

	// MaxTotalChars bounds the whole narrative. Deliberately less than three
	// times MaxSectionChars: every section may be long, but not all at once.
	MaxTotalChars = 3000

	// DailyGenerationCap is SPEC.md §6.4, shipped at the recommended value.
	// Far above honest use behind a 24-hour content-hash cache, and low enough
	// that a runaway client costs about a dollar rather than an open-ended
	// amount.
	DailyGenerationCap = 50
)

// bannedWords is SPEC.md §2.5's second check: the number words, fraction
// words, magnitude words and bare unit words that would let a figure through
// in a form the digit check cannot see.
//
// It is drawn deliberately wide, down to "one" (SPEC.md §6.1). The counts have
// their own tokens -- behavior.trade_count, risk.position_count,
// behavior.finding_count -- so a model reaching for the word instead of the
// token is a model that has slipped the contract, and catching that is the
// point. The cost is that ordinary prose is occasionally rejected: "on the one
// hand" fails, and the retry absorbs it. The failure direction is toward
// saying nothing rather than toward saying something unverifiable, which is
// the correct direction.
//
// Ordinals other than the fraction words are NOT here. "first" and "second"
// appear constantly in explanatory prose with no numeric claim attached, and
// banning them would reject far more good drafts than bad ones. Whether that
// line is drawn correctly is a question for the manual pass, judged on the
// observed rejection rate rather than guessed at now.
var bannedWords = []string{
	"zero", "one", "two", "three", "four", "five", "six", "seven", "eight",
	"nine", "ten", "eleven", "twelve", "thirteen", "fourteen", "fifteen",
	"sixteen", "seventeen", "eighteen", "nineteen", "twenty", "thirty",
	"forty", "fifty", "sixty", "seventy", "eighty", "ninety",

	"hundred", "hundreds", "thousand", "thousands", "million", "millions",
	"billion", "billions", "trillion", "trillions",

	"half", "halves", "third", "thirds", "quarter", "quarters", "fifth",
	"fifths", "eighth", "eighths", "tenth", "tenths", "dozen", "dozens",

	"twice", "double", "doubled", "triple", "tripled",

	// Words that assert a specific count without spelling a numeral. The
	// manual pass produced "a couple of names" for a two-holding portfolio:
	// true, checkable, and still a figure the model wrote itself, which is
	// exactly what this list exists to stop.
	//
	// "few", "several" and "many" are absent deliberately -- they are vague
	// rather than countable, and asserting nothing specific is not the failure
	// being guarded against.
	//
	// "both" was here for one run of the manual pass and was REMOVED. It cost
	// a whole generation for "the two figures are both small", where the
	// figures had already been named by their own placeholders. "Both" refers
	// back to things already on the page; it does not claim a measurement, and
	// the rule this list serves is "the model states no figure", not "the
	// model uses no number-ish word". Widening past that costs real drafts and
	// buys nothing.
	"couple", "couples", "pair", "pairs",

	// Unit words. The renderer supplies the unit with the figure, so one of
	// these in a draft is either a duplicate or a figure written by hand.
	"percent", "percentage", "percentages", "dollar", "dollars", "cent",
	"cents", "usd", "bps",
}

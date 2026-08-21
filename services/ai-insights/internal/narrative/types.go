// Package narrative turns a computed portfolio report into prose without
// letting a language model produce any of the figures in it.
//
// The mechanism is placeholder substitution (SPEC.md §2.2). The model is given
// the report and asked to write prose in which every figure appears as a named
// token; this package supplies the token vocabulary, rejects any draft that
// carries a figure of its own, and renders the accepted draft by substituting
// values straight out of the report struct. A number the user reads was
// produced by strconv from a struct field. There is no path by which a
// model-generated token reaches the page as a figure -- not a discouraged
// path, an absent one.
package narrative

// Kind is what a placeholder's value is, which decides how it renders
// (SPEC.md §2.4). Formatting lives with the renderer, in one place, so the
// same figure cannot be spelled two ways.
type Kind uint8

// The zero Kind is deliberately not a valid one: a Value that reached the
// renderer without a kind would otherwise format through whatever the default
// branch happened to be, and silently.
const (
	KindPercent       Kind = iota + 1 // a percentage whose sign carries no meaning: a weight, a volatility
	KindSignedPercent                 // a percentage whose sign is the point: a return, an excess, a drawdown
	KindRatio                         // Sharpe and the like, dimensionless
	KindIndex                         // HHI, dimensionless and small
	KindMoney                         // USD, no symbol -- callers prepend "$"
	KindQuantity                      // a share count, possibly fractional
	KindCount                         // a whole number of things
	KindDate                          // a calendar date, "2006-01-02" as the report carries it
	KindSymbol                        // a ticker, rendered verbatim
)

// Value is one figure the model may refer to but never write.
//
// It carries the raw value and its kind, never a formatted string, and that is
// load-bearing rather than tidy (plan D1). The vocabulary is rendered into the
// prompt so the model can judge whether a drawdown is severe -- but if it were
// rendered as "-12.4%" there, the model would be looking at the exact string it
// is forbidden to produce, one copy away from a violation. Formatting happens
// after the model has been left out of it.
type Value struct {
	Kind Kind

	// Num holds every numeric kind. Text holds KindDate and KindSymbol.
	Num  float64
	Text string
}

func num(k Kind, n float64) Value { return Value{Kind: k, Num: n} }
func text(k Kind, s string) Value { return Value{Kind: k, Text: s} }

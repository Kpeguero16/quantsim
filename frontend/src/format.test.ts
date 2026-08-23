import { describe, expect, it } from 'vitest'

import {
  formatDate,
  formatIndex,
  formatPercent,
  formatPrice,
  formatQuantity,
  formatRatio,
  formatSignedPercent,
} from './format'

describe('formatPrice', () => {
  it('renders two decimals, thousands-separated', () => {
    expect(formatPrice(1234.5)).toBe('1,234.50')
  })
})

describe('formatQuantity', () => {
  it('trims trailing zeros off a round quantity', () => {
    expect(formatQuantity(10)).toBe('10')
  })

  it('preserves the minimum tradable quantity', () => {
    expect(formatQuantity(0.0001)).toBe('0.0001')
  })

  it('trims a single trailing zero', () => {
    expect(formatQuantity(1.5)).toBe('1.5')
  })
})

describe('formatDate', () => {
  // Both assertions pin the exact UTC calendar date regardless of the
  // machine running the test: formatDate always renders via
  // {timeZone: 'UTC'}, so the result never depends on the runner's local
  // zone. Run on this dev machine (US Eastern, UTC-4), a naive
  // new Date(...).toLocaleDateString() with no timeZone override would
  // render the first case as 7/31/2024 -- the exact bug this function
  // exists to prevent.
  it('renders a UTC-midnight date string on its own calendar day', () => {
    expect(formatDate('2024-08-01T00:00:00Z')).toBe('8/1/2024')
  })

  it('renders a non-UTC-offset timestamp on its own calendar day', () => {
    expect(formatDate('2024-08-09T00:00:00-04:00')).toBe('8/9/2024')
  })
})

/**
 * The parity table (SPEC.md 2.4).
 *
 * Every expectation here was PRINTED BY THE GO RENDERER
 * (services/ai-insights/internal/narrative/render.go's format(), driven
 * through KindPercent and KindSignedPercent) and pasted in. They are not
 * derived from a TypeScript port of roundHalfAway, and that distinction is
 * the whole value of this suite: a test that computes its expectation the
 * same way as the value under test proves the two agree when built the
 * same way, which is not the drift it exists to catch. Step 21's own
 * adversarial pass shipped exactly that defect and found it late.
 *
 * The cases are chosen where the two languages CAN disagree, not where
 * they obviously agree: negative two-decimal halves (toFixed gets these
 * wrong), values whose shortest decimal form is a halfway case, a value
 * above 1000 (grouping), a positive value that rounds to zero (the sign is
 * decided before rounding), and a negative value too small to show.
 */
const PARITY: [number, string, string][] = [
  // value      formatPercent   formatSignedPercent
  [-1234.5, '-1234.5%', '-1234.5%'],
  [-99.85, '-99.9%', '-99.9%'],
  [-99.55, '-99.6%', '-99.6%'],
  [-99.35, '-99.4%', '-99.4%'],
  [-98.85, '-98.9%', '-98.9%'],
  [-12.35, '-12.4%', '-12.4%'],
  [-7.25, '-7.3%', '-7.3%'],
  [-3.5, '-3.5%', '-3.5%'],
  [-2.5, '-2.5%', '-2.5%'],
  [-0.125, '-0.1%', '-0.1%'],
  [-0.05, '-0.1%', '-0.1%'],
  [-0.04, '-0.0%', '-0.0%'],
  [-0.001, '-0.0%', '-0.0%'],
  [0, '0.0%', '0.0%'],
  [0.04999, '0.0%', '+0.0%'],
  [0.05, '0.1%', '+0.1%'],
  [0.125, '0.1%', '+0.1%'],
  [2.5, '2.5%', '+2.5%'],
  [7.25, '7.3%', '+7.3%'],
  [12.34, '12.3%', '+12.3%'],
  [12.35, '12.4%', '+12.4%'],
  [98.85, '98.9%', '+98.9%'],
  [99.35, '99.4%', '+99.4%'],
  [99.55, '99.6%', '+99.6%'],
  [99.85, '99.9%', '+99.9%'],
  [100, '100.0%', '+100.0%'],
  [1234.5, '1234.5%', '+1234.5%'],
]

describe('percent formatting agrees with the backend renderer', () => {
  it.each(PARITY)('%f', (value, plain, signed) => {
    expect(formatPercent(value)).toBe(plain)
    expect(formatSignedPercent(value)).toBe(signed)
  })
})

describe('percent formatting, the signed-zero edges', () => {
  // Kept out of the table above because -0 and 0 would render the same test
  // name there. Reachable rather than theoretical: JSON.parse('-0') really
  // does produce negative zero, so a backend figure of -0 arrives as -0 in
  // the browser -- and the Go renderer prints "-0.0%" for it, sign and all.
  // Swallowing the sign on one side only is the whole class of bug this
  // suite exists to catch.
  it('keeps the sign on a negative zero', () => {
    expect(formatPercent(-0)).toBe('-0.0%')
    expect(formatSignedPercent(-0)).toBe('-0.0%')
  })

  it('leaves a positive zero unsigned', () => {
    expect(formatPercent(0)).toBe('0.0%')
  })
})

describe('formatSignedPercent', () => {
  // Zero is a measurement, not a gain: a portfolio that exactly matched its
  // benchmark has an excess return of 0, and "+0.0%" would read as a win.
  // The backend applies its "+" on Num > 0 for the same reason.
  it('leaves an exact zero unsigned', () => {
    expect(formatSignedPercent(0)).toBe('0.0%')
  })
})

/**
 * Ratio and index parity (SPEC.md 2.4, extended in T7).
 *
 * Same rule as the percent table above: every expectation was printed by
 * render.go's format() through KindRatio and KindIndex, not derived here.
 *
 * These cases are the reason format.ts rounds through a port of the
 * backend's algorithm instead of handing the raw value to toLocaleString.
 * Plain toLocaleString reads the shortest decimal form, so it renders
 * -9.995 as "-10.00" where Go renders "-9.99" -- the double behind that
 * literal is a hair below the halfway point, and Go's scale-then-round sees
 * that while Intl does not. At one decimal place the two never diverge,
 * which is why the percent formatters shipped without the port; at two and
 * three places they diverge on 272 and 184 of 270,002 values. Every case
 * below marked "the port is load-bearing" goes red if fixed() is replaced
 * by a plain toLocaleString call.
 */
describe('formatRatio agrees with the backend renderer', () => {
  it.each([
    // the port is load-bearing
    [-9.995, '-9.99'],
    [-9.915, '-9.91'],
    [-9.905, '-9.90'],
    [-9.825, '-9.82'],
    [-1.005, '-1.00'],
    [1.005, '1.00'],
    [9.995, '9.99'],
    // ordinary values, and the two halfway cases that round the same way
    // under either strategy
    [-2.675, '-2.68'],
    [2.675, '2.68'],
    [-0.125, '-0.13'],
    [5.298553258534417, '5.30'],
    [5.95294044989533, '5.95'],
    [0, '0.00'],
    // negative, too small to show, sign kept
    [-0.004, '-0.00'],
    // no thousands separator, unlike money
    [1234.567, '1234.57'],
  ])('%f', (value, want) => {
    expect(formatRatio(value)).toBe(want)
  })
})

describe('formatIndex agrees with the backend renderer', () => {
  it.each([
    // the port is load-bearing
    [-8.1885, '-8.188'],
    [-8.1855, '-8.185'],
    [-8.1825, '-8.182'],
    // an HHI lives in 0..1, which is where these three actually matter
    [0.3333333333333333, '0.333'],
    [0.1235, '0.124'],
    [1, '1.000'],
    [0, '0.000'],
    [0.0005, '0.001'],
    [0.0001, '0.000'],
    // negative, too small to show, sign kept
    [-0.0005, '-0.001'],
    [-0.0004, '-0.000'],
    [1234.5678, '1234.568'],
  ])('%f', (value, want) => {
    expect(formatIndex(value)).toBe(want)
  })
})

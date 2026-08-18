import { describe, expect, it } from 'vitest'

import { validateBacktestForm } from './backtest-validation'

const VALID = {
  symbol: 'aapl',
  shortWindow: '5',
  longWindow: '20',
  startDate: '2026-01-01',
  endDate: '2026-06-01',
  startingCapital: '10000',
}

describe('validateBacktestForm', () => {
  it('accepts a fully valid form and uppercases the symbol', () => {
    const result = validateBacktestForm(VALID)
    expect(result).toEqual({
      ok: true,
      value: {
        symbol: 'AAPL',
        short_window: 5,
        long_window: 20,
        start_date: '2026-01-01',
        end_date: '2026-06-01',
        starting_capital: 10000,
      },
    })
  })

  it('rejects an empty symbol', () => {
    const result = validateBacktestForm({ ...VALID, symbol: '  ' })
    expect(result.ok).toBe(false)
  })

  it('rejects short_window below 2', () => {
    const result = validateBacktestForm({ ...VALID, shortWindow: '1' })
    expect(result.ok).toBe(false)
  })

  it('accepts short_window at the floor of 2', () => {
    const result = validateBacktestForm({
      ...VALID,
      shortWindow: '2',
      longWindow: '3',
    })
    expect(result.ok).toBe(true)
  })

  it('rejects long_window equal to short_window', () => {
    const result = validateBacktestForm({
      ...VALID,
      shortWindow: '10',
      longWindow: '10',
    })
    expect(result.ok).toBe(false)
  })

  it('rejects long_window above 500', () => {
    const result = validateBacktestForm({ ...VALID, longWindow: '501' })
    expect(result.ok).toBe(false)
  })

  it('accepts long_window at the ceiling of 500', () => {
    const result = validateBacktestForm({
      ...VALID,
      shortWindow: '2',
      longWindow: '500',
    })
    expect(result.ok).toBe(true)
  })

  it('rejects a missing start or end date', () => {
    expect(validateBacktestForm({ ...VALID, startDate: '' }).ok).toBe(false)
    expect(validateBacktestForm({ ...VALID, endDate: '' }).ok).toBe(false)
  })

  it('rejects start_date equal to end_date', () => {
    const result = validateBacktestForm({
      ...VALID,
      startDate: '2026-01-01',
      endDate: '2026-01-01',
    })
    expect(result.ok).toBe(false)
  })

  it('rejects start_date after end_date', () => {
    const result = validateBacktestForm({
      ...VALID,
      startDate: '2026-06-01',
      endDate: '2026-01-01',
    })
    expect(result.ok).toBe(false)
  })

  it('rejects starting_capital of 0', () => {
    const result = validateBacktestForm({ ...VALID, startingCapital: '0' })
    expect(result.ok).toBe(false)
  })

  it('rejects a negative starting_capital', () => {
    const result = validateBacktestForm({ ...VALID, startingCapital: '-1' })
    expect(result.ok).toBe(false)
  })

  it('rejects a non-numeric starting_capital', () => {
    const result = validateBacktestForm({ ...VALID, startingCapital: 'abc' })
    expect(result.ok).toBe(false)
  })
})

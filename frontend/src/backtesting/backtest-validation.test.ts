import { describe, expect, it } from 'vitest'

import {
  type BacktestFormValues,
  validateBacktestForm,
} from './backtest-validation'

const COMMON = {
  symbols: 'aapl',
  startDate: '2026-01-01',
  endDate: '2026-06-01',
  startingCapital: '10000',
}

const MA_VALID: BacktestFormValues = {
  ...COMMON,
  strategy: 'ma_crossover',
  shortWindow: '5',
  longWindow: '20',
  rsiPeriod: '',
  oversold: '',
  overbought: '',
  fastPeriod: '',
  slowPeriod: '',
  signalPeriod: '',
}

const RSI_VALID: BacktestFormValues = {
  ...COMMON,
  strategy: 'rsi',
  shortWindow: '',
  longWindow: '',
  rsiPeriod: '14',
  oversold: '30',
  overbought: '70',
  fastPeriod: '',
  slowPeriod: '',
  signalPeriod: '',
}

const MACD_VALID: BacktestFormValues = {
  ...COMMON,
  strategy: 'macd',
  shortWindow: '',
  longWindow: '',
  rsiPeriod: '',
  oversold: '',
  overbought: '',
  fastPeriod: '12',
  slowPeriod: '26',
  signalPeriod: '9',
}

describe('validateBacktestForm', () => {
  describe('fields shared by every strategy', () => {
    it('rejects an empty symbol list', () => {
      expect(validateBacktestForm({ ...MA_VALID, symbols: '  ' }).ok).toBe(false)
      expect(validateBacktestForm({ ...MA_VALID, symbols: ' , , ' }).ok).toBe(false)
    })

    it('uppercases, sorts and drops the comma artifacts a human types', () => {
      const result = validateBacktestForm({
        ...MA_VALID,
        symbols: ' msft , aapl,, nvda, ',
      })
      expect(result.ok).toBe(true)
      if (!result.ok) return
      expect(result.value.symbols).toEqual(['AAPL', 'MSFT', 'NVDA'])
    })

    it('rejects a duplicate rather than quietly deduplicating it', () => {
      // Case-insensitively, matching the backend: silently running a
      // one-symbol backtest for someone who asked for two answers a
      // different question than the one asked.
      expect(validateBacktestForm({ ...MA_VALID, symbols: 'AAPL, AAPL' }).ok).toBe(false)
      expect(validateBacktestForm({ ...MA_VALID, symbols: 'aapl, AAPL' }).ok).toBe(false)
      expect(validateBacktestForm({ ...MA_VALID, symbols: 'AAPL, aapl ' }).ok).toBe(false)
    })

    it('accepts ten symbols and rejects eleven', () => {
      const ten = Array.from({ length: 10 }, (_, i) => `S${i}`).join(', ')
      const eleven = Array.from({ length: 11 }, (_, i) => `S${i}`).join(', ')
      expect(validateBacktestForm({ ...MA_VALID, symbols: ten }).ok).toBe(true)
      expect(validateBacktestForm({ ...MA_VALID, symbols: eleven }).ok).toBe(false)
    })

    it('rejects a missing start or end date', () => {
      expect(
        validateBacktestForm({ ...MA_VALID, startDate: '' }).ok,
      ).toBe(false)
      expect(validateBacktestForm({ ...MA_VALID, endDate: '' }).ok).toBe(
        false,
      )
    })

    it('rejects start_date equal to end_date', () => {
      const result = validateBacktestForm({
        ...MA_VALID,
        startDate: '2026-01-01',
        endDate: '2026-01-01',
      })
      expect(result.ok).toBe(false)
    })

    it('rejects start_date after end_date', () => {
      const result = validateBacktestForm({
        ...MA_VALID,
        startDate: '2026-06-01',
        endDate: '2026-01-01',
      })
      expect(result.ok).toBe(false)
    })

    it('rejects starting_capital of 0', () => {
      const result = validateBacktestForm({ ...MA_VALID, startingCapital: '0' })
      expect(result.ok).toBe(false)
    })

    it('rejects a negative starting_capital', () => {
      const result = validateBacktestForm({
        ...MA_VALID,
        startingCapital: '-1',
      })
      expect(result.ok).toBe(false)
    })

    it('rejects a non-numeric starting_capital', () => {
      const result = validateBacktestForm({
        ...MA_VALID,
        startingCapital: 'abc',
      })
      expect(result.ok).toBe(false)
    })
  })

  describe('ma_crossover', () => {
    it('accepts a fully valid form, uppercases the symbols, and builds a discriminated body', () => {
      const result = validateBacktestForm(MA_VALID)
      expect(result).toEqual({
        ok: true,
        value: {
          symbols: ['AAPL'],
          strategy: 'ma_crossover',
          params: { short_window: 5, long_window: 20 },
          start_date: '2026-01-01',
          end_date: '2026-06-01',
          starting_capital: 10000,
        },
      })
    })

    it('rejects short_window below 2', () => {
      const result = validateBacktestForm({ ...MA_VALID, shortWindow: '1' })
      expect(result.ok).toBe(false)
    })

    it('accepts short_window at the floor of 2', () => {
      const result = validateBacktestForm({
        ...MA_VALID,
        shortWindow: '2',
        longWindow: '3',
      })
      expect(result.ok).toBe(true)
    })

    it('rejects long_window equal to short_window', () => {
      const result = validateBacktestForm({
        ...MA_VALID,
        shortWindow: '10',
        longWindow: '10',
      })
      expect(result.ok).toBe(false)
    })

    it('rejects long_window above 500', () => {
      const result = validateBacktestForm({ ...MA_VALID, longWindow: '501' })
      expect(result.ok).toBe(false)
    })

    it('accepts long_window at the ceiling of 500', () => {
      const result = validateBacktestForm({
        ...MA_VALID,
        shortWindow: '2',
        longWindow: '500',
      })
      expect(result.ok).toBe(true)
    })
  })

  describe('rsi', () => {
    it('accepts a fully valid form and builds a discriminated body', () => {
      const result = validateBacktestForm(RSI_VALID)
      expect(result).toEqual({
        ok: true,
        value: {
          symbols: ['AAPL'],
          strategy: 'rsi',
          params: { period: 14, oversold: 30, overbought: 70 },
          start_date: '2026-01-01',
          end_date: '2026-06-01',
          starting_capital: 10000,
        },
      })
    })

    it('rejects a period below 2', () => {
      expect(
        validateBacktestForm({ ...RSI_VALID, rsiPeriod: '1' }).ok,
      ).toBe(false)
    })

    it('rejects a period above 499 (WarmupBars = period+1 <= 500)', () => {
      expect(
        validateBacktestForm({ ...RSI_VALID, rsiPeriod: '500' }).ok,
      ).toBe(false)
    })

    it('accepts a period at the ceiling of 499', () => {
      expect(
        validateBacktestForm({ ...RSI_VALID, rsiPeriod: '499' }).ok,
      ).toBe(true)
    })

    it('rejects oversold at or below 0', () => {
      expect(
        validateBacktestForm({ ...RSI_VALID, oversold: '0' }).ok,
      ).toBe(false)
    })

    it('rejects oversold equal to overbought', () => {
      expect(
        validateBacktestForm({ ...RSI_VALID, oversold: '70', overbought: '70' })
          .ok,
      ).toBe(false)
    })

    it('rejects oversold greater than overbought', () => {
      expect(
        validateBacktestForm({ ...RSI_VALID, oversold: '80', overbought: '70' })
          .ok,
      ).toBe(false)
    })

    it('rejects overbought at or above 100', () => {
      expect(
        validateBacktestForm({ ...RSI_VALID, overbought: '100' }).ok,
      ).toBe(false)
    })
  })

  describe('macd', () => {
    it('accepts a fully valid form and builds a discriminated body', () => {
      const result = validateBacktestForm(MACD_VALID)
      expect(result).toEqual({
        ok: true,
        value: {
          symbols: ['AAPL'],
          strategy: 'macd',
          params: { fast_period: 12, slow_period: 26, signal_period: 9 },
          start_date: '2026-01-01',
          end_date: '2026-06-01',
          starting_capital: 10000,
        },
      })
    })

    it('rejects a fast_period below 2', () => {
      expect(
        validateBacktestForm({ ...MACD_VALID, fastPeriod: '1' }).ok,
      ).toBe(false)
    })

    it('rejects slow_period at or below fast_period', () => {
      expect(
        validateBacktestForm({
          ...MACD_VALID,
          fastPeriod: '12',
          slowPeriod: '12',
        }).ok,
      ).toBe(false)
    })

    it('rejects a signal_period below 2', () => {
      expect(
        validateBacktestForm({ ...MACD_VALID, signalPeriod: '1' }).ok,
      ).toBe(false)
    })

    it('rejects slow_period + signal_period - 1 exceeding 500, even when slow_period alone is under 500', () => {
      // The exact case the backend's maxWarmupBars bound exists to catch
      // (Step 18 SPEC.md 2.4): slow=480 alone is fine, but 480+30-1=509 isn't.
      const result = validateBacktestForm({
        ...MACD_VALID,
        slowPeriod: '480',
        signalPeriod: '30',
      })
      expect(result.ok).toBe(false)
    })

    it('accepts slow_period + signal_period - 1 landing exactly on 500', () => {
      const result = validateBacktestForm({
        ...MACD_VALID,
        slowPeriod: '471',
        signalPeriod: '30',
      })
      expect(result.ok).toBe(true)
    })
  })
})

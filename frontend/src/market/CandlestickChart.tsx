/**
 * Daily OHLC candles for one symbol, from GET /market-data/history/:symbol.
 *
 * Uses the Lightweight Charts v5 series API: `chart.addSeries(CandlestickSeries,
 * options)`. v4's `chart.addCandlestickSeries(options)` method does not exist
 * on this version -- most examples still online show it, and it will fail
 * silently different ways (missing method) if reintroduced (SPEC.md 2.8).
 */
import { useEffect, useRef, useState } from 'react'
import {
  CandlestickSeries,
  createChart,
  type CandlestickData,
  type IChartApi,
  type ISeriesApi,
  type Time,
} from 'lightweight-charts'

import { ApiError, api } from '../api/client'
import type { Bar } from '../api/types'

/**
 * The API's `timestamp` is an RFC3339 instant at midnight of the trading
 * day, e.g. "2026-07-28T00:00:00-04:00". Lightweight Charts' business-day
 * `Time` wants just the date, "YYYY-MM-DD" -- a UTC timestamp would be
 * wrong here, since converting through `Date`/`toISOString` risks shifting
 * the calendar date across a UTC offset boundary depending on the bar's
 * timezone. The date is already unambiguous in the string itself, so this
 * takes it directly rather than parsing and reformatting.
 */
function toBusinessDay(timestamp: string): Time {
  return timestamp.slice(0, 10) as Time
}

function toCandlestick(bar: Bar): CandlestickData<Time> {
  return {
    time: toBusinessDay(bar.timestamp),
    open: bar.open,
    high: bar.high,
    low: bar.low,
    close: bar.close,
  }
}

/**
 * Sorted ascending and de-duplicated by day before reaching the chart --
 * Lightweight Charts throws on out-of-order or duplicate-time data, and the
 * API is not contractually required to guarantee either (SPEC.md's Task 5
 * acceptance criteria call this out explicitly even though the service
 * happens to return sorted data today).
 */
function prepareData(bars: Bar[]): CandlestickData<Time>[] {
  const byDay = new Map<string, CandlestickData<Time>>()
  for (const bar of bars) {
    byDay.set(bar.timestamp.slice(0, 10), toCandlestick(bar))
  }
  return [...byDay.values()].sort((a, b) =>
    (a.time as string).localeCompare(b.time as string),
  )
}

interface CandlestickChartProps {
  symbol: string
}

type LoadState =
  | { status: 'loading' }
  | { status: 'ok' }
  | { status: 'empty' }
  | { status: 'error'; message: string }

export default function CandlestickChart({ symbol }: CandlestickChartProps) {
  const containerRef = useRef<HTMLDivElement>(null)
  const chartRef = useRef<IChartApi | null>(null)
  const seriesRef = useRef<ISeriesApi<'Candlestick'> | null>(null)
  const [state, setState] = useState<LoadState>({ status: 'loading' })

  // Chart lifecycle: one chart per mount, disposed on unmount. Symbol
  // changes reuse the same chart instance and just call setData again --
  // tearing down and recreating the chart on every symbol switch is what
  // would leak canvas/resize-observer instances if the dispose ever missed
  // a path.
  useEffect(() => {
    const container = containerRef.current
    if (!container) return

    const chart = createChart(container, {
      layout: {
        background: { color: 'transparent' },
        textColor: '#9aa4b8', // --color-ink-muted; chart options take literal
        // colours, not CSS custom properties, so this and the two below
        // are kept in sync with index.css's tokens by hand.
      },
      grid: {
        vertLines: { color: '#232936' }, // --color-line
        horzLines: { color: '#232936' },
      },
      rightPriceScale: { borderColor: '#2f3746' }, // --color-line-strong
      timeScale: { borderColor: '#2f3746' },
      autoSize: true,
    })

    const series = chart.addSeries(CandlestickSeries, {
      upColor: '#26a69a', // --color-up
      downColor: '#ef5350', // --color-down
      borderVisible: false,
      wickUpColor: '#26a69a',
      wickDownColor: '#ef5350',
    })

    chartRef.current = chart
    seriesRef.current = series

    return () => {
      chart.remove()
      chartRef.current = null
      seriesRef.current = null
    }
  }, [])

  // Data fetch + setData: reruns per symbol, reusing the chart from above.
  useEffect(() => {
    let cancelled = false
    setState({ status: 'loading' })

    api
      .history(symbol)
      .then((response) => {
        if (cancelled) return
        const data = prepareData(response.bars)
        seriesRef.current?.setData(data)
        chartRef.current?.timeScale().fitContent()
        setState(data.length > 0 ? { status: 'ok' } : { status: 'empty' })
      })
      .catch((error: unknown) => {
        if (cancelled) return
        setState({
          status: 'error',
          message:
            error instanceof ApiError ? error.message : 'Could not load',
        })
      })

    return () => {
      cancelled = true
    }
  }, [symbol])

  return (
    <div className="relative h-full min-h-80">
      <div
        ref={containerRef}
        className="h-full w-full"
        role="img"
        aria-label={
          state.status === 'ok'
            ? `Candlestick chart of ${symbol} daily prices`
            : undefined
        }
      />

      {state.status === 'loading' && (
        <div
          className="absolute inset-0 grid place-items-center"
          aria-busy="true"
          aria-label={`Loading chart for ${symbol}`}
        >
          <div className="h-6 w-6 animate-pulse rounded-full bg-elevated" />
        </div>
      )}

      {state.status === 'empty' && (
        <p className="absolute inset-0 grid place-items-center text-sm text-ink-muted">
          No history for {symbol} yet.
        </p>
      )}

      {state.status === 'error' && (
        <p
          role="alert"
          className="absolute inset-0 grid place-items-center text-sm text-danger"
        >
          {state.message}
        </p>
      )}
    </div>
  )
}

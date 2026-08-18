/**
 * The one component that writes. Buy/sell form for whichever symbol is
 * currently selected in Dashboard.tsx.
 *
 * Client-side validation runs first and never calls the API on failure --
 * that's what keeps a malformed quantity from ever producing a history
 * row, the same invalid_request boundary SPEC.md 2.6 documents on the
 * backend, enforced here too rather than only relied upon there.
 *
 * No confirmation modal (SPEC.md 2.8): submit is direct.
 */
import { useEffect, useState, type FormEvent } from 'react'

import { ApiError, api } from '../api/client'
import type { Side } from '../api/types'
import { describeRejection, isRejectionReason } from './rejection-reason'

// Mirrors minQuantity/maxQuantity in
// services/trading-engine/internal/service/trading.go. Not read from a
// shared source -- the two are independent literals, commented here so a
// future change to either side is easy to grep for (SPEC.md 2.7).
const MIN_QUANTITY = 0.0001
const MAX_QUANTITY = 1e9

const INVALID_REQUEST_MESSAGE =
  'That order could not be processed. Check the quantity and try again.'

interface OrderTicketProps {
  symbol: string | null
  onOrderPlaced: () => void
}

export default function OrderTicket({
  symbol,
  onOrderPlaced,
}: OrderTicketProps) {
  // Starts unselected -- a submit with no side chosen is a client-side
  // validation failure like any other (SPEC.md 2.7's bounds check), not an
  // implicit "buy". Once chosen, it's kept across submits (SPEC.md 2.8: no
  // reset to a blank form after each order).
  const [side, setSide] = useState<Side | null>(null)
  const [quantity, setQuantity] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const disabled = symbol === null

  // A quantity typed (or an error shown) for one symbol is specific to
  // that symbol and would otherwise silently carry over to whatever gets
  // selected next -- side is deliberately left alone, since a buy/sell
  // preference isn't symbol-specific the way a typed quantity is
  // (SPEC.md 2.8).
  useEffect(() => {
    setQuantity('')
    setError(null)
  }, [symbol])

  function validateQuantity(raw: string): number | null {
    const value = Number(raw)
    if (
      raw.trim() === '' ||
      !Number.isFinite(value) ||
      value < MIN_QUANTITY ||
      value > MAX_QUANTITY
    ) {
      return null
    }
    return value
  }

  async function handleSubmit(event: FormEvent) {
    event.preventDefault()
    if (symbol === null) return

    // The `disabled` prop on the submit button only takes effect once
    // React commits the `submitting` state; a fast double-click or two
    // Enter presses can fire two submit events before that happens. This
    // guard is synchronous with the event, so it closes that gap instead
    // of relying on a render that hasn't happened yet.
    if (submitting) return

    if (side === null) {
      setError('Select buy or sell.')
      return
    }

    const parsedQuantity = validateQuantity(quantity)
    if (parsedQuantity === null) {
      setError(
        `Enter a quantity between ${MIN_QUANTITY} and ${MAX_QUANTITY.toLocaleString('en-US')}.`,
      )
      return
    }

    setError(null)
    setSubmitting(true)
    try {
      await api.placeOrder({ symbol, side, quantity: parsedQuantity })
      setQuantity('')
      onOrderPlaced()
    } catch (thrown) {
      if (thrown instanceof ApiError) {
        if (thrown.code === 'invalid_request') {
          setError(INVALID_REQUEST_MESSAGE)
        } else if (isRejectionReason(thrown.code)) {
          setError(describeRejection(thrown.code))
        } else {
          setError(thrown.message)
        }
      } else {
        setError('Could not place the order.')
      }
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <section
      aria-label="Order ticket"
      className="rounded-lg border border-line bg-surface p-4"
    >
      <h2 className="text-sm font-semibold text-ink">
        {symbol ?? 'Order Ticket'}
      </h2>

      {disabled ? (
        <p className="mt-3 text-sm text-ink-muted">
          Select a symbol to trade.
        </p>
      ) : (
        <form
          className="mt-3 space-y-3"
          onSubmit={(e) => void handleSubmit(e)}
          // The number input's min/max would otherwise let the browser
          // silently block submission via native constraint validation
          // for an out-of-range value -- handleSubmit would never run,
          // leaving a stale error from a previous attempt on screen. All
          // the same bounds are enforced in validateQuantity above, so
          // this doesn't loosen validation, it just makes this component
          // the one place that reports it.
          noValidate
        >
          <div className="grid grid-cols-2 gap-2">
            <button
              type="button"
              onClick={() => setSide('buy')}
              aria-pressed={side === 'buy'}
              className={`rounded-md border px-3 py-2 text-sm font-medium transition-colors ${
                side === 'buy'
                  ? 'border-up bg-up/10 text-up'
                  : 'border-line-strong text-ink-muted hover:bg-elevated'
              }`}
            >
              Buy
            </button>
            <button
              type="button"
              onClick={() => setSide('sell')}
              aria-pressed={side === 'sell'}
              className={`rounded-md border px-3 py-2 text-sm font-medium transition-colors ${
                side === 'sell'
                  ? 'border-down bg-down/10 text-down'
                  : 'border-line-strong text-ink-muted hover:bg-elevated'
              }`}
            >
              Sell
            </button>
          </div>

          <div>
            <label
              htmlFor="order-quantity"
              className="block text-sm text-ink-muted"
            >
              Quantity
            </label>
            <input
              id="order-quantity"
              type="number"
              inputMode="decimal"
              min={MIN_QUANTITY}
              max={MAX_QUANTITY}
              step={MIN_QUANTITY}
              value={quantity}
              onChange={(e) => setQuantity(e.target.value)}
              className="tabular mt-1 w-full rounded-md border border-line-strong bg-canvas px-3 py-2 font-mono text-ink"
              placeholder="0.0000"
            />
          </div>

          {error && (
            <p role="alert" className="text-sm text-danger">
              {error}
            </p>
          )}

          <button
            type="submit"
            disabled={submitting}
            className="w-full rounded-md bg-accent px-3 py-2 text-sm font-medium text-canvas transition-colors hover:bg-accent-hover disabled:cursor-not-allowed disabled:opacity-50"
          >
            {submitting
              ? 'Placing…'
              : side === 'buy'
                ? 'Buy'
                : side === 'sell'
                  ? 'Sell'
                  : 'Place Order'}
          </button>
        </form>
      )}
    </section>
  )
}

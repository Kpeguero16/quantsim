/**
 * The logged-in screen: watchlist with live prices, one symbol selected at
 * a time, plus the trading UI from Step 15 -- a tabbed panel
 * (Chart/Positions/Orders/Portfolio) and an order ticket pinned across
 * every tab (SPEC.md 2.2).
 */
import { useEffect, useState } from 'react'

import { formatPrice } from '../format'
import BrandMark from '../BrandMark'
import { useAuth } from '../auth/context'
import OrderTicket from '../trading/OrderTicket'
import OrdersTable from '../trading/OrdersTable'
import PortfolioSummary from '../trading/PortfolioSummary'
import PositionsTable from '../trading/PositionsTable'
import { useOrders } from '../trading/use-orders'
import { usePortfolio } from '../trading/use-portfolio'
import CandlestickChart from './CandlestickChart'
import PriceList from './PriceList'
import { usePrices } from './use-prices'
import { useSymbols } from './use-symbols'

// A stable empty-array reference. `symbolsState.symbols` is itself stable
// once loaded (it's the same array object held in state), but a fresh `[]`
// literal here would be a *new* reference on every render while loading --
// which would retrigger usePrices's effect and the selection effect below
// on every unrelated re-render (e.g. each 15s price tick).
const NO_SYMBOLS: string[] = []

type Tab = 'chart' | 'positions' | 'orders' | 'portfolio'

const TABS: { id: Tab; label: string }[] = [
  { id: 'chart', label: 'Chart' },
  { id: 'positions', label: 'Positions' },
  { id: 'orders', label: 'Orders' },
  { id: 'portfolio', label: 'Portfolio' },
]

export default function Dashboard() {
  const { user, logout } = useAuth()
  const symbolsState = useSymbols()
  const symbols =
    symbolsState.status === 'ok' ? symbolsState.symbols : NO_SYMBOLS
  const prices = usePrices(symbols)

  const [selected, setSelected] = useState<string | null>(null)
  const [tab, setTab] = useState<Tab>('chart')

  // Default to the first symbol once the watchlist arrives, without
  // overriding a selection the user already made.
  useEffect(() => {
    if (selected === null && symbols.length > 0) setSelected(symbols[0])
  }, [selected, symbols])

  // Called once here -- no child component fetches either of these itself
  // (SPEC.md 2.3). PositionsTable/PortfolioSummary get `portfolio`,
  // OrdersTable gets `orders`, OrderTicket refetches both after a fill.
  const portfolio = usePortfolio()
  const orders = useOrders()

  return (
    <div className="min-h-dvh">
      <header className="flex items-center justify-between border-b border-line px-4 py-3 sm:px-6">
        <div className="flex items-center gap-2.5">
          <BrandMark className="h-6 w-6" />
          <span className="text-base font-semibold tracking-tight text-ink">
            QuantSim
          </span>
        </div>
        <div className="flex items-center gap-4">
          {portfolio.status === 'ok' && (
            <span className="tabular font-mono text-sm text-ink-muted">
              ${formatPrice(portfolio.portfolio.balance)}
            </span>
          )}
          <span className="hidden text-sm text-ink-muted sm:inline">
            {user?.username}
          </span>
          <button
            type="button"
            onClick={logout}
            className="rounded-md border border-line-strong px-3 py-1.5 text-sm text-ink transition-colors hover:bg-elevated"
          >
            Sign out
          </button>
        </div>
      </header>

      <main className="grid grid-cols-1 gap-4 p-4 sm:p-6 lg:grid-cols-[320px_1fr_320px]">
        <section
          aria-label="Watchlist"
          className="rounded-lg border border-line bg-surface"
        >
          {symbolsState.status === 'loading' && (
            <div
              className="space-y-3 p-4"
              aria-busy="true"
              aria-label="Loading watchlist"
            >
              {Array.from({ length: 5 }).map((_, i) => (
                <div
                  // eslint-disable-next-line react/no-array-index-key -- a
                  // fixed-count skeleton list has no stable identity to key by
                  key={i}
                  className="h-9 animate-pulse rounded bg-elevated"
                />
              ))}
            </div>
          )}

          {symbolsState.status === 'error' && (
            <p role="alert" className="p-4 text-sm text-danger">
              {symbolsState.message}
            </p>
          )}

          {symbolsState.status === 'ok' && (
            <PriceList
              symbols={symbols}
              prices={prices}
              selected={selected}
              onSelect={setSelected}
            />
          )}
        </section>

        <section
          aria-label={selected ? `${selected} ${tab}` : 'Trading'}
          className="rounded-lg border border-line bg-surface"
        >
          <div
            role="tablist"
            aria-label="Trading views"
            className="flex border-b border-line px-2"
          >
            {TABS.map(({ id, label }) => (
              <button
                key={id}
                type="button"
                role="tab"
                aria-selected={tab === id}
                onClick={() => setTab(id)}
                className={`border-b-2 px-3 py-2.5 text-sm font-medium transition-colors ${
                  tab === id
                    ? 'border-accent text-ink'
                    : 'border-transparent text-ink-muted hover:text-ink'
                }`}
              >
                {label}
              </button>
            ))}
          </div>

          {tab === 'chart' &&
            (selected ? (
              <div className="h-96 p-4">
                <CandlestickChart symbol={selected} />
              </div>
            ) : (
              <p className="p-4 text-sm text-ink-muted">
                Select a symbol to see its chart.
              </p>
            ))}

          {tab === 'positions' && <PositionsTable state={portfolio} />}
          {tab === 'orders' && <OrdersTable state={orders} />}
          {tab === 'portfolio' && <PortfolioSummary state={portfolio} />}
        </section>

        <OrderTicket
          symbol={selected}
          onOrderPlaced={() => {
            portfolio.refetch()
            orders.refetch()
          }}
        />
      </main>
    </div>
  )
}

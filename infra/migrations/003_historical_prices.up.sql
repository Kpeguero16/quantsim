CREATE TABLE historical_prices (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    symbol TEXT NOT NULL,
    timeframe TEXT NOT NULL,
    timestamp TIMESTAMPTZ NOT NULL,
    open NUMERIC(20, 4) NOT NULL,
    high NUMERIC(20, 4) NOT NULL,
    low NUMERIC(20, 4) NOT NULL,
    close NUMERIC(20, 4) NOT NULL,
    volume BIGINT NOT NULL DEFAULT 0,
    CONSTRAINT unique_historical_price UNIQUE(symbol, timeframe, timestamp)
);

CREATE INDEX idx_historical_prices_symbol_timeframe ON historical_prices(symbol, timeframe);
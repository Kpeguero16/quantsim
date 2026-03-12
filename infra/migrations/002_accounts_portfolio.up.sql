CREATE TABLE accounts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    balance NUMERIC(20, 4) NOT NULL DEFAULT 0,
    currency TEXT NOT NULL DEFAULT 'USD',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
    );

CREATE INDEX idx_accounts_user_id ON accounts(user_id);

CREATE TABLE positions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    symbol TEXT NOT NULL,
    quantity NUMERIC(20, 4) NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT unique_position UNIQUE(account_id, symbol)
);

CREATE TABLE orders (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    symbol TEXT NOT NULL,
    side TEXT NOT NULL,
    quantity NUMERIC(20, 4) NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    order_type TEXT NOT NULL DEFAULT 'market',
    limit_price NUMERIC(20, 4),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
);

CREATE TABLE trades (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    order_id UUID NOT NULL REFERENCES orders(id),
    symbol TEXT NOT NULL,
    side TEXT NOT NULL,
    quantity NUMERIC(20, 4) NOT NULL,
    price NUMERIC(20, 4) NOT NULL,
    executed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
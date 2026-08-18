-- Drops both tables 007 created. backtest_trades first: it FKs to
-- backtests, and the CASCADE only helps row-level deletes, not a DROP TABLE
-- ordering.

DROP TABLE backtest_trades;
DROP TABLE backtests;

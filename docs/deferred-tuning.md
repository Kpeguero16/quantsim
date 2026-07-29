# Deferred tuning — revisit under real traffic

Items deliberately left alone because the right value depends on traffic shape
we do not have yet. None are bugs; all are defaults that are fine for local
development and wrong-ish for a deployed service.

**Revisit when:** Phase 4 puts QuantSim on AWS and there is real request
volume to measure. Guessing now would bake in numbers with nothing behind
them — the point of deferring is to set these from observed behaviour rather
than from taste.

---

## 1. Gateway HTTP server has only `ReadHeaderTimeout`

**Where:** `services/gateway/cmd/server/main.go` — the `http.Server` literal.

**Now:** `ReadHeaderTimeout: 10s`, everything else at Go's defaults.

**Why it matters later:** `IdleTimeout` is unset, and when it is zero Go falls
back to `ReadTimeout`, which is also zero — so idle keep-alive connections are
never reaped. A client can hold connections open indefinitely. On a laptop
that is nothing; on a public instance it is a cheap way to exhaust file
descriptors.

**Likely fix:** add `IdleTimeout` (~120s) and a `WriteTimeout` chosen to sit
above the slowest legitimate backend response. Deliberately **do not** set
`ReadTimeout` — it caps the whole request including the body, which would break
large uploads and any future streaming endpoint.

**What to measure first:** p99 backend response time (sets the `WriteTimeout`
floor) and steady-state idle connection count.

---

## 2. Proxy transport `MaxIdleConnsPerHost` is 2

**Where:** `services/gateway/internal/proxy/proxy.go` — `NewTransport`.

**Now:** clones `http.DefaultTransport`, inheriting `MaxIdleConnsPerHost: 2`.

**Why it matters later:** with only two backends, more than two concurrent
in-flight requests per backend forces a fresh TCP handshake per request. The
transport is shared across proxies specifically so connections pool; a limit of
2 means it barely does. Irrelevant at one-developer volume, visible as added
per-request latency under concurrency.

**Likely fix:** `MaxIdleConnsPerHost: 32` or so, tuned against measured
concurrency. Raising it costs idle sockets, so it is worth a number rather than
a guess.

**What to measure first:** concurrent in-flight requests per backend at peak,
and the ratio of new connections to reused ones.

---

## Related decisions recorded elsewhere

- **Graceful shutdown** — none of the three services drain on SIGTERM
  (market-data's poller included). Recorded in
  `docs/archive/phase1-step6-market-data-live/SPEC.md` §2.9. Becomes real when
  deploys start rolling rather than restarting.
- **Service-to-service auth** — backends trust anything that reaches them; the
  Phase 1 control is loopback binding (`BIND_ADDR`, and the `127.0.0.1` port
  bindings in `docker-compose.yml`). Recorded in the Step 7 spec §2.4. Needs a
  real answer once the services do not share a host.
- **Rate limiting** — deliberately out of scope for the gateway in Phase 1
  (Step 7 spec §8). The gateway is the natural place for it. Note that
  `X-Forwarded-For` is now trustworthy, which is what a per-IP limiter needs.

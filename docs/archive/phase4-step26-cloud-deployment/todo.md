# Todo — Cloud deployment (Step 26)

Tracks `tasks/plan.md`'s 6 tasks and 2 checkpoints. **T1-T5 and both checkpoints done. T6 (provision) is BLOCKED on an AWS account that does not exist yet, and is the only thing left.**

Branch `step26-cloud-deployment`, cut from `main` at `17116c9`. Root `SPEC.md` and `tasks/` stay untracked as always.

---

## State of the machine

**Everything is put back.** Container stack down, two containers running (Postgres, Redis), no host processes.

Database restored and **verified by query**: `users=20 accounts=20 trades=0 orders=0 positions=0 backtests=0`, `historical_prices=3525`. No `insights:*` or `narrative:*` keys.

**This step cost $0.00.** The narrative endpoint was never called.

---

## The two things this step found by building rather than by specifying

### 1. A proxy in front of the gateway silently breaks its rate limiter

This is the one that would have shipped.

`clientIP` reads `r.RemoteAddr` and deliberately nothing else -- `X-Forwarded-For` is client-authored at that hop, and `docs/security-backlog.md` records a previous claim to the contrary as wrong. Put Caddy in front and `RemoteAddr` becomes Caddy's container address on **every** request. The limiter does not break; it works perfectly, on one key, for the entire internet. **100 requests per 15 minutes shared by everybody**, arriving as "login is broken for everyone" rather than as anything resembling a rate limit.

Fixed with `TRUSTED_PROXIES`, **empty by default** so every existing test passes unmodified. When the peer is trusted, the client is the **rightmost** `X-Forwarded-For` entry -- the only one a client cannot forge, since the proxy appends the real address to whatever was sent. Leftmost is the reflex and it reintroduces exactly the bypass the original comment describes.

Correct for **one** hop, which is why §2.4 collapsed nginx-plus-Caddy into Caddy alone.

### 2. `no-new-privileges` makes the Caddy binary unexecutable

`caddy` ships with `cap_net_bind_service=+ep`. The kernel refuses to exec a file carrying capabilities under `NoNewPrivs`, so the container crash-looped on `exec /usr/bin/caddy: operation not permitted` -- an error naming neither cause nor fix.

Resolved by removing the privilege rather than the sandbox: `setcap -r` at build time, and HTTP/HTTPS moved to high ports inside the container with the host publishing 80 and 443 onto them. Nothing in that container binds a privileged port now.

---

## T1 — One origin. Done.

Caddy serves the bundle and proxies `/auth`, `/market-data`, `/trading`, `/backtests`, `/insights`. `BASE_URL` defaults to `''`. Vite's dev server proxies the same list.

`/backtests` is listed twice in both places, bare and prefixed: the collection is requested at exactly that path and a prefix pattern alone does not match it. Missing the bare entry 404s the backtest tab against the static file server, which reads as a broken backtesting service.

## T2 — The bundle names no host. Done.

| | |
|---|---|
| new bundle | **0** hits for `localhost:8080`; the only absolute URLs left are an SVG namespace, React's docs link and TradingView's attribution |
| **control**, Step 25's bundle | **1** hit |

The `env -u` branch in `Dockerfile.frontend` is gone with it -- it existed only because `??` does not fall back on an empty string, and there is nothing to pass any more.

## T3 — `CORS_ALLOWED_ORIGIN`. Done.

Default keeps `make run-*` working with no new variable. Checked directly rather than through a browser, since T1 stops the browser exercising CORS at all:

| Origin | ACAO |
|---|---|
| `http://localhost:5173` (default) | echoed |
| `https://evil.example.com` | absent |
| after setting the variable to the real domain | only the real domain echoed; the old default stops matching |

## T4 — The healthcheck binary. Done.

A ~30-line stdlib-only module at `infra/docker/healthcheck`, built once in the builder stage and copied into all six images. All six report healthy. It discriminates: exit 1 against a dead port, exit 1 with `PORT` empty, exit 0 in situ.

**Visibility, not recovery.** Docker does not restart an unhealthy container. What changed is that `docker compose ps` stops reporting a hung service as fine.

## T5 — Production overlay and runbook. Done.

`docker-compose.prod.yml`, `infra/deploy/deploy.sh`, `infra/deploy/fetch-secrets.sh`, `docs/DEPLOYMENT.md`.

**`!reset` was the wrong tag and produced no ports at all.** Compose appends list entries when merging overlays, so the base file's loopback 5173 binding survives into production, pointing at a port Caddy no longer listens on. `!reset` removes the key *and discards the entries written under it* -- the merged config had zero published ports and looked plausible. `!override` is the tag that replaces a list.

**The frontend healthcheck pointed at a port that does not exist in production.** It targeted the site port, which moves to `http_port`/`https_port` the moment `SITE_ADDRESS` is a hostname. The container would have reported unhealthy forever, which is worse than no healthcheck because it also hides every real failure. There is now an internal `:5199` health listener that never moves.

## T6 — Provision. BLOCKED.

No AWS account. `docs/DEPLOYMENT.md` is the runbook and `SPEC.md` §5 is its summary. Khalil runs `aws configure` himself; I never handle the keys.

**The domain question is answered.** `khalilpeguero.me` already resolves to GitHub Pages via its apex; `quantsim.khalilpeguero.me` is a separate A record pointing at the Elastic IP and leaves Pages untouched.

---

## Verification

| | |
|---|---|
| Journey through **one origin** | register 201, login 200, `/me` 200, price 200, order **filled** 201, positions 200, insights **200**, backtests 200 -- all via 5173, nothing published on 8080 |
| **Rate limiting behind the proxy** | budget spent from one client → 429; a **different** client on the network → 401, its own budget |
| **Control**, `TRUSTED_PROXIES` empty | the same different client → **429 without making a single request**. The defect, reproduced |
| Mutation testing, `clientip.go` | **10 run, 10 killed.** One more was equivalent and the code it covered was deleted |
| Dev loop | `npm run dev` serves the page and proxies `/auth/login` and `/backtests` to the gateway |
| Healthchecks | six services healthy; the binary exits 1 on a dead port and 1 on an unset `PORT` |
| Prod overlay | merged config publishes exactly 80 and 443, Postgres and Redis on loopback, gateway nothing; `/data` a volume, no stray 5173 |
| `.env` in images | 0 files, 0 secret hits |
| Migrations gate | dirty schema → migrate exit 1, no app service starts |
| `make docker-up` | 2 containers, still |
| Backend | `make vet` clean, `make test` green, `make test-integration` **63/0**, `GOWORK=off` **8/8** |

---

## Things that will trip you up

**`docker compose config` reporting no ports is a merge bug, not an empty overlay.** `!reset` on a list removes the key and everything under it.

**`no-new-privileges` and file capabilities are mutually exclusive.** Any image whose binary carries `setcap` capabilities will crash-loop under it with `operation not permitted` and no further explanation.

**A healthcheck pinned to a port that moves between environments is worse than none.** It reports unhealthy forever and hides real failures behind the noise.

**Do not put a second proxy in front of Caddy.** The rightmost `X-Forwarded-For` entry stops being the client and becomes the inner proxy, and the rate limiter goes back to one shared bucket. Nothing fails; it just stops protecting anyone.

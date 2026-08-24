# SPEC — Dockerization (Step 25)

Status: **Implemented and verified, browser pass included.** §4.11 carries the results. §2.12 was found by implementing this rather than by specifying it, and it is the one that would have shipped: the first run pointed the whole stack at the wrong database and every check but one passed anyway. §5 carries what deployment still has to answer.

Scope: `infra/docker/` gains the images, `docker-compose.yml` gains the seven application services behind a profile, `.dockerignore` and `.env.example` are new or updated, and the `Makefile` gains targets for the containerized stack. **No Go source changes, no frontend source changes, no migration, no schema change, and no change to any figure, threshold or rule.**

Prior specs archived at `docs/archive/phase1-step4-auth/` through `docs/archive/phase4-step24-report-cache-invalidation/`.

---

## 1. Objective

Today the stack runs as eight processes a person starts by hand in eight terminals, against two containers. That is fine on this laptop and it is the whole obstacle to the next roadmap item: there is no artifact to deploy, and `agents.md` §231 names the target as EC2 running docker-compose.

**Objective:** `docker compose --profile app up -d` brings up the entire stack from a clean checkout, and a browser at `http://localhost:5173` behaves exactly as it does today with `make run-*`.

The word doing the work there is *exactly*. This step is a packaging change and nothing else. If any behaviour differs between the two ways of running, that difference is a defect in this step, not a property of containers.

**Non-goals:**

- **Cloud deployment.** The next roadmap item, and it is separate on purpose: §6 lists three things it must decide that this step deliberately does not.
- **A registry, image tags or CI.** Images are built locally by compose. Nothing pushes anywhere.
- **Making `allowedOrigin` env-driven.** §2.9 keeps the containerized frontend on port 5173 so the hardcoded constant stays correct. It has to change at deployment, not here.
- **Container orchestration.** No Kubernetes, no swarm, no Terraform. `agents.md` lists all three as later or stretch.
- **Consolidating the four Redis client construction sites.** Still open from Step 24, unaffected by this, and not made better or worse by it.
- **Anything in `docs/deferred-tuning.md`.** Those defaults want traffic shape, and containerizing produces none.

---

## 2. Design decisions

### 2.1 The app services go behind a compose profile, so `make docker-up` keeps meaning what it means today

`docker compose up -d` currently starts Postgres and Redis, and that is the first half of the daily loop: datastores in Docker, services with `go run` so a code change is one restart away. Adding seven services to the default set would silently change that command into "start everything", and the host loop would then be racing containers for ports 8080 through 8085.

So every application service gets `profiles: [app]`. `docker compose up -d` is unchanged, byte for byte, in what it starts. `docker compose --profile app up -d` starts the full stack. The repo already uses exactly this mechanism for pgAdmin's `tools` profile, so it introduces no new concept.

The migration one-shot (§2.8) sits in the `app` profile too, for the same reason: `make docker-up` must not start applying migrations to the dev database as a side effect of a command that has never done that.

### 2.2 One parameterized Dockerfile for the six Go services, not six copies

The six services build identically: same Go version, same `CGO_ENABLED=0`, same static binary, same base image. The only difference is a path.

Six near-identical Dockerfiles is the standard shape and it is wrong here. They do not stay identical. One gets a Go bump, one gets a build flag, and the drift is invisible because nothing ever diffs them. `infra/docker/Dockerfile.service` takes `ARG SERVICE` and compose passes it per service.

This is the opposite call from Step 24's Redis client, which was deliberately copied rather than extracted, and the difference is what the duplication would cost. There, extraction meant pushing `go-redis` into a module that `backtesting` imports and does not use. Here, extraction costs one `ARG` and buys a single place where the build is defined.

`market-data` is the odd one: it is the only service with no dependency on `pkg`. Copying `pkg` into its build is harmless, since without a `replace` directive naming it, nothing resolves to it.

### 2.3 The build context is the repo root, and `go.work` must never enter it

Every service module except `market-data` carries `replace github.com/kpeguero/quantsim/pkg => ../../pkg`. A relative replace means the build needs `pkg/` on disk at that relative path, so the build context is the repo root and the image reproduces the `pkg/` + `services/<name>/` layout. A per-service context cannot work, and failing to notice that is how this step ends with five images and one that will not build.

`go.work` is the trap next to it. If it lands in the context, the build silently uses the workspace, and then the image builds fine while `GOWORK=off go build ./...` is the only thing standing between this repo and a broken clean build. Step 20 already found two modules that did not build off-workspace, and the checklist recorded the reason it mattered: *a standard Go Dockerfile is precisely the `GOWORK=off`, clean-cache case this breaks*.

Two guards, because they fail differently. `.dockerignore` excludes `go.work` and `go.work.sum`, so the file is not there. `ENV GOWORK=off` in the builder, so it would not be used if it were. The first can be quietly undone by someone adding a broad exception to `.dockerignore`; the second cannot.

### 2.4 Distroless, nonroot, read-only, and what that costs

Runtime base is `gcr.io/distroless/static-debian12:nonroot`: no shell, no package manager, no libc, uid 65532. With `read_only: true`, `cap_drop: [ALL]` and `no-new-privileges` in compose. The services write no files, so read-only costs nothing and is a real constraint rather than a decoration. `ca-certificates` is present in that image, which `market-data` needs for Alpaca and `ai-insights` for Anthropic.

**What it costs is `docker exec sh`, and that is worth stating before someone needs it at 1am.** Debugging is `docker logs`, or rebuilding with `--target build` and running the builder stage, which has a full toolchain. That is a worse debugging story than alpine, and it is the trade taken: this repo is public and the next step puts it on a public IP.

**No `HEALTHCHECK` on the Go services**, which follows from the same choice: without a shell there is nothing to run one with, short of compiling a second binary into every image. Each service already serves `/healthz` and anything that wants it can reach it over the network. The ordering that actually matters in compose is migrations, and that uses `service_completed_successfully`, not health. Postgres, Redis and the frontend's nginx keep the healthchecks they can run.

Checked rather than assumed: **no production code path loads a timezone.** `time.LoadLocation` appears once in the repo, in `ai-insights`' calendar *test*, which is not in the image. If that ever moves into a service, `static-debian12` has no tzdata and the failure is at runtime.

### 2.5 `BIND_ADDR` must be `0.0.0.0` in every container, and the default is the exact opposite

This is the one that will burn an hour if it is not written down first.

Every Go service defaults `BIND_ADDR` to `127.0.0.1`, and `.env` sets it to `127.0.0.1` explicitly, both on purpose: auth and market-data have no authentication of their own and expect the gateway in front of them. Inside a container, `127.0.0.1` is the container's own loopback, so a service bound there is reachable by nothing, including the gateway on the same compose network. The symptom is `connection refused` from the gateway to `http://auth:8081`, which reads exactly like the auth container being down while it sits there logging that it is listening.

Compose sets `BIND_ADDR=0.0.0.0` per service, and this is precisely the case `.env.example`'s own comment carves out: *only set this to 0.0.0.0 when running behind a real network boundary (a container network, a security group)*. The boundary is real, and §2.10 is what makes it real.

### 2.6 Per-service `environment:`, not `env_file: .env`

`.env` names hosts that do not exist inside the compose network. `DATABASE_URL` says `localhost:5432`, `REDIS_URL` says `localhost:6379`, and the five `*_SERVICE_URL` entries say `localhost:808x`. Every one of those is correct for `make run-*` and wrong for a container, where the hostnames are `postgres`, `redis`, `auth`, `market-data` and so on.

So the app services do not use `env_file`. Compose composes the URLs from the credential variables that *are* portable:

```
DATABASE_URL: postgres://${POSTGRES_USER}:${POSTGRES_PASSWORD}@postgres:5432/${POSTGRES_DB}?sslmode=disable
REDIS_URL: redis://redis:6379/0
AUTH_SERVICE_URL: http://auth:8081
```

`.env` keeps its `localhost` values untouched, so the host loop keeps working, and neither set of values can drift into the other.

One sharp edge: `POSTGRES_PASSWORD` is interpolated into a URL, so a password containing `@`, `:`, `/` or `#` produces a connection string that parses wrong or not at all. The current dev password does not, and the failure is loud, but `.env.example` should say so where the password is set rather than where the URL is built.

### 2.7 Each container gets only the secrets it needs

Today the Makefile does `-include .env` and `export`, so every `make run-*` target hands every service the whole file. The gateway process has `ALPACA_API_SECRET` in its environment. `backtesting` has `ANTHROPIC_API_KEY`. Nothing reads them, and nothing stops a future line of code, or a crash dump, or a debug endpoint, from doing so.

Compose lists environment per service, so the split is free and worth taking: `ALPACA_*` reaches only `market-data`, `ANTHROPIC_*` only `ai-insights`, `JWT_SECRET` only the five services that verify or sign tokens, and `DATABASE_URL` only the four that own tables. This is not a fix for a known bug. It is a smaller blast radius for the same money, and the containerized stack is the one that will run somewhere public.

### 2.8 Migrations run as a one-shot service, and the app waits for it

A pinned `migrate/migrate` image, `infra/migrations` mounted read-only, `depends_on: postgres: service_healthy`, running `up`. Every app service then waits on `migrate: service_completed_successfully`.

Two things this buys. The `golang-migrate` CLI stops being a prerequisite for running the stack, which currently requires a `go install` and a `PATH` that does not include `$(go env GOPATH)/bin` by default. And a schema older than the code becomes impossible to start with, rather than something discovered as a 500 from a missing column.

`up` on an already-migrated database is a no-op, so this is idempotent across restarts. A **dirty** migration state fails the one-shot and therefore stops the whole stack, which is right: the alternative is six services starting against a half-applied schema.

Host `make migrate-up` stays exactly as it is, for the `make run-*` loop. Two paths to the same migrations is a small cost and the alternative is worse in both directions.

### 2.9 The frontend's API URL is fixed at build time, and that is the fact that bites at deployment

`frontend/src/api/client.ts` reads `import.meta.env.VITE_API_BASE_URL ?? 'http://localhost:8080'`. Vite substitutes that during `npm run build`, so it is in the compiled bundle. **Setting `VITE_API_BASE_URL` on a running frontend container does nothing at all**, and it looks like it should work, which is the problem.

For this step that costs nothing, because the default is already right: the gateway publishes on `127.0.0.1:8080` and the browser is on the host. So the frontend image takes a `VITE_API_BASE_URL` build arg defaulting to empty, and local compose does not set it.

The frontend serves on **5173**, not 80. That is not aesthetic. The gateway's `allowedOrigin` is the compile-time constant `http://localhost:5173`, so serving the container anywhere else makes every API call fail CORS, which reads as a broken gateway rather than a moved port. Keeping the port keeps the constant true and keeps this step to packaging. §6 owns what deployment does about it.

**No SPA fallback in nginx.** `App.tsx` records that `react-router-dom` was deliberately not added, so there are no client-side routes and nothing to fall back for. `try_files $uri /index.html` would turn every typo into a silent index page. Real 404s.

### 2.10 Only the gateway and the frontend publish ports

`127.0.0.1:8080` for the gateway and `127.0.0.1:5173` for the frontend, matching the existing convention and its reason: Docker publishes ports with NAT rules that bypass the host firewall, so a published port is a reachable port.

Auth, market-data, trading-engine, backtesting and ai-insights publish nothing. They are reachable only from the compose network, which is a stronger boundary than the one they have today, where `make run-auth` puts an unauthenticated service on the host's loopback alongside everything else the laptop is running.

Postgres and Redis keep their current published ports. `make test-integration` connects from the host to 5432 and must keep working, and the psql and redis-cli checks that every recent step has leaned on run from the host too.

### 2.11 No source changes, in either language

Every knob this needs already exists and is already env-driven: `PORT`, `BIND_ADDR`, `DATABASE_URL`, `REDIS_URL`, the five service URLs, the Alpaca and Anthropic keys, and the rate-limit settings. Nothing about containerizing this stack requires a line of Go or TypeScript.

That is worth stating as a constraint rather than an observation. If this step finds itself editing a service, either it has found a genuine gap worth its own decision, or it is quietly turning a packaging change into a behaviour change. `git diff --stat` at the end must show no `.go` and no `.ts`/`.tsx` files.
### 2.12 The container database name is `POSTGRES_APP_DB`, not `POSTGRES_DB`

Added after the first run of this stack, which is the only reason it is worded like a warning.

`POSTGRES_DB` is the obvious variable to build a connection string from and it is the wrong one. It names the database the postgres image creates at first boot. On the reference machine that is `quantsim`, an **empty decoy**; the application lives in `postgres`, which is what `DATABASE_URL` has always said, and both `README.md` and `docs/PHASE2_CHECKLIST.md` record the split deliberately.

Deriving from it pointed every container at the decoy, and **nothing looked wrong**. The migrate one-shot created the schema there on demand, so `/healthz` returned 200, registration returned 201, the order filled at the correct price and positions came back right. One endpoint noticed: `GET /insights/portfolio` returned 404 `symbol_unavailable`, because it is the only one that needs data that was supposed to already exist.

So compose reads `POSTGRES_APP_DB`, defaulting to `postgres` to match what both `.env` and `.env.example` put in `DATABASE_URL`. Compose cannot parse a database name out of a URL, so a second variable is the only way to say this, and `.env.example` says why at length rather than leaving the next person to rediscover it.

The general shape is worth keeping separately from the fix: **a stack pointed at an empty database is indistinguishable from a healthy one as long as something will create the schema on demand.** Migrations running automatically is what makes that possible, and it is otherwise a good idea.

---

## 3. The change

`infra/docker/Dockerfile.service` (new)

- Multi-stage. Builder `golang:1.25`, `ENV GOWORK=off`, `ARG SERVICE`. Copies `pkg/go.mod`, `pkg/go.sum` and `services/$SERVICE/go.{mod,sum}` first, then `go mod download`, then sources, so a dependency layer survives a code change. `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w"`.
- Runtime `gcr.io/distroless/static-debian12:nonroot`, binary only, `USER nonroot`.

`infra/docker/Dockerfile.frontend` (new)

- Builder `node:22`, `npm ci`, `ARG VITE_API_BASE_URL`, `npm run build`.
- Runtime `nginxinc/nginx-unprivileged:alpine` with `infra/docker/nginx.conf`, listening on 5173, no SPA fallback, gzip on, a healthcheck it can actually run. The unprivileged variant rather than stock nginx: stock drops privileges itself and needs `CAP_SETUID` to do it, which `cap_drop: ALL` removes.

`.dockerignore` (new)

- **Deny by default**: `*`, then `!pkg/`, `!services/`, `!frontend/`, `!infra/docker/`, then `frontend/node_modules/`, `frontend/dist/`, `**/*_test.go`, `**/testdata/` inside those.
- The usual shape is the opposite (list what to exclude) and it fails open: anything new at the repo root is in the context until someone remembers a line. The two entries that matter are the ones this makes impossible to forget, `.env` and `go.work` (§2.3, §4.3).

`docker-compose.yml`

- Seven services under `profiles: [app]`, plus the `migrate` one-shot. Per-service `environment:` per §2.6 and §2.7, `BIND_ADDR=0.0.0.0` per §2.5, published ports per §2.10, `read_only`/`cap_drop`/`no-new-privileges` per §2.4, `restart: unless-stopped`.
- Postgres, Redis and pgAdmin untouched.

`Makefile`

- `stack-up`, `stack-down`, `stack-build`, `stack-logs`, `stack-ps`, wrapping `docker compose --profile app`. `docker-up` unchanged, and §4.7 guards that; `docker-down` had to change, for the reason in §4.11.

`.env.example`

- The password-in-a-URL edge from §2.6, and a short note that the container stack overrides the `localhost` URLs rather than reading them.

`README.md`

- A "Run the whole stack in Docker" section next to the existing local-development one. The `golang-migrate` prerequisite becomes conditional on the host loop.

---

## 4. Verification

Infrastructure has no unit tests, so every check below is a runtime one, and the ones that matter carry a control. A check that passes against a broken configuration is the failure mode this whole file exists to avoid.

1. **The full journey through the containerized stack.** From `docker compose down -v`: bring up `--profile app`, then register, login, fetch a price, place an order, fetch `/insights/portfolio`, all through the published gateway. This is the only check that proves the thing works.
2. **`BIND_ADDR` is load-bearing (control).** Remove the override for `auth` alone and confirm the gateway gets a connection failure. Without this, §2.5 is a claim.
3. **No image contains `.env`.** `docker create` plus `docker export | tar -t` and grep. There is no shell to check from the inside, which makes the export the check rather than a fallback.
4. **Nothing internal is published.** `docker compose ps` shows exactly 8080, 5173, 5432 and 6379, all on 127.0.0.1.
5. **The build does not use the workspace.** `docker compose build --no-cache` with `go.work` present in the working tree, plus confirming it is absent from the context. Step 20's finding says this is the case that has actually broken before.
6. **Migrations gate the stack.** From an empty database the one-shot reaches version 9 and the services start. Forced dirty, the one-shot fails and no app service starts.
7. **`make docker-up` still starts two containers (regression guard).** The daily loop is the thing most likely to be broken by this step and least likely to be noticed.
8. **Step 24 survives containerization.** A fill through the containerized trading-engine deletes `insights:{user_id}` and the refetched report agrees with Postgres. It has `REDIS_URL` in compose, and a stack that omits it silently reintroduces that defect.
9. **Read-only root filesystem holds.** The services run the full journey without a write failure, which is what turns §2.4 from an assumption into a fact.
10. **`make test`, `make vet` and `make test-integration` are unchanged.** Integration in particular, since it reaches Postgres on the host.

The narrative endpoint stays untouched. It is the only billed path, and Step 24 means a fill now triggers a generation.

### 4.11 What the runs showed

| # | Check | Result |
|---|---|---|
| 1 | Full journey through the containerized gateway | register 201, login 200, `/me` 200, price 200, order **filled** 201, positions 200, insights **200**, backtests 200 |
| 2 | **Control:** `BIND_ADDR=127.0.0.1` for auth alone | auth logs `listening on 127.0.0.1:8081`, gateway logs `dial tcp 172.18.0.5:8081: connect: connection refused`, login **502**. Restored: 401 |
| 3 | `.env` in any image | 0 files, 0 hits for `JWT_SECRET`, 0 for `ALPACA_API_SECRET`, across all seven. **Control:** an image built to contain `.env` scores 1 and 1 |
| 4 | Published ports | exactly 8080, 5173, 5432, 6379, all on 127.0.0.1 |
| 5 | `build --no-cache` with `go.work` in the tree | exit 0, 7/7 |
| 6 | Dirty schema | `error: Dirty database version 9`, migrate exit 1, **no app service started** |
| 7 | `make docker-up` | 2 containers, unchanged |
| 8 | Step 24 in containers | `insights:{user}` 1 → 0 on the fill, refetched report agrees with Postgres at 2 trades |
| 9 | Hardening on **running** containers | `readonly=true cap_drop=[ALL]` on all seven, six as `nonroot`, nginx as uid 101 |
| 10 | `make vet` / `test` / `test-integration` | clean / green / **63 PASS, 0 FAIL, 0 SKIP** |
| §2.11 | Source files touched | **0** `.go`, `.ts`, `.tsx` |

Frontend, mechanically: `GET /` 200, `/does-not-exist` **404** (no SPA fallback), assets `public, max-age=31536000, immutable` as one header, `/index.html` `no-cache`, and a preflight from `Origin: http://localhost:5173` answered 204 with the matching `Access-Control-Allow-Origin`.

### 4.12 The browser pass

Signed in against the containerized stack and worked through every tab. Chart, watchlist, positions, order history, portfolio, backtests and insights all rendered. Two market orders placed from the ticket (GOOGL 60, MSFT 5) filled and moved the balance to $75,970.54, which reconciles to the cent against the three fills. Three backtests ran, writing 70 trade rows.

Every XHR went to `http://localhost:8080` and returned 200 -- so the compiled-in API base URL (§2.9) is right and CORS holds from the containerized origin. **No console errors, none.**

Two things confirmed themselves here that no unit test reaches:

- **Step 24's invalidation, through the UI.** The report read 1 trade before the browser orders and 3 after, with no stale window in between.
- **§2.7's consequence, priced.** Two narrative generations landed on two different `narrative:{user}:{hash}` keys, because each fill changed the report and therefore the hash. That is Step 23 and Step 24 working together exactly as Step 24 §2.7 predicted, and it is the first time this project has paid for it.

**Cost: 2 narrative generations.** Not $0.00, unlike Steps 23 and 24, and spent deliberately: the insights tab fires the narrative from a mount effect rather than a button, so the tab cannot be looked at for free.

**One change fell outside the plan.** `make docker-down` now takes down every profile. Compose's `down` ignores services whose profile is not active, so a plain `docker compose down` after `stack-up` leaves seven containers referencing the network it just removed, and the next `stack-up` fails with `network <hash> not found` -- an error naming nothing that leads back to the cause. `docker-up` is untouched, which is the promise §2.1 actually makes.

---

## 5. Open

**The CORS origin has to become configuration, and deployment is where.** `allowedOrigin` is a compile-time constant, correct for exactly one origin. The moment the frontend is served from anything other than `http://localhost:5173`, every API call fails preflight. Its comment says a CORS origin is not a knob worth exposing "before something needs to turn it". Deployment is that something.

**So does `VITE_API_BASE_URL`, and it is worse, because it is baked.** Changing the API host means rebuilding the frontend image, so image and environment stop being separable. The alternatives are a runtime config file the app fetches at startup, or serving the frontend and the API on one origin behind a single proxy, which would delete the CORS question as well. Both are deployment decisions with real trade-offs and neither belongs here.

**Secrets come from `.env` and will not, in a deployment.** Compose interpolation from a file on disk is right for a laptop. On EC2 it is a file of credentials sitting next to the application, and `agents.md` already names a secrets manager as the target.

**No image is tagged, versioned or pushed anywhere.** Compose builds locally, so "the image" is whatever was last built. Deployment needs a registry and a tagging scheme, and CI needs to build them.

**Nothing restarts a hung service.** `restart: unless-stopped` covers a crash, not a process that is up and answering nothing, and §2.4 means there is no in-container healthcheck to notice. Fine on a laptop, a real gap on a single EC2 box.

**Still open from Step 24, and untouched by this:** `auth` and `market-data` build Redis clients without `ContextTimeoutEnabled`, so their context deadlines are inert. Its own step.

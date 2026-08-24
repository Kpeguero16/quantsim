# Plan — Dockerization (Step 25)

Branch `step25-dockerization`, to be cut from `main` at `f86c74f`.

The risk here is not the Dockerfiles. It is that a containerized stack can look completely healthy while differing from the host stack in ways nothing checks: a service bound where nothing can reach it, a URL pointing at a host that does not exist, a build quietly leaning on `go.work`, or a secret baked into a layer. Every task below ends with something that would catch its own failure.

Five tasks, two checkpoints.

---

## T1 — The service image

`infra/docker/Dockerfile.service` and `.dockerignore`. Repo-root context, `ARG SERVICE`, `GOWORK=off`, distroless nonroot.

Build all six by hand before compose exists, so a build failure is a build failure rather than a compose failure. `market-data` is the one that differs, since it does not depend on `pkg`.

Acceptance: six images build with `--no-cache`, each runs and refuses to start with a clear message when its required env is missing, and `go.work` is provably absent from the build context.

## T2 — Compose, the app profile, and the migration one-shot

The seven services plus `migrate`, per SPEC §2.5 through §2.8 and §2.10. Per-service `environment:`, `BIND_ADDR=0.0.0.0`, only gateway and frontend published, hardening flags on.

Acceptance: `docker compose up -d` still starts exactly Postgres and Redis. `--profile app` starts everything, migrations run first, and every service logs that it is listening.

## T3 — The frontend image

`Dockerfile.frontend`, `nginx.conf`, port 5173, no SPA fallback. The build arg exists and is unset locally.

Acceptance: the page loads at `http://localhost:5173` and its API calls reach the gateway without a CORS error.

## T4 — Makefile, `.env.example`, README

`stack-*` targets alongside the untouched `docker-up`. The password-in-a-URL edge documented where the password is set. A Docker section in the README, with the `golang-migrate` prerequisite made conditional.

Acceptance: someone following the README from a clean clone reaches a working stack without a Go toolchain or the migrate CLI.

## T5 — The adversarial pass

SPEC §4's ten checks, with the controls run rather than assumed. The three that carry the step are §4.2 (remove the `BIND_ADDR` override and confirm it breaks), §4.3 (`.env` absent from every image, verified by export since there is no shell) and §4.5 (the build does not use the workspace).

Acceptance: every check run and recorded, every control confirmed to fail the way it should, and `git diff --stat` showing no `.go` and no `.ts`/`.tsx`.

---

## Checkpoint A — After T2

The stack is up but has no frontend. Prove the backend half through the published gateway with curl: register, login, price, order, insights. If this works, everything left is packaging a static bundle.

## Checkpoint B — After T5

Full journey in the browser, then the dev loop back: `docker compose down`, `make docker-up`, `make run-*`, and confirm the host stack is exactly as it was.

---

## Not in this step

Cloud deployment, a registry, image tags, CI, `allowedOrigin` as configuration, and secrets outside `.env`. SPEC §5. The Redis client consolidation is still open and is not made better or worse by this.

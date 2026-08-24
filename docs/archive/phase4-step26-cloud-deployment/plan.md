# Plan — Cloud deployment (Step 26)

Branch `step26-cloud-deployment`, to be cut from `main` at `17116c9`.

The step splits at provisioning. T1-T5 are local, verifiable today, and are most of the work. T6 needs an AWS account that does not exist yet, and the plan says so rather than pretending otherwise.

Unlike Step 25 this step **does** change source, in two small places. That is the thing to watch: the temptation is to keep going.

## T1 — One origin

nginx proxies the five API prefixes to the gateway. `BASE_URL` defaults to `''`. Vite's dev server proxies the same prefixes.

Acceptance: every browser request goes to 5173 and none to 8080, locally, with the gateway unpublished. The dev loop works too.

## T2 — The bundle names no host

Drop the `env -u` branch, which §2.2 makes unnecessary.

Acceptance: grep the built assets for any host and find none. Run the same grep against Step 25's image as the control, because a grep that cannot fail proves nothing.

## T3 — `CORS_ALLOWED_ORIGIN`

The constant becomes configuration, with the current value as the default.

Acceptance: the default keeps `make run-*` working unchanged; a changed value changes what the preflight matches. Checked directly, since §2.2 stops the browser from exercising CORS.

## T4 — The healthcheck binary

A new tiny module, built once, copied into all six images.

Acceptance: `docker compose ps` shows healthy; a service pointed at a dead port shows unhealthy rather than up.

## T5 — The production overlay and the runbook

`docker-compose.prod.yml`, the Caddyfile, `deploy.sh`, `fetch-secrets.sh`, `docs/DEPLOYMENT.md`.

Acceptance: the overlay runs locally end to end, so the only thing untested on the instance is the certificate.

## T6 — Provision — BLOCKED

Needs an AWS account. §5 is the runbook. Khalil runs `aws configure` himself; I never handle the keys.

---

## Checkpoint A — After T3

Full journey through one origin, locally, plus the dev loop. If this works the deployment shape is proven and what is left is packaging and a machine.

## Checkpoint B — After T5

The prod overlay locally, then everything Step 25 verified re-run, since T1 moved how every request is routed.

---

## Not in this step

CI/CD, Terraform, RDS, a registry, autoscaling, a load balancer, and any fix for a hung service beyond noticing one. SPEC §1 and §6.

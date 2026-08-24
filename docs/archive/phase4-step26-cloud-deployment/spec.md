# SPEC — Cloud deployment (Step 26)

Status: **Built and verified locally, apart from provisioning, which is blocked on an AWS account.** §4.10 carries the results.

§2.4 and §2.10 were rewritten after building started. Putting a proxy in front of the gateway collapses its per-IP rate limiter into a single global bucket, and the two-proxy topology the first draft described made that worse rather than better. Both are recorded as they now stand.

There is no AWS account yet, and creating one is not something I can do. So this step is split at that line: **everything up to provisioning is built and verified locally against a production-shaped configuration**, and §5 is a runbook that runs the moment the account exists. Nothing is left half-built on either side of the line.

Scope: `services/gateway` and `frontend/` each gain one small change, `infra/docker/` gains a healthcheck binary and a TLS front door, and a production compose overlay appears. **This step does change source**, unlike Step 25, and §2.2 and §2.5 are why.

Prior specs archived at `docs/archive/phase1-step4-auth/` through `docs/archive/phase4-step25-dockerization/`.

---

## 1. Objective

Step 25 made the stack deployable and named three things standing between it and a public IP: `allowedOrigin` is a compile-time constant, `VITE_API_BASE_URL` is baked into the bundle at build time, and secrets come from a `.env` on disk. A fourth turned up on the way: nothing notices a hung service.

**Objective:** QuantSim runs at `https://quantsim.khalilpeguero.me`, on one EC2 instance, over real certificates, with the same images that run locally.

**Non-goals:**

- **CI/CD.** Deploy is a script somebody runs. `agents.md` lists pipelines as an optional enhancement and they want a working deploy to automate first.
- **Terraform.** Same list, same reason. The first instance is worth creating by hand once, so the runbook records what actually had to exist.
- **RDS.** Decided against in §2.8. Postgres stays a container.
- **A registry.** §2.9.
- **Autoscaling, multi-AZ, load balancers.** One instance. An ALB alone would cost more per month than everything else here combined.
- **Rewriting the CORS middleware.** §2.5 makes its origin configuration. The middleware itself is Step 7 and Step 11 work and is not touched.

---

## 2. Design decisions

### 2.1 One origin, which deletes two of the three blockers rather than solving them

The frontend and the API get served from the same origin. nginx serves the bundle at `/` and reverse-proxies `/auth`, `/market-data`, `/trading`, `/backtests` and `/insights` to the gateway.

Both remaining blockers are consequences of two origins, not problems in their own right. With one origin there is no cross-origin request to allow, so `allowedOrigin` stops being load-bearing; and the frontend has no host to name, so there is nothing to bake. Fixing them individually would have meant an env-driven CORS origin *and* a runtime config file the app fetches at startup, which is two mechanisms to maintain in place of one that is simply not needed.

It is also less to expose. The gateway stops being published at all: locally the browser talks only to 5173, in production only to 443.

The cost is that nginx now has a routing table that has to match the gateway's, and a new route added to the gateway needs a line here too. That is real, and it is why §3 keeps the prefix list in exactly one file rather than one per environment.

### 2.2 The frontend stops naming a host at all

`BASE_URL` becomes `''` by default, so every request is relative, and the dev server proxies the API prefixes to `localhost:8080` through Vite's `server.proxy`.

This is the honest version of "make the base URL configurable". The alternative that looks easier — keep `VITE_API_BASE_URL` and set it per environment — leaves the value compiled into the bundle, so the image and the environment stay welded together and every host change is a rebuild. A relative URL has no environment in it to get wrong.

Two details this must not get wrong:

- **`??` does not fall back on an empty string.** `import.meta.env.VITE_API_BASE_URL ?? 'http://localhost:8080'` returns `''` when the variable is set to empty, which is why Step 25's Dockerfile has an `env -u` branch. Inverting the default makes that branch unnecessary and it goes.
- **Vite's proxy is server-side**, so dev requests are same-origin too. CORS stops being exercised in development, which is a good thing except that it also stops being *tested* by hand. §4 keeps a direct cross-origin check for exactly that reason.

The env var stays as an escape hatch for pointing a local frontend at a remote API. It is no longer how anything is deployed.

### 2.3 The proxy runs locally too, so local and production differ only by TLS

The temptation is to add the proxy only in the production overlay and leave local development on two origins. That would mean the routing everything now depends on is exercised for the first time on the instance.

So the same image proxies in both. The production overlay adds TLS and a domain and changes nothing else about how a request is routed. If `/insights/portfolio` works locally it works deployed, because it is the same server with the same prefixes talking to the same gateway.

### 2.4 Caddy replaces nginx entirely, in both environments, and there is exactly one proxy hop

The first draft had nginx serving the bundle and proxying the API, with Caddy in front of it for TLS in production. That is two proxies in production and one locally, which is wrong twice over: the environments stop matching, and §2.10's client-IP problem gets a second hop to walk.

So Caddy is the only web server. It serves the static bundle and reverse-proxies the API prefixes, and it is the same container and the same Caddyfile in both places. The site address is the only difference: `:5173` locally, `${DOMAIN}` in production, and Caddy issues certificates automatically for the second and not the first, which is exactly the behaviour wanted.

That leaves **one** proxy between the browser and the gateway, in both environments. §2.10 depends on that being true and on it staying true.

Caddy also redirects 80 to 443 and sets HSTS, which is the part people forget and is what stops the first request of the day from being the plaintext one. certbot with nginx is the same outcome with a cron job and a renewal hook to get wrong.

**The domain is `quantsim.khalilpeguero.me`.** `khalilpeguero.me` already resolves to GitHub Pages, and a subdomain is a separate A record that leaves the apex alone. Pages keeps serving what it serves.

**Plain HTTP is not an option here and the reason is specific.** This application has real registration and login, and Step 13 put refresh tokens in it. Over HTTP on a public IP, every password and every JWT is readable by anything on the path. A portfolio project that ships a login form over plaintext is demonstrating the wrong thing.

Caddy also redirects 80 to 443 and gets the HSTS header, which is the part people forget and which is what stops the first request of the day from being the plaintext one.

### 2.5 The CORS origin becomes configuration, and stays enforced

§2.1 means nothing crosses origins in normal operation. The constant still has to go, for two reasons that survive that: a deployed service whose code names `http://localhost:5173` is wrong in a way that will confuse somebody, and the middleware is the thing that would *stop* a real cross-origin request, so it needs to describe the real origin.

`CORS_ALLOWED_ORIGIN`, defaulting to `http://localhost:5173` so the `make run-*` loop keeps working with no new variable. The comment on the constant said a CORS origin was not a knob worth exposing "before something needs to turn it". Something needs to turn it.

### 2.6 A healthcheck binary, not a flag in six mains

Distroless has no shell, so there is nothing for a Docker `HEALTHCHECK` to run. Step 25 recorded that and left it.

The obvious fix is a `-healthcheck` flag in each service's `main.go`: six edits to six files that must stay identical, in six packages that otherwise have nothing to do with each other. Instead `infra/docker/healthcheck/` is a ~30-line module that GETs `http://127.0.0.1:$PORT/healthz` and exits 0 or 1. The builder stage compiles it once and copies it into every image.

**What this buys is visibility, not recovery.** Docker does not restart an unhealthy container -- only Swarm and Kubernetes do -- so `restart: unless-stopped` still covers a crash and nothing covers a hang. What changes is that `docker compose ps` stops lying about it. Recovery is out of scope and §6 says so rather than pretending a healthcheck is one.

### 2.7 Secrets come from SSM Parameter Store, with a documented interim

Standard SSM parameters are free, SecureString uses a KMS key that is free by default, and a small script pulls them into a root-owned `.env` at deploy time. Secrets Manager is the same thing at $0.40 per secret per month.

Until the account exists there is nothing to write them to, so the interim is a `.env` on the instance at mode 600 owned by root, which is also the fallback if SSM is ever unreachable at boot. The distinction worth keeping: the file is a **cache** of the parameters, not the source of truth, and the deploy script rewrites it every time.

`ANTHROPIC_API_KEY` deserves a line of its own. It is the only secret here that costs money when leaked rather than merely granting access, and a public instance makes the narrative endpoint reachable by anyone who registers. Step 21's daily generation cap is what stands between that and a bill.

### 2.8 Postgres stays a container, and backups are a documented dump

`agents.md` names RDS as optional and EC2 + docker-compose as the target. RDS is free for twelve months on a new account and roughly $12 a month afterwards, which is a bill that arrives long after anyone has stopped thinking about this.

The honest trade: a container on the instance means the data lives on one EBS volume with no managed backups and no point-in-time recovery, and losing the instance loses the data. For simulated trading data that is recoverable by re-running the seed, that is acceptable. It would not be for anything else, and §6 says so.

A `pg_dump` to a second location is in the runbook rather than automated, because an automated backup nobody has restored from is not a backup.

### 2.9 Images move by `docker save`, not through a registry

The first deploy needs no accounts, no tokens and no registry: `docker save | ssh | docker load`.

Building on the instance was ruled out first. A t3.micro has 1GB of RAM and the Go toolchain compiling six services will meet it. Pushing to ghcr.io was the alternative -- free for public repositories, and this repository is public -- but it needs a personal access token, which is a credential to create and store for a step that has one instance and no CI.

`save`/`load` is slower and entirely manual, and it is the right amount of machinery for one box. §6 records ghcr as the upgrade the moment CI exists, since that is when the tradeoff flips.

**A t3.micro will be tight.** Nine containers against 1GB, and Postgres alone wants a couple of hundred megabytes. The runbook allocates swap before anything else, because the failure mode without it is the OOM killer taking whichever container it likes during the first deploy.

### 2.10 A proxy in front of the gateway breaks its rate limiter, and that has to be fixed here

This is the part that would have shipped.

`clientIP` reads `r.RemoteAddr` and deliberately nothing else. The comment on it, and `docs/security-backlog.md`, both explain why at length: `X-Forwarded-For` is client-authored at that hop, so a limiter keying on it hands a fresh budget to anyone who sets a header. There is a test that fails if it ever changes.

Put Caddy in front and `RemoteAddr` becomes Caddy's container address on **every** request. The per-IP limiter does not stop working -- it works perfectly, on one key, for everybody. 100 requests per 15 minutes shared across the entire internet. On a public instance that is a self-inflicted denial of service, arriving as "login is broken for everyone" rather than as anything resembling a rate limit.

The fix is the standard one and its safety is entirely in the precondition. `TRUSTED_PROXIES`, a list of CIDRs, **empty by default**. When it is empty nothing changes at all, which is why every existing test still passes unmodified. When `RemoteAddr` falls inside it, the client address is the **rightmost** entry of `X-Forwarded-For`.

Rightmost, not leftmost, and that is the whole security argument. A client sending `X-Forwarded-For: 1.2.3.4` gets its real address appended by Caddy, producing `1.2.3.4, <real>`. The rightmost entry is the one the trusted hop wrote and the only one that is not client-authored. Taking the leftmost would reintroduce exactly the bypass the original comment describes.

**This is correct for one trusted hop and no more.** With two, the rightmost entry is the inner proxy and the limiter keys on it. §2.4 is what guarantees one, which is why these two decisions have to be read together.

An untrusted peer sending `X-Forwarded-For` is still ignored, unconditionally. The old behaviour is not weakened; it is made conditional on a peer the operator has named.

---

## 3. The change

`services/gateway/cmd/server/main.go`

- `allowedOrigin` const becomes `envOrDefault("CORS_ALLOWED_ORIGIN", "http://localhost:5173")`, per §2.5.

`frontend/src/api/client.ts`

- `BASE_URL` defaults to `''` rather than `http://localhost:8080`, per §2.2.

`frontend/vite.config.ts`

- `server.proxy` for the five API prefixes to `http://localhost:8080`.

`infra/docker/nginx.conf`

- `location` blocks proxying the five prefixes to the gateway, with the prefix list in one place.

`infra/docker/healthcheck/` (new module)

- `main.go`, ~30 lines. Added to `go.work` and to the Makefile's `GO_MODULES`.

`infra/docker/Dockerfile.service`

- Builds and copies the healthcheck binary; adds `HEALTHCHECK`.

`infra/docker/Dockerfile.frontend`

- Drops the `env -u` branch, which §2.2 makes unnecessary.

`infra/docker/Caddyfile` (new)

- The site block for `${DOMAIN}`, reverse-proxying the frontend.

`docker-compose.prod.yml` (new)

- Overlay adding `caddy`, publishing 80 and 443 on `0.0.0.0` rather than loopback, and unpublishing the gateway.

`infra/deploy/` (new)

- `deploy.sh` (save, ship, load, restart) and `fetch-secrets.sh` (SSM to `.env`).

`docs/DEPLOYMENT.md` (new)

- The runbook in §5, in full, including what to do when a certificate does not issue.

---

## 4. Verification

Local first, and the production-shaped parts are verifiable without AWS.

1. **The single origin works locally.** Every API call from the browser goes to `localhost:5173` and returns 200, with no request to 8080 at all. This is the whole of §2.1 and §2.3 and it is checkable today.
2. **The bundle names no host.** grep the built assets for `localhost:8080` and for the deployed domain; both must be absent. §2.2's actual claim.
3. **Control: the old bundle did name one.** Step 25's image has `http://localhost:8080` compiled in, which is what makes check 2 meaningful.
4. **The dev loop still works.** `make run-frontend` against `make run-gateway`, through Vite's proxy.
5. **CORS is still enforced**, checked directly rather than through the browser, since §2.2 stops exercising it: a request with a foreign `Origin` must not be echoed, and the configured origin must be.
6. **`CORS_ALLOWED_ORIGIN` is load-bearing.** Set it to something else and confirm the preflight stops matching.
7. **Healthchecks report.** `docker compose ps` shows every service healthy; a service pointed at a dead port shows unhealthy rather than up.
8. **The prod overlay is valid and complete**, checked with `docker compose -f docker-compose.yml -f docker-compose.prod.yml config` and by running it locally against a self-signed internal name, so the only untested thing on the instance is the certificate.
9. **Everything Step 25 verified still holds**: journey, ports, `.env` absent from images, migrations gating, `make docker-up` at two containers, `vet`/`test`/`test-integration`.

The narrative endpoint stays untouched. It is the only billed path.

### 4.10 What the runs showed

| # | Check | Result |
|---|---|---|
| 1 | Journey through **one origin** | register 201, login 200, `/me` 200, price 200, order **filled** 201, positions 200, insights **200**, backtests 200 -- all via 5173, with nothing published on 8080 |
| 2 | Bundle names no host | **0** hits for `localhost:8080`. **Control:** Step 25's bundle scores 1 |
| 3 | Rate limiting behind the proxy | one client spends its budget → 429; a **different** client on the network → 401, its own budget |
| 3c | **Control**, `TRUSTED_PROXIES` empty | that different client → **429 having made no requests at all**. The defect §2.10 describes, reproduced |
| 4 | Dev loop | `npm run dev` serves the page and proxies `/auth/login` and `/backtests` to the gateway |
| 5 | CORS still discriminates | configured origin echoed, `https://evil.example.com` not |
| 6 | `CORS_ALLOWED_ORIGIN` load-bearing | setting it to the real domain stops the old default matching |
| 7 | Healthchecks | six services healthy; the binary exits 1 on a dead port, 1 on empty `PORT`, 0 in situ |
| 8 | Prod overlay | merged config publishes exactly 80 and 443; Postgres and Redis loopback; gateway nothing; `/data` a volume; no stray 5173 |
| 9 | Step 25's checks | `.env` absent from images, migrations gate the stack, `make docker-up` at 2 containers, `vet`/`test` clean, `test-integration` **63/0**, `GOWORK=off` **8/8** |
| -- | Mutation testing on `clientip.go` | **10 run, 10 killed**; an 11th was equivalent and the code it covered was deleted |

Two of those mutants survived the first pass and both were real gaps. A test asserting the mapped-IPv6 case was passing **for the wrong reason**: it built `RemoteAddr` by concatenation, so an unbracketed IPv6 address failed `SplitHostPort`, the port stayed in the key, and every request got its own budget. And the no-usable-header fallback could not be distinguished from keying on the empty string with only one proxy in the test.

---

## 5. The provisioning runbook

Blocked until an AWS account exists. Written now so the session that has one is short.

1. **Account and IAM.** An account, then an IAM user with programmatic access rather than root keys. `aws configure` is run by Khalil; I never handle the keys.
2. **Key pair.** `~/.ssh/id_ed25519.pub` already exists and gets imported rather than generating a new one.
3. **Instance.** t3.micro, Amazon Linux 2023, 30GB gp3 (the free tier ceiling). **Swap first**, per §2.9.
4. **Security group.** 22 from Khalil's IP only, 80 and 443 from anywhere. Nothing else. Not 5432, not 6379, not 8080.
5. **Elastic IP**, so the DNS record survives a stop/start. Free while attached, charged while not.
6. **DNS.** An A record for `quantsim` at whatever hosts `khalilpeguero.me`'s DNS, pointing at the Elastic IP. The apex and its GitHub Pages records are not touched.
7. **Docker** on the instance, then `infra/deploy/deploy.sh`.
8. **Secrets** into SSM, then `fetch-secrets.sh`.
9. **Certificate.** Caddy issues on first request. If it fails, the answer is almost always DNS not having propagated or port 80 being closed.
10. **Verify** the same journey §4 runs locally, against the real domain, plus the certificate chain.

---

## 6. Open

**Nothing restarts a hung service.** §2.6 gives visibility and not recovery. A watchdog container or a systemd timer is the fix, and it wants to be chosen after something has actually hung.

**Losing the instance loses the database.** §2.8. `pg_dump` is in the runbook and is manual. An automated backup nobody has restored from is not a backup, so the first restore should be a deliberate exercise.

**Deploy is a script, not a pipeline.** CI/CD is where ghcr.io becomes worth its credential, per §2.9.

**Terraform.** The runbook in §5 is the specification for it, which is the right order: nobody should write Terraform for infrastructure they have not yet created by hand once.

**Still open from Step 24:** `auth` and `market-data` build Redis clients without `ContextTimeoutEnabled`. Untouched by Steps 25 and 26.

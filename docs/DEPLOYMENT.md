# Deploying QuantSim

One EC2 instance running the same containers that run on a laptop, behind Caddy
on `https://quantsim.khalilpeguero.me`.

**Status: the code is ready and nothing has been provisioned.** Everything in
§Local rehearsal is verified; everything from §1 onward needs an AWS account
that does not exist yet. Step 26 stops at that line on purpose rather than
half-building past it.

---

## What runs where

```
browser ──HTTPS──> Caddy (frontend container)
                     ├── /            static bundle
                     └── /auth /market-data /trading /backtests /insights
                                      └──> gateway ──> auth, market-data,
                                                       trading-engine,
                                                       backtesting, ai-insights
                                                          └──> Postgres, Redis
```

**One origin, one proxy hop.** Both matter:

- One origin is why the bundle contains no API host and why nothing crosses
  origins, so there is no per-environment frontend build and no CORS to
  configure.
- One hop is why the gateway's rate limiter can trust `X-Forwarded-For`. It
  reads the **rightmost** entry, which is the only one a client cannot forge,
  and that is correct for exactly one trusted proxy. **Do not put another
  proxy in front of Caddy** -- an ALB, Cloudflare, anything -- without
  revisiting `services/gateway/internal/middleware/clientip.go`. The failure
  is silent: the limiter starts keying on the inner proxy and every client
  shares one budget.

Only 80 and 443 are published. Postgres and Redis bind loopback on the
instance; the gateway and the five services publish nothing at all.

---

## Local rehearsal

Everything except the certificate can be checked before touching AWS.

```bash
make stack-up
curl -s -o /dev/null -w '%{http_code}\n' http://localhost:5173/          # 200
curl -s -o /dev/null -w '%{http_code}\n' http://localhost:5173/healthz   # 404, no SPA fallback
docker compose --profile app ps                                          # all healthy
```

The production merge is checkable too, without running it:

```bash
DOMAIN=quantsim.khalilpeguero.me docker compose \
  -f docker-compose.yml -f docker-compose.prod.yml --profile app config
```

---

## 1. Account and credentials

Create the AWS account, then an **IAM user with programmatic access** -- not
root keys. Attach only what this needs: EC2, and `ssm:GetParametersByPath` on
`/quantsim/*`.

```bash
aws configure   # you type the keys; they never go anywhere else
```

## 2. Instance

- **t3.micro**, Amazon Linux 2023, **30GB gp3** (the free-tier ceiling).
- Import `~/.ssh/id_ed25519.pub` as the key pair rather than generating a new one.

**Allocate swap before anything else.** Nine containers against 1GB is tight,
and without swap the first deploy is decided by the OOM killer:

```bash
sudo dd if=/dev/zero of=/swapfile bs=1M count=2048
sudo chmod 600 /swapfile && sudo mkswap /swapfile && sudo swapon /swapfile
echo '/swapfile none swap sw 0 0' | sudo tee -a /etc/fstab
```

Then Docker:

```bash
sudo dnf install -y docker && sudo systemctl enable --now docker
sudo usermod -aG docker ec2-user   # log out and back in
```

## 3. Security group

| Port | Source |
|---|---|
| 22 | your IP only |
| 80 | 0.0.0.0/0 |
| 443 | 0.0.0.0/0 |

Nothing else. Not 5432, not 6379, not 8080. Port 80 is not optional: Caddy's
ACME HTTP-01 challenge uses it, and closing it is the most common reason a
certificate never issues.

## 4. Elastic IP

Allocate and associate one, so the DNS record survives a stop/start. Free while
attached to a running instance, **charged while it is not** -- releasing it is
part of tearing this down.

## 5. DNS

Wherever `khalilpeguero.me`'s DNS lives, add:

```
quantsim   A   <elastic ip>
```

The apex and its GitHub Pages records are untouched; `khalilpeguero.me` keeps
serving Pages. Confirm before deploying, because Caddy will fail to issue a
certificate against a name that does not resolve yet:

```bash
dig +short quantsim.khalilpeguero.me
```

## 6. Secrets

Put them in SSM under `/quantsim/`, as `SecureString` for anything sensitive:

```bash
aws ssm put-parameter --name /quantsim/JWT_SECRET      --type SecureString --value '...'
aws ssm put-parameter --name /quantsim/POSTGRES_PASSWORD --type SecureString --value '...'
aws ssm put-parameter --name /quantsim/ALPACA_API_KEY  --type SecureString --value '...'
aws ssm put-parameter --name /quantsim/ALPACA_API_SECRET --type SecureString --value '...'
aws ssm put-parameter --name /quantsim/ANTHROPIC_API_KEY --type SecureString --value '...'
aws ssm put-parameter --name /quantsim/POSTGRES_USER   --type String --value 'quantsim'
aws ssm put-parameter --name /quantsim/POSTGRES_DB     --type String --value 'quantsim'
aws ssm put-parameter --name /quantsim/POSTGRES_APP_DB --type String --value 'postgres'
aws ssm put-parameter --name /quantsim/DOMAIN          --type String --value 'quantsim.khalilpeguero.me'
```

Give the instance a role with `ssm:GetParametersByPath` on `/quantsim/*`, so
no AWS keys live on the box, then:

```bash
sudo /opt/quantsim/infra/deploy/fetch-secrets.sh
```

**`ANTHROPIC_API_KEY` deserves a moment.** It is the only secret here that
costs money when leaked rather than merely granting access, and a public
instance makes the narrative endpoint reachable by anyone who registers. Step
21's per-user daily generation cap is what stands between that and a bill.
Leave it out of SSM entirely if you would rather deploy without the narrative:
the report endpoint is unaffected and the narrative endpoint returns 200 with
`narrative: null`.

## 7. Deploy

From your laptop:

```bash
infra/deploy/deploy.sh ec2-user@quantsim.khalilpeguero.me
```

Builds locally, ships the images over ssh, and starts the stack with the
production overlay. The image load is the slow part.

## 8. Verify

```bash
curl -sI https://quantsim.khalilpeguero.me/ | head -1        # HTTP/2 200
curl -sI http://quantsim.khalilpeguero.me/ | head -1         # 308 to HTTPS
curl -s https://quantsim.khalilpeguero.me/healthz            # 404, no SPA fallback
```

Then the journey: register, login, a price, an order, the insights tab.

---

## When it goes wrong

**The certificate never issues.** Almost always DNS or port 80. Check
`dig +short` resolves to the Elastic IP, that the security group allows 80 from
anywhere, and `docker logs quantsim-frontend` for the ACME error. Let's Encrypt
rate-limits failures at five per account per hostname per hour, so fix the
cause before retrying -- and note the certificate store is a **volume** so
retries do not start from scratch.

**Everyone gets 429 at once.** The rate limiter is keying on the proxy. Check
the gateway's boot log: it should say `trusting X-Forwarded-For from
172.28.0.10/32 (one hop)`. If it says `TRUSTED_PROXIES is unset`, that is the
bug, and it is a shared bucket for the entire internet.

**A report says no historical data is available.** `POSTGRES_APP_DB` is
pointing at the wrong database. The migrate one-shot creates the schema
wherever it is aimed, so registration and orders both succeed and only a
report that needs pre-existing bars gives it away.

**The frontend reports unhealthy.** Its healthcheck hits `:5199/healthz`, an
internal listener that exists precisely so it does not move when
`SITE_ADDRESS` becomes a hostname.

**Containers die during the first deploy.** Swap, §2.

---

## What this deliberately does not have

- **Nothing restarts a hung service.** `restart: unless-stopped` covers a
  crash. The healthchecks report a hang and Docker does not act on them --
  only Swarm and Kubernetes do.
- **No automated backups.** `pg_dump` by hand:
  ```bash
  docker exec quantsim-postgres pg_dump -U "$POSTGRES_USER" postgres | gzip > quantsim-$(date +%F).sql.gz
  ```
  Losing the instance loses the database. Restore it once deliberately, before
  you need to.
- **No CI/CD.** Deploy is this script.
- **One instance, one AZ, no load balancer.** An ALB alone costs more per month
  than everything here combined.

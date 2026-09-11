# Event Nu Backend

Go backend for Event Nu: auth, email verification, organizer applications,
venues, categories, and events. Transactional email runs on the Brevo free tier.

## Repo layout

```text
cmd/api        HTTP API (auth, email, org, events)
cmd/worker     Background worker (email outbox consumer)
internal/
  api          HTTP handlers, routes, application wiring
  config       Environment-driven configuration (fails fast)
  domain       Domain types
  infrastructure/email   Brevo v3 client + sender interface + noop
  repository   Postgres repositories (outbox, email codes, users, ...)
  service      Business logic (email codes, outbox enqueues, verify)
  worker       Outbox poller (lease, retry/backoff, dead-letter)
migrations     SQL migrations (goose)
```

## Requirements

- Go **1.26.6** exactly — matches `go.mod`; other 1.26.x toolchains should work,
  but the pinned version is used in CI. Verify with `go version`.
- PostgreSQL (Neon) — `DATABASE_URL` under the `app_user` role (RLS-guarded)
- Brevo free account with transactional access enabled

## Running integration tests

`go test -race -p 1 ./...` (via `make test`) runs unit tests plus the
PostgreSQL-backed integration tests for auth, organizers, events, pagination,
idempotency, and the worker.

- Tests connect with `DATABASE_URL` (the non-superuser `app_user` role — a
  superuser would bypass RLS and the cross-user-block tests would fail).
- `-p 1` is required: packages share one database and must not run in parallel.
- When `DATABASE_URL` is not set (or unreachable), the DB-backed tests `t.Skip`
  and the suite still passes — a skipped test is not the same as a green DB test,
  so CI always runs against a real Postgres service (see the CI workflow).

Local workflow (Docker required) — boots the same Postgres 16 CI uses, creates
the non-superuser `app_user` role, applies migrations, then runs the full gate:

```sh
make db-up
make ci
```

- `make db-up` is safe to re-run; `make db-down && make db-up` starts from a
  clean database to match CI exactly.
- The Makefile defaults `DATABASE_URL`, `DATABASE_ADMIN_URL`, `JWT_SECRET`, and
  `EMAIL_PROVIDER` to CI-equivalent values; any already-exported value wins.
- Without Docker, export the same four variables against your own Postgres
  (following the workflow below) and run `make ci` directly.

## CI quality gates (phase-01)

`.github/workflows/ci.yml` enforces the same gates as local development —
`gofmt -l`, `go vet`, `go build`, `go test -race -p 1 -count=1 ./...` — against
a **Postgres 16 service** so the integration/RLS tests actually run (they
`t.Skip` when `DATABASE_URL` is unset or unreachable, which is fine locally but
would make CI useless).

How CI provisions the database:

1. Boots `postgres:16` with `event_nu_test` as the database.
2. As the `postgres` superuser, creates the **`app_user` login role** and grants
   `CONNECT` on the database + `USAGE` on `public`. It also sets
   `ALTER DEFAULT PRIVILEGES` so every table/sequence the migrations create is
   automatically granted to `app_user`.
3. Runs the goose migrations with `DATABASE_ADMIN_URL` (superuser). Migration
   `00007` `GRANT`s organizer/venue/event tables to `app_user` directly.

Why `app_user` must be non-superuser: RLS is bypassed for superusers, so the
cross-user-block proof tests (`internal/repository/user_rls_test.go`,
`internal/infrastructure/database/rls_test.go`) would silently pass for the
wrong reason. The full auth/RLS matrix only means something when every test runs
as `app_user`. The worker's trusted paths run as `app_user` with the `service`
RLS role preset — no extra role membership is needed.

Run the same gates locally against your own Postgres (Docker works):

```sh
docker run --name eventnu-ci-pg -e POSTGRES_PASSWORD=postgres \
  -e POSTGRES_DB=event_nu_test -p 5433:5432 -d postgres:16

export DATABASE_ADMIN_URL="postgres://postgres:postgres@localhost:5433/event_nu_test?sslmode=disable"
export DATABASE_URL="postgres://app_user:app_user_pass@localhost:5433/event_nu_test?sslmode=disable"
psql "$DATABASE_ADMIN_URL" <<'SQL'
CREATE ROLE app_user LOGIN PASSWORD 'app_user_pass';
GRANT CONNECT ON DATABASE event_nu_test TO app_user;
GRANT USAGE ON SCHEMA public TO app_user;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO app_user;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO app_user;
SQL
make migrate-up
make ci
```

## Configuration

`.env` lives at the repo root (`/home/daggi/Projects/event_nu_mobile/.env`),
above the `backend/` git root. See `.env.example` for the template.

| Variable | Purpose |
|---|---|
| `APP_ENV`, `PORT` | Runtime environment and HTTP port |
| `DATABASE_URL` | API/worker connection (transactions set RLS role; worker presets `service`) |
| `DATABASE_ADMIN_URL` | Migration/owner connection |
| `JWT_SECRET` | HMAC secret; `JWT_EXPIRY` (default `15m`), `REFRESH_TOKEN_EXPIRY` (default `30d`) |
| `TICKET_QR_SECRET` | HMAC key for ticket QR payloads (defaults to `JWT_SECRET`); see `docs/ticket-qr-protocol.md` |
| `PAYMENT_PROVIDER` | `noop` (default) or `chapa`; see `docs/chapa.md` |
| `CHAPA_SECRET_KEY` | Chapa API key (initialize/verify auth); required when `PAYMENT_PROVIDER=chapa` |
| `CHAPA_API_BASE` | Chapa API root (default `https://api.chapa.co/v1`) |
| `CHAPA_WEBHOOK_SECRET` | Webhook HMAC key (defaults to `CHAPA_SECRET_KEY`) |
| `CHAPA_REDIRECT_BASE` | Checkout `return_url` base (defaults to `API_PUBLIC_BASE`) |
| `EMAIL_PROVIDER` | `brevo` or `noop` (default `noop`) |
| `BREVO_API_KEY` | v3 API key (`xkeysib-...`) |
| `BREVO_API_BASE` | Defaults to `https://api.brevo.com` |
| `BREVO_API_SENDER` | Verified technical From address |
| `BREVO_SENDER_EMAIL` | User-facing reply-to address |
| `BREVO_SENDER_NAME` | Display name (default `Event Nu`) |
| `BREVO_TEMPLATE_WELCOME`, `BREVO_TEMPLATE_VERIFY` | Transactional template IDs (3 and 4) |
| `API_PUBLIC_BASE` | Base URL used to build the verify link (default `http://localhost:8080`) |
| `AUTH_LOGIN_RATE_LIMIT`, `AUTH_LOGIN_RATE_WINDOW` | Login attempts per IP/account (default `20`/`5m`) |
| `AUTH_REGISTER_RATE_LIMIT`, `AUTH_REGISTER_RATE_WINDOW` | Register per IP/account (default `5`/`1h`) |
| `AUTH_REFRESH_RATE_LIMIT`, `AUTH_REFRESH_RATE_WINDOW` | Refresh per IP (default `30`/`5m`) |
| `AUTH_VERIFY_RATE_LIMIT`, `AUTH_VERIFY_RATE_WINDOW` | Verify per IP (default `20`/`1h`) |
| `CORS_ALLOWED_ORIGINS` | Comma-separated origin allowlist (default empty = same-origin) |
| `EMAIL_POLL_INTERVAL`, `EMAIL_BATCH_SIZE`, `EMAIL_MAX_ATTEMPTS`, `EMAIL_RETRY_BASE_DELAY` | Outbox worker tuning |

`EMAIL_PROVIDER=brevo` requires `BREVO_API_KEY`, `BREVO_API_SENDER`, and both
template IDs (`> 0`); startup aborts otherwise instead of silently skipping
sends.

### Auth rate limiting

Login/register/refresh/verify are rate-limited in-process with a fixed-window
limiter (`internal/api/middleware/ratelimit.go`). The API is a **single
instance**, so in-memory state is intentional — no Redis until the topology
changes. IP is keyed from `X-Forwarded-For` (first value) else `RemoteAddr`;
login and register additionally key the normalized body email (lower-cased), so
distributed credential stuffing still trips a per-account 429. Every 429 is a
generic `too_many_requests` with a `Retry-After` header and reveals nothing
about account existence.

## Run

```sh
make migrate-up     # apply migrations (uses DATABASE_ADMIN_URL)
make run            # API on PORT
make worker         # email outbox consumer (separate process)
make test           # go test -race -p 1 ./... (serial: shared dev DB)
make vet
```

Integration tests that need a database skip when `DATABASE_URL` isn't set.

## Transactional email flow

1. `POST /api/v1/auth/register` completes; the handler enqueues a welcome
   (`BrevoTemplateWelcome`) and a verification email (`BrevoTemplateVerify`)
   into `email_outbox`. Handlers never block on Brevo.
2. The worker claims rows (`FOR UPDATE SKIP LOCKED` + lease), sends via
   `POST /v3/smtp/email`, records results + `brevo_message_id` in
   `email_logs`, retries with backoff on transient failures (`429`/`5xx`), and
   dead-letters rows that exhaust `EMAIL_MAX_ATTEMPTS`.
3. Verification uses a 6-digit code: `SendVerification` stores its SHA-256
   hash in `email_codes` (TTL 24h, max 5 attempts) and emails a code-free deep
   link (`eventnu://verify`) with the code as a separate param. The code is
   never embedded in a URL, so it cannot leak through logs, referrers, or
   browser history. `POST /api/v1/auth/verify` (JSON body `{"code":"XXXXXX"}`)
   redeems it; the old `GET /api/v1/auth/verify?code=…` was removed (405).

Brevo templates receive params as `{{ params.<key> }}`:

- Welcome (3): `{{ params.name }}`, `{{ params.username }}`
- Verify (4): `{{ params.name }}`, `{{ params.verify_url }}` (`eventnu://verify`),
  `{{ params.verify_code }}` (6-digit code the app POSTs)

## Brevo setup

Sender model: the verified technical address (`BREVO_API_SENDER`) is the From,
branded with `BREVO_SENDER_NAME`; the user-facing address
(`BREVO_SENDER_EMAIL`) is the reply-to. Address masking via sender/reply-to is
compliant with Brevo terms.

Transactional sending requires the account relay to be **activated**. A fresh
free account can return `403 permission_denied` ("SMTP account is not yet
activated") even with validated senders and active templates — file a support
request to enable it.

### Adding an authenticated domain

If you want to send from your own domain (`no-reply@<domain>`):

1. Register the domain (e.g. Porkbun/Spaceship `.com`).
2. **Brevo → Senders → Domains → Add a domain**; Brevo returns records to add
   at the registrar: a code TXT (authentication), DKIM TXT, and SPF CNAMEs.
3. Wait 24–48h for propagation; Brevo marks the domain **Authenticated** and
   the SMTP relay unlocks automatically.
4. Set `BREVO_API_SENDER=no-reply@<domain>` in `.env` and `.env.example`.
   `BREVO_SENDER_EMAIL` (reply-to) can stay as `event.nua@gmail.com`.

## Deliverability caveats (free tier)

- Send cap of 300 emails/day.
- Shared IP pool; messages are signed via `brevosend.com`, and DKIM/SPF don't
  align across the shared domain (gmail publishes `p=none` DMARC, so
  deliverability still works in practice).
- Outbound messages display a "Sent with Brevo" footer.
- Machine egress here is HTTPS-only, so sending must use the REST API
  (`/v3/smtp/email`), not SMTP ports 587/465.
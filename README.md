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

Local workflow:

```sh
export DATABASE_URL="postgres://app_user:...@localhost:5432/event_nu?sslmode=require"
make test
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
| `EMAIL_PROVIDER` | `brevo` or `noop` (default `noop`) |
| `BREVO_API_KEY` | v3 API key (`xkeysib-...`) |
| `BREVO_API_BASE` | Defaults to `https://api.brevo.com` |
| `BREVO_API_SENDER` | Verified technical From address |
| `BREVO_SENDER_EMAIL` | User-facing reply-to address |
| `BREVO_SENDER_NAME` | Display name (default `Event Nu`) |
| `BREVO_TEMPLATE_WELCOME`, `BREVO_TEMPLATE_VERIFY` | Transactional template IDs (3 and 4) |
| `API_PUBLIC_BASE` | Base URL used to build the verify link (default `http://localhost:8080`) |
| `EMAIL_POLL_INTERVAL`, `EMAIL_BATCH_SIZE`, `EMAIL_MAX_ATTEMPTS`, `EMAIL_RETRY_BASE_DELAY` | Outbox worker tuning |

`EMAIL_PROVIDER=brevo` requires `BREVO_API_KEY`, `BREVO_API_SENDER`, and both
template IDs (`> 0`); startup aborts otherwise instead of silently skipping
sends.

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
   hash in `email_codes` (TTL 24h, max 5 attempts) and emails a clickable
   link; `GET /api/v1/auth/verify?code=XXXXXX` redeems it.

Brevo templates receive params as `{{ params.<key> }}`:

- Welcome (3): `{{ params.name }}`, `{{ params.username }}`
- Verify (4): `{{ params.name }}`, `{{ params.verify_url }}`

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
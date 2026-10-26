# Production runbook — Event Nu backend (single VM, Docker Compose)

Operational contract for the deploy defined by `deploy/docker-compose.prod.yml`.
Read once before the first deploy; §2 is the day-2 operations section.

Topology recap: nginx terminates TLS and proxies to the API over the Compose
network; Postgres is Neon (SaaS, outside this VM); Kafka runs single-node
KRaft on this VM for the outbox relay and push fanout; the worker is a second
container of the same image. Secrets live in Vault (KV) on this VM, volume-
backed, sealed until an operator unseals it; the unseal key and root token
must live OFF this VM (password manager / sealed ops note).

---

## 1. First deploy

0. Build a pinned image and get it to the VM: `docker build -t eventnu:<git-sha> .`,
   then `docker save` (+ `docker load` on the VM), or push the image to a
   registry. Never deploy `:latest` blind — pin the tag in the `IMAGE`
   variable in `deploy/.env` (see §2 "Update").

1. VM one-time setup (Debian/Ubuntu assumed):

   ```sh
   sudo apt-get update && sudo apt-get install -y docker.io docker-compose-v2 ufw
   sudo ufw allow 22, 80, 443/tcp; sudo ufw enable
   sudo swapoff -v /swapfile 2>/dev/null || sudo swapoff -a   # see §3 hard limit 5
   # DNS: A/AAAA record for the domain -> this VM's IP.
   ```

2. Clone the repo on the VM (migrations run from the checkout, §1 step 6):

   ```sh
   git clone https://github.com/daggix02-m/event_nu_backend.git /opt/event-nu
   cd /opt/event-nu/deploy
   ```

3. Environment file (non-secret config now; Vault credentials follow in
   step 4 after Vault exists):

   ```sh
   cp env.production.example .env
   sed -i "s/EVENTS.EXAMPLE.COM/<your-domain>/" nginx.conf
   chmod 600 .env                   # file perms ARE the secret boundary here
   ```

   Mandatory now: `API_PUBLIC_BASE=https://<your-domain>`,
   `CORS_ALLOWED_ORIGINS` (your app origin; leave non-app origins out).
   The secret values (`DATABASE_URL`, `JWT_SECRET`, provider keys, ... go
   into Vault in step 4; `.env` copies of them are only emergency
   overrides and stay commented out).

4. Vault bring-up (one-time; §2 "Vault" covers unseal/rotation afterwards):

   ```sh
   docker compose up -d vault
   # Shorthand: run the vault CLI inside the server container. -T (no tty)
   # lets the later policy heredoc pipe through; VAULT_TOKEN authenticates.
   vc() { docker compose exec -e VAULT_ADDR=http://127.0.0.1:8200 -T vault "$@"; }
   ```

   ```sh
   # 4a. Initialize (ONCE; a file-backed server starts uninitialized).
   vc vault operator init -key-shares=1 -key-threshold=1
   #    -> ONE unseal key + a root token. Store both OFF this VM
   #       immediately (password manager). There is no recovery without
   #       them: a sealed Vault you cannot unseal = the secrets are gone.

   # 4b. Unseal (also required after every VM reboot / vault recreate).
   vc vault operator unseal <unseal-key>

   # 4c. Provision with the root token (the CLI reads this env; drop the
   #     export after provisioning):
   export VAULT_TOKEN=<root-token-from-4a>

   # 4d. KV v2 at secret/ (a plain server ships with NO secret mount).
   vc vault secrets enable -path=secret -version=2 kv

   # 4e. Seed the record (real values; FCM_SERVICE_ACCOUNT is the whole
   #     JSON body — quote it). Rotate any of these the same way later.
   vc vault kv put secret/eventnu \
     DATABASE_URL="postgres://app_user:<pw>@<neon-host>/<db>?sslmode=require" \
     DATABASE_ADMIN_URL="postgres://<owner>:<pw>@<neon-host>/<db>?sslmode=require" \
     JWT_SECRET="$(openssl rand -hex 32)" \
     TICKET_QR_SECRET="$(openssl rand -hex 32)" \
     BREVO_API_KEY="<fill-when-email-enabled>" \
     CHAPA_SECRET_KEY="<fill-when-payments-enabled>" \
     CHAPA_WEBHOOK_SECRET="<fill-when-payments-enabled>" \
     MEDIA_S3_ACCESS_KEY="<fill-when-s3-enabled>" \
     MEDIA_S3_SECRET_KEY="<fill-when-s3-enabled>" \
     FCM_SERVICE_ACCOUNT="<fill-when-push-enabled>"

   # 4f. Least-privilege policy + AppRole login for the apps: the API and
   #     worker read exactly one record path, once per boot.
   vc vault policy write eventnu-app - <<'EOF'
   path "secret/data/eventnu" { capabilities = ["read"] }
   EOF
   vc vault auth enable approle
   vc vault write auth/approle/role/eventnu \
       token_policies="eventnu-app" secret_id_ttl=720h
   vc vault read  -field=role_id   auth/approle/role/eventnu/role-id    # -> .env VAULT_ROLE_ID
   vc vault write -f -field=secret_id auth/approle/role/eventnu/secret-id # -> .env VAULT_SECRET_ID
   ```

   Put the `VAULT_ROLE_ID` / `VAULT_SECRET_ID` values into `deploy/.env`
   (chmod 600 already applied; re-issue the secret id monthly — its TTL is
   720h — or wire `VAULT_SECRET_ID_FILE` with a compose secret mount).

5. Issue TLS once, nginx in webroot mode must serve the challenge before the
   cert exists. Do it bottom-up:

   ```sh
   # 5a. Start nginx alone in "cert bootstrapping" mode:
   docker compose up -d nginx
   # 5b. (Temporarily) switch off the :443 server block in nginx.conf or
   #     restage by replacing it — then:
   docker run --rm -v eventnu-prod_cert_data:/etc/letsencrypt \
     -v eventnu-prod_webroot:/var/www/certbot \
     certbot/certbot certonly --webroot -w /var/www/certbot \
     -d <your-domain> -m <admin-email> --agree-tos --redirect
   # 5c. Restore nginx.conf fully; `docker compose up -d nginx` reloads.
   ```

   (A simpler repeatable scripted version: generate a throw-away self-signed
   pair, keep nginx on TLS from the first second, reissue with certbot above,
   then delete the throwaway. Document it if you prefer that shape.)

6. Schema first, then app. `goose` is a raw binary and does not read Vault;
   inject the admin URL per run (it is in Vault, provide it via -e):

   ```sh
   ADMIN_URL="$(vc sh -c 'vault kv get -field=DATABASE_ADMIN_URL secret/eventnu')"
   docker compose --profile ops run --rm -e DATABASE_ADMIN_URL="$ADMIN_URL" migrate
   docker compose up -d                            # api + worker (+ vault, kafka)
   docker compose ps                               # api must be healthy
   curl -fsS https://<your-domain>/readyz          # {"status":"ready"}
   curl -fsS http://127.0.0.1:8080/healthz         # loopback smoke test
   ```

7. Runtime verification (failure to observe these means the deploy is not
   done):

   - `docker compose logs api | grep -m1 "vault secrets loaded"` — the boot
     line naming the record (no secret values ever appear, only counts).
   - `docker compose logs worker` — email consumer started; with Kafka: the
     relay loop and fanout consumer connect (broker hello lines).
   - `docker compose exec kafka bash -c 'echo > /dev/tcp/localhost/9092'`
     succeeds.
   - Log in through the app and confirm a real JWT flow.

---

## 2. Day-2 operations

### Update (code change)

```sh
cd /opt/event-nu && git pull && docker build -t eventnu:<new-sha> .
sed -i "s/^IMAGE=.*/IMAGE=eventnu:<new-sha>/" deploy/.env
# Migrations first (goose needs the admin URL injected; see §1 step 6):
vc() { docker compose exec -e VAULT_ADDR=http://127.0.0.1:8200 -T vault "$@"; }
ADMIN_URL="$(VAULT_TOKEN=<root-token> vc sh -c 'vault kv get -field=DATABASE_ADMIN_URL secret/eventnu')"
docker compose --profile ops run --rm -e DATABASE_ADMIN_URL="$ADMIN_URL" migrate
docker compose up -d                                    # replaces api & worker
docker compose ps && curl -fsS https://<your-domain>/readyz
```

### Rollback

Previous image tags are the rollback plan (no-DB-rollback steps below):

```sh
sed -i "s/^IMAGE=.*/IMAGE=eventnu:<previous-sha>/" deploy/.env
docker compose up -d
```

If the failure was a *migration*: `goose down` runs one step backward
(per-file Down sections; 25 files, each with one). Inspect first — a schema
Down on a table with live data is destructive by design:

```sh
docker compose --profile ops run --rm \
    -e DATABASE_ADMIN_URL="$ADMIN_URL" \
    migrate '/app/goose postgres "$DATABASE_ADMIN_URL" down'
```

Migration history: `'/app/goose postgres "$DATABASE_ADMIN_URL" status'`,
review before every Down ($ADMIN_URL as in the Update section).

### Vault

- **Unseal after every VM reboot or `vault` container recreate.** The api
  and worker fail fast (they boot, see Vault sealed, and exit — restart
  policy keeps retrying) until you run:

  ```sh
  vc() { docker compose exec -e VAULT_ADDR=http://127.0.0.1:8200 -T vault "$@"; }
  vc vault operator unseal <unseal-key>
  ```

  This is the price of keeping unseal keys off-VM: a rebooted VM serves
  nothing until an operator unseals. Physical VM restarts are rare; plan
  the unseal step into every reboot checklist.
- **Re-export the root token after a vault container recreate** (`docker
  compose up -d` replacing the vault service wipes its container
  filesystem) — `VAULT_TOKEN=<root-token>` before any provisioning command.
  Plain `docker compose restart vault` keeps everything.
- **Secret rotation** (e.g. JWT compromise, Brevo key leak): update the
  record and restart the consumers of it — values are read once per boot:

  ```sh
  vc vault kv put secret/eventnu JWT_SECRET="<new value>"
  docker compose up -d --force-recreate api worker
  ```

  JWT_SECRET rotation invalidates all sessions/logins (tokens fail
  validation) — rotate TICKET_QR_SECRET separately only with the QR
  semantics in mind; DB URL rotation means updating the Neon role password
  and the record in the same step.
- **AppRole credential rotation**: `secret_id_ttl=720h` (30 days). Re-issue
  before expiry or api/worker boots start failing:

  ```sh
  vc vault write -f -field=secret_id auth/approle/role/eventnu/secret-id
  # (rotate VAULT_SECRET_ID in deploy/.env, then docker compose up -d api worker)
  ```

- **If the unseal key / root token is lost**, there is no on-VM recovery.
  With the data volume still present but sealed: gone. Worst case is re-init
  (`docker compose down -v` on vault + redo §1 step 4): Vault data was the
  only copy of the secrets, so restoration means re-issuing every secret
  (Neon passwords, openssl rand, provider consoles). This is why §1 step 4a
  insists the key + token live off-VM from the first minute.

### Backups (Postgres / Neon)

Neon does point-in-time recovery by itself. **The restore drill is the part
that counts** — an untested backup is not a backup:

```sh
# Monthly drill (throwaway db, then diff some rows):
docker run --rm postgres:16 pg_dump "postgres://<admin-url>/<db>" > drill.sql
docker run --rm -i postgres:16 psql "postgres://<admin-url>/restore_drill" < drill.sql
```

Keep the last `drill.sql` + its verify output with the deploy notes.

### Certificates (renewal)

Pre-renewal hook (crontab on the VM):

```cron
0 3 * * 1 docker run --rm -v eventnu-prod_cert_data:/etc/letsencrypt \
    -v eventnu-prod_webroot:/var/www/certbot \
    certbot/certbot renew --webroot -w /var/www/certbot \
    --deploy-hook 'nginx -s reload'
```

Because nginx runs in a different container, reloading actually means:
`docker exec <nginx-container> nginx -s reload` — wrap that in the cron entry,
or restart the compose service. Watch output for the expiry date before
confirming each renewal.

### Logs and retention

Docker JSON logs are capped at 10 MB × 3 files per container (compose config,
`max-size`/`max-file`). The API logs in JSON with request IDs to stdout.

Known PII-in-logs: the worker logs each sent email by recipient address at
Info (`internal/worker/email.go`). Decide the retention window with your
privacy posture; 3×10 MB ≈ 30 MB is the in-memory default ("3 rotated files"),
rotate off-host if you need longer. Container stdout is the sole sink —
forward to your log store if required.

### Health & alerting

- Liveness: GET `/healthz` — never fails unless the process is dead.
- Readiness: GET `/readyz` — 503 when Postgres is unreachable.
- Metrics: GET `/metrics` — Prometheus text; watch `http_requests_total`,
  dependency gauges (goroutines, allocs). Wire an alert when
  `up == 0` or 5xx ratio > threshold. A minimal, self-managed alert route:
  any uptime checker pointing at `/readyz` from a remote box.

### Kafka specifics

- Topic `eventnu.events` auto-creates (1 partition, RF 1 — the app's
  `EnsureTopic` also creates it at worker start).
- Container restart IDs: broker persists to `eventnu-prod_kafka_data`;
  this is fine to lose (at-least-once delivery design re-queues everything —
  outbox rows are the durable source of truth), but keep the volume for
  cleanliness.
- Scale-out note: the broker is single-node and single-partition — enough for
  launch; revisit if fanout volume grows beyond latency budget.

---

## 3. Hard limits to respect (or change design first)

1. **One API replica, one worker replica.** The rate limiter, idempotency
   store, FCM token cache, and `SKIP LOCKED` leases assume it. Second replica:
   migrate those to a shared store first (`ratelimit.go` comments name the
   seam).
2. **`DATABASE_ADMIN_URL` reaches the owner role** — never give the API
   container the admin URL; only the `migrate` job gets it (injected per
   run from Vault).
3. **No plaintext HTTP in front of nginx** — the loopback `:8080` publish on
   host exists only for ops and must stay loopback-bound.
4. **Vault unseal key and root token never live on this VM.** The moment
   they exist only in `vault_data` + a sealed container, a lost unseal key
   equals data loss of every secret (see §2 "Vault"). Vault's file volume
   is the single runbook copy of the secrets — the operator's off-VM copy
   is the backup plan for the whole secret store.
5. **One Vault, file storage, manual unseal** is the deliberate posture for
   a single VM. A second app replica (already prevented by limit 1) or a
   Vault HA/managed setup changes this paragraph first. Vault runs with
   `disable_mlock` (mlock is unavailable on several hosts and crashes the
   server) — compensate by keeping the VM swap-free (`swapoff` in the
   first-deploy checklist), otherwise Vault memory can page to disk.
4. **Domain name in nginx.conf is the single point of truth** for the certbot
   paths (they are templated together).

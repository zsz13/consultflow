# ConsultFlow

An exception-first, API-driven rebuild of the VistA/CPRS **Consult/Request Tracking** workflow (package GMRC,
file #123 REQUEST/CONSULTATION). The state machine, guards and audit trail are recovered from the GMRC MUMPS
routines in the OSEHRA VistA-M source tree (`Packages/Consult Request Tracking/`). Each rule links back to the exact
legacy line it came from, and an automated test checks every one of those line citations against the source.

This repository contains only the modern application; the legacy source is not included. All data is synthetic.
This is a hackathon/demo system: it has no authentication and must not hold real patient data.

## Run it

```sh
docker compose up --build
```

This is the local development stack; its ports bind to `127.0.0.1` only. For a public server see
[Deploy to a VPS](#deploy-to-a-vps).

- App: <http://localhost:8080>
- API: <http://localhost:8081/api/dashboard> (also proxied at `:8080/api`)
- PostgreSQL: `localhost:55432` (user/password/db `consultflow`)

On first start the backend creates the schema and loads 17 synthetic consults, with timings relative to the current
time. To restore them later, use **Reset demo data** on the dashboard (or `POST /api/demo/reset`). To start completely
fresh, run `docker compose down -v`.

## Five-minute demo

1. **Dashboard.** "10 consults need attention", ranked critical first. Each row says *why* in one sentence: a STAT
   consult not received for 30 hours, a consult open 70 days, a cancelled consult 4 days from automatic discontinue, an
   unsigned note blocking completion, a possible duplicate order. The status flow on the right is the state machine with
   live counts.
2. **Open #6 (ZZTEST, ECHO).** It is blocked by an unsigned note. The panel explains that partial results prevent
   forward, discontinue and cancel. Expand **Why 7 actions are unavailable** to see each refusal and the rule behind it.
   Choose **Sign note** and the consult completes. The track, timeline and dashboard all update.
3. **Switch "Acting as"** to *Requesting provider*. The available actions change, because permissions come from the
   server.
4. **Open #12 (ZZTEST, KILO)**, an inter-facility consult this site placed. A service clinician can only comment,
   because the remote filling facility owns the workflow (legacy rule `R-IFC-PLACER`). The requesting provider can also
   discontinue the order.
5. **Dashboard → Run auto-discontinue job.** #8 (cancelled 35 days) becomes discontinued, reproducing the legacy
   overnight job `GMRCCXDC`.
6. **Legacy rules.** Every rule, the legacy file and line it was recovered from, and where it lives in this codebase.

## Architecture

```
browser ──> nginx (frontend container, :8080) ──/api──> Go API (:8081) ──> PostgreSQL
            React + TypeScript SPA                      net/http + pgx
```

```
consultflow/
├── docker-compose.yml
├── docs/legacy-analysis.md       what was recovered from GMRC, with evidence and assumptions
├── backend/
│   ├── cmd/consultflow/          entry point: migrate, seed on first run, serve
│   └── internal/
│       ├── workflow/             pure domain: statuses, actions, guards (Decide), attention rules,
│       │                         explanations, traceability registry; no I/O, fully unit-tested
│       ├── store/                PostgreSQL schema, row-locked transactional actions, audit log, seed
│       └── api/                  REST handlers + integration tests against a real database
└── frontend/                     Vite + React 19 + TypeScript, no UI kit or router dependency
```

Design choices:

- **One decision function.** Every state change, whether from a user, the seed data or the overnight job, goes through
  `workflow.Decide`. The seed replays its scenarios through the same path, so demo data cannot contain an impossible
  history.
- **Server-side validation only.** The UI shows what `GET /api/consults/{id}?role=` reports as available. It never
  decides on its own.
- **Audit is append-only.** `consult_activities` has a trigger that rejects `UPDATE` and `DELETE`. Each entry stores the
  previous status, the new status, the actor and role, the time, the comment and the legacy action type (#123.1).
- **Row locks.** An action runs in a transaction holding `SELECT … FOR UPDATE` with a 2-second lock timeout. This
  mirrors GMRC's `L +^GMR(123,GMRCO):2`; a second concurrent action gets `409`.

## Workflow model

| Status          | ORDER STATUS #100.01 | Meaning                                                            |
| --------------- | -------------------- | ------------------------------------------------------------------ |
| PENDING         | 5 `pend`             | Ordered, forwarded or resubmitted; not yet received                |
| ACTIVE          | 6 `actv`             | Received by the consulting service                                 |
| SCHEDULED       | 8 `schd`             | Scheduled; waiting for results                                     |
| PARTIAL_RESULTS | 9 `part`             | An unsigned note is linked                                         |
| CANCELLED       | 13 `canc`            | Denied by the service; back with the requester for edit/resubmit   |
| COMPLETE        | 2 `comp`             | Signed note or administrative completion                           |
| DISCONTINUED    | 1 `dc`               | Stopped                                                            |

| Action (123.1 type)                       | From                                     | To                          |
| ----------------------------------------- | ---------------------------------------- | --------------------------- |
| Receive (21)                              | PENDING                                  | ACTIVE                      |
| Schedule (8)                              | PENDING, ACTIVE                          | SCHEDULED                   |
| Forward (17, or 25 to a remote service)   | PENDING, ACTIVE, SCHEDULED; no unsigned note | PENDING at the new service |
| Discontinue (6) / Cancel-deny (19)        | PENDING, ACTIVE, SCHEDULED; comment required | DISCONTINUED / CANCELLED |
| Edit & resubmit (11)                      | CANCELLED                                | PENDING                     |
| Link note: unsigned (9) / signed (10, 14) | any except DISCONTINUED, CANCELLED       | PARTIAL_RESULTS / COMPLETE (a complete consult stays COMPLETE) |
| Sign note (10, 14)                        | any except DISCONTINUED, CANCELLED       | COMPLETE                    |
| Administrative complete (10)              | PENDING … PARTIAL_RESULTS; comment required | COMPLETE                 |
| Comment (20), significant findings (4)    | any                                      | unchanged                   |
| Auto-discontinue (6, system job only)     | CANCELLED for 31+ days                   | DISCONTINUED                |

A consult this facility placed at another facility (IFC placer) only accepts comments, edit/resubmit, and a
discontinue by the requesting provider (as a CPRS order DC). See
[docs/legacy-analysis.md](docs/legacy-analysis.md) for evidence, legacy quirks and assumptions.

## API

| Method | Path                            | Purpose                                                                                                |
| ------ | ------------------------------- | ------------------------------------------------------------------------------------------------------ |
| GET    | `/api/dashboard`                | Status counts, attention list, per-service load, recent activity                                       |
| GET    | `/api/consults`                 | List; `bucket=attention\|active\|awaiting_scheduling\|awaiting_results\|returned\|closed\|all`, `status`, `serviceId`, `q` |
| POST   | `/api/consults`                 | Place a consult; `409 DUPLICATE` unless `acknowledgeDuplicate`                                         |
| GET    | `/api/consults/{id}?role=`      | Consult, attention flags, explanation, actions available to `role`                                     |
| GET    | `/api/consults/{id}/timeline`   | Audit trail, oldest first                                                                              |
| POST   | `/api/consults/{id}/actions`    | Take an action (`{action, role, actor, comment, …}`)                                                   |
| POST   | `/api/jobs/auto-discontinue`    | Run the GMRCCXDC equivalent now                                                                        |
| GET    | `/api/traceability`             | Rule registry with legacy references                                                                   |
| GET    | `/api/meta`, `/api/services`    | Statuses, action definitions, urgencies, roles; services                                               |
| POST   | `/api/demo/reset`               | Reload the synthetic scenarios (demo only; this also truncates the audit log)                          |

Rejected actions return `409` (the state does not allow it), `422` (missing or invalid input) or `403` (role), with the
rule id, for example:

```json
{ "error": { "kind": "CONFLICT", "rule": "R-RECEIVE", "ruleTitle": "Receive only from PENDING",
             "message": "The receive action may only be taken when the consult has a pending status." } }
```

## Development and tests

Backend (Go 1.25+):

```sh
cd backend
go vet ./... && go test ./...                      # unit tests; integration tests skip
docker compose up -d db                            # from the repository root
TEST_DATABASE_URL='postgres://consultflow:consultflow@localhost:55432/consultflow_test?sslmode=disable' \
  go test ./...                                    # + API integration tests (creates consultflow_test)
go run ./cmd/consultflow                           # API on :8081 against the compose database
```

- `internal/workflow`: the full status × action transition matrix, written independently from the implementation;
  legacy action codes; guards; roles; IFC; notes; auto-discontinue; attention thresholds; and a check that every
  suggested next step is actually allowed. `trace_test.go` checks that every modern file referenced by a rule exists,
  and that every cited legacy line exists and matches. That second check needs a VistA-M checkout and is skipped
  without one: `VISTA_ROOT=/path/to/VistA-M go test ./internal/workflow/`.
- `internal/api`: seeded state, action flow and audit rows, error codes, append-only trigger, overnight job, duplicate
  warning, concurrent actions serialized by the row lock, IFC placer restrictions.

Frontend (Node 22):

```sh
cd frontend
npm ci
npm run dev          # http://localhost:5173, proxies /api to :8081
npm run lint && npm run typecheck && npm run format:check && npm run build
```

Tooling notes: ESLint is pinned to 9.x because `eslint-plugin-jsx-a11y` does not yet support ESLint 10, and TypeScript is
pinned to 6.0 because `typescript-eslint` does not yet support TypeScript 7.

## Deploy to a VPS

A single Linux VPS running Docker Compose. nginx is the only service with public ports, and it serves the frontend,
proxies `/api/` to the Go API, and terminates HTTPS with a Let's Encrypt certificate. The files involved are
`docker-compose.prod.yml`, `deploy/nginx/`, `.env.example` and `scripts/deploy-vps.sh`.

**1. DNS.** Create an A record `consultflow.zsz13.com → <VPS public IPv4>` and wait until it resolves.

**2. Server prerequisites.** Docker Engine with the Compose v2 plugin, `git` and `curl`. Inbound TCP 80 and 443 must be
open (firewall and provider security group); nothing else needs to be public.

```sh
curl -fsSL https://get.docker.com | sh      # Docker Engine + Compose plugin (Debian/Ubuntu and others)
sudo usermod -aG docker "$USER"             # then log out and back in
```

**3. Deploy.**

```sh
git clone git@github.com:zsz13/consultflow.git
cd consultflow
git checkout main
./scripts/deploy-vps.sh
```

On the first run the script:

1. Checks Docker and Compose.
2. Creates `.env` from `.env.example` with a generated PostgreSQL password (mode 600, git-ignored).
3. Checks that the domain resolves to this server.
4. Obtains the certificate with certbot in standalone mode.
5. Builds and starts the stack and waits for every health check.
6. Verifies the HTTP→HTTPS redirect, the frontend, client-side routes and `/api/health`.
7. Prints `https://consultflow.zsz13.com`.

To receive expiry emails, set `LETSENCRYPT_EMAIL` in `.env`. To rehearse without touching Let's Encrypt's production
rate limits, set `LETSENCRYPT_STAGING=1` (the certificate will be untrusted). A later run with `0` replaces it.

**Redeploy / update:** `git pull && ./scripts/deploy-vps.sh`. Reruns never delete volumes, keep `.env` and the database
password, and skip certificate issuance when a valid certificate exists.

**Operations**

```sh
docker compose -f docker-compose.prod.yml ps                 # status and health
docker compose -f docker-compose.prod.yml logs -f backend    # logs (db, backend, web, certbot)
docker compose -f docker-compose.prod.yml exec db pg_dump -U consultflow consultflow > backup.sql
```

- **Certificate renewal is automatic.** The `certbot` service runs `certbot renew --webroot` at start and every 12
  hours, renewing within 30 days of expiry, and nginx reloads every 6 hours to pick up the new certificate.
- **PostgreSQL data** lives in the `consultflow-prod_pgdata` volume.
- **Do not change `POSTGRES_PASSWORD`** after the first run. The existing database keeps its original password.
- **`docker compose -f docker-compose.prod.yml down -v`** deletes all data and certificates.

What this deployment is not:

- No authentication: anyone with the URL can view the demo and take actions, including **Reset demo data**.
- A single host, with no replication or managed backups.
- Synthetic data only.

Put it behind HTTP basic auth or an IP allow-list at nginx if the demo must be private.

### VPS sizing

Measured on this stack:

| Resource | Measurement |
| --- | --- |
| Runtime memory, all containers idle | about 90 MB (PostgreSQL 52 MB, API 15 MB, nginx 17 MB, plus certbot) |
| Peak build RSS | `go build` ~420 MB, `npm ci` ~365 MB, `tsc` ~235 MB, `vite build` ~145 MB |
| Images and build cache | about 1.5–2.5 GB, including the Go and Node build images |

Compose builds the backend and frontend images in parallel, so a first deploy needs roughly 0.8–1 GB free for the
build on top of the running database.

| | vCPU | RAM | Disk |
| --- | --- | --- | --- |
| Minimum demo | 1 | 1 GB **plus 2 GB swap** | 10 GB |
| Recommended hackathon/demo | 2 | 2 GB | 20 GB |

Building on the VPS is what sets the RAM floor. The running app would fit in 512 MB, but a 1 GB server without swap
can be OOM-killed during the parallel image build. Add swap on a 1 GB server, or build one service at a time
(`docker compose -f docker-compose.prod.yml build backend`, then `build web`) before running the script. Two vCPUs
mostly shorten build time; the running demo is idle most of the time.

## Out of scope

Authentication (roles are chosen in the UI and trusted by the server), HL7/CPRS order messaging and alerts (the
timeline marks IFC updates as simulated), note text and TIU integration, Clinical Procedures, disassociating results,
and consult reports. See the assumptions in [docs/legacy-analysis.md](docs/legacy-analysis.md).

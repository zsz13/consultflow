#!/usr/bin/env bash
# First deployment and redeployment of ConsultFlow on a single Linux VPS.
#
#   ./scripts/deploy-vps.sh
#
# Safe to rerun: it never deletes volumes, keeps the existing .env and
# PostgreSQL password, and only requests a certificate when none is present.
#
# Optional environment overrides:
#   SKIP_DNS_CHECK=1   skip comparing DNS with this server's public IP
#                      (e.g. behind a proxy or NAT that hides the public IP)
set -euo pipefail

cd "$(dirname "$0")/.."
COMPOSE=(docker compose -f docker-compose.prod.yml)

log() { printf '\n==> %s\n' "$*"; }
ok() { printf '    ok: %s\n' "$*"; }
die() { printf '\nERROR: %s\n' "$*" >&2; exit 1; }

# 1. Tooling ------------------------------------------------------------------
log "Checking Docker"
command -v docker >/dev/null || die "Docker is not installed. See https://docs.docker.com/engine/install/"
docker compose version >/dev/null 2>&1 || die "Docker Compose v2 plugin is missing (docker compose version failed)."
docker info >/dev/null 2>&1 || die "Cannot talk to the Docker daemon. Start it, or run as a user in the 'docker' group (or with sudo)."
command -v curl >/dev/null || die "curl is required."
ok "$(docker --version); $(docker compose version --short 2>/dev/null || true)"

# 2. Configuration -------------------------------------------------------------
log "Checking configuration (.env)"
if [[ ! -f .env ]]; then
  [[ -f .env.example ]] || die ".env.example is missing."
  password="$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')"
  umask 077
  awk -v pw="$password" '/^POSTGRES_PASSWORD=/ { print "POSTGRES_PASSWORD=" pw; next } { print }' .env.example > .env
  chmod 600 .env
  ok "created .env with a generated PostgreSQL password (keep this file; it is git-ignored)"
fi
set -a
# shellcheck disable=SC1091
. ./.env
set +a

DOMAIN="${DOMAIN:-}"
LETSENCRYPT_EMAIL="${LETSENCRYPT_EMAIL:-}"
LETSENCRYPT_STAGING="${LETSENCRYPT_STAGING:-0}"
[[ "$DOMAIN" =~ ^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?\.[A-Za-z]{2,}$ ]] || die "DOMAIN in .env is not a valid hostname: '$DOMAIN'"
[[ -n "${POSTGRES_PASSWORD:-}" && "$POSTGRES_PASSWORD" != "change-me" ]] || die "Set POSTGRES_PASSWORD in .env (or delete .env to generate one)."
[[ "$POSTGRES_PASSWORD" =~ ^[A-Za-z0-9]+$ ]] || die "POSTGRES_PASSWORD must contain only letters and digits."
[[ "$LETSENCRYPT_STAGING" == 0 || "$LETSENCRYPT_STAGING" == 1 ]] || die "LETSENCRYPT_STAGING must be 0 or 1."
"${COMPOSE[@]}" config -q || die "docker-compose.prod.yml does not validate."
ok "domain $DOMAIN, staging=$LETSENCRYPT_STAGING"

# 3. DNS -----------------------------------------------------------------------
log "Checking DNS for $DOMAIN"
resolve_ipv4() {
  if command -v getent >/dev/null; then getent ahostsv4 "$1" | awk '{print $1}' | sort -u
  elif command -v dig >/dev/null; then dig +short A "$1"
  elif command -v host >/dev/null; then host -t A "$1" | awk '/has address/ {print $4}'
  fi
}
resolved="$(resolve_ipv4 "$DOMAIN" || true)"
[[ -n "$resolved" ]] || die "$DOMAIN does not resolve. Create a DNS A record: $DOMAIN -> <this server's public IPv4>, wait for it to propagate, then rerun."
ok "$DOMAIN resolves to: $(echo "$resolved" | tr '\n' ' ')"
if [[ "${SKIP_DNS_CHECK:-0}" != 1 ]]; then
  public_ip="$(curl -4 -fsS --max-time 5 https://api.ipify.org 2>/dev/null || curl -4 -fsS --max-time 5 https://ifconfig.me 2>/dev/null || true)"
  if [[ -z "$public_ip" ]]; then
    printf '    warning: could not determine this server'"'"'s public IP; continuing with DNS as resolved.\n'
  elif ! grep -qxF "$public_ip" <<<"$resolved"; then
    die "$DOMAIN resolves to $(echo "$resolved" | tr '\n' ' ')but this server's public IP is $public_ip. Fix the A record (or set SKIP_DNS_CHECK=1 if a proxy/NAT is expected)."
  else
    ok "matches this server's public IP ($public_ip)"
  fi
fi

# 4-5. Certificate -------------------------------------------------------------
log "Checking TLS certificate"
certbot_sh() { "${COMPOSE[@]}" run --rm --no-deps --entrypoint sh certbot -c "$1"; }
live="/etc/letsencrypt/live/$DOMAIN"
need_cert=0
if ! certbot_sh "test -f $live/fullchain.pem && test -f $live/privkey.pem" >/dev/null 2>&1; then
  need_cert=1
elif [[ "$LETSENCRYPT_STAGING" == 0 ]] && certbot_sh "grep -q acme-staging /etc/letsencrypt/renewal/$DOMAIN.conf" >/dev/null 2>&1; then
  ok "existing certificate is from the staging CA; replacing it with a trusted one"
  certbot_sh "certbot delete --non-interactive --cert-name $DOMAIN" >/dev/null
  need_cert=1
fi

if [[ "$need_cert" == 1 ]]; then
  # The first certificate is issued in standalone mode, before nginx exists.
  # nginx cannot start without a certificate, so port 80 must be free here.
  "${COMPOSE[@]}" stop web >/dev/null 2>&1 || true
  if command -v ss >/dev/null && ss -ltnH '( sport = :80 )' | grep -q .; then
    die "Port 80 is in use by another process (e.g. a host nginx/apache). Stop it, then rerun."
  fi
  certbot_args=(certonly --standalone --non-interactive --agree-tos --cert-name "$DOMAIN" -d "$DOMAIN")
  if [[ -n "$LETSENCRYPT_EMAIL" ]]; then certbot_args+=(--email "$LETSENCRYPT_EMAIL" --no-eff-email)
  else certbot_args+=(--register-unsafely-without-email); fi
  [[ "$LETSENCRYPT_STAGING" == 1 ]] && certbot_args+=(--staging)
  log "Requesting a Let's Encrypt certificate for $DOMAIN"
  "${COMPOSE[@]}" run --rm --no-deps -p 80:80 --entrypoint certbot certbot "${certbot_args[@]}" \
    || die "Certificate request failed. Check that port 80 is reachable from the internet (firewall/security group) and that DNS points here."
  ok "certificate issued"
else
  ok "certificate for $DOMAIN already present (renewal is automatic)"
fi

# 6. Start / update the stack --------------------------------------------------
log "Building and starting the stack (data volumes are kept)"
"${COMPOSE[@]}" up -d --build --remove-orphans

# 7. Health --------------------------------------------------------------------
log "Waiting for health checks"
deadline=$((SECONDS + 240))
for svc in db backend web; do
  while :; do
    cid="$("${COMPOSE[@]}" ps -q "$svc")"
    status="$( [[ -n "$cid" ]] && docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$cid" 2>/dev/null || echo missing)"
    [[ "$status" == healthy ]] && { ok "$svc healthy"; break; }
    if (( SECONDS > deadline )); then
      "${COMPOSE[@]}" logs --tail 40 "$svc" >&2 || true
      die "$svc did not become healthy (status: $status)."
    fi
    sleep 3
  done
done

# 8. End-to-end verification (through nginx on this host, with real TLS/SNI) --
log "Verifying HTTPS end to end"
curl_local=(curl -sS --max-time 15 --resolve "$DOMAIN:80:127.0.0.1" --resolve "$DOMAIN:443:127.0.0.1")
[[ "$LETSENCRYPT_STAGING" == 1 ]] && curl_local+=(--insecure)

redirect="$("${curl_local[@]}" -o /dev/null -w '%{http_code} %{redirect_url}' "http://$DOMAIN/consults")"
[[ "$redirect" == "301 https://$DOMAIN/consults" ]] || die "HTTP did not redirect to HTTPS (got: $redirect)."
ok "http://$DOMAIN redirects to https"

home="$("${curl_local[@]}" -f "https://$DOMAIN/")" || die "HTTPS request for / failed."
grep -q 'id="root"' <<<"$home" || die "HTTPS / did not return the frontend."
ok "frontend served over HTTPS"

spa="$("${curl_local[@]}" -o /dev/null -w '%{http_code}' "https://$DOMAIN/some/client/route")"
[[ "$spa" == 200 ]] || die "Client-side route fallback failed (HTTP $spa)."
ok "client-side routes fall back to index.html"

health="$("${curl_local[@]}" -f "https://$DOMAIN/api/health")" || die "API health check through nginx failed."
grep -q '"ok"' <<<"$health" || die "Unexpected API health response: $health"
ok "API reachable at https://$DOMAIN/api/health"

curl_public=(curl -sS --max-time 10 -o /dev/null)
[[ "$LETSENCRYPT_STAGING" == 1 ]] && curl_public+=(--insecure)
if "${curl_public[@]}" "https://$DOMAIN/api/health" 2>/dev/null; then
  ok "reachable from this host via public DNS"
else
  printf '    note: could not reach https://%s via public DNS from this host (hairpin NAT or firewall); check from your browser.\n' "$DOMAIN"
fi

log "ConsultFlow is live: https://$DOMAIN"
[[ "$LETSENCRYPT_STAGING" == 1 ]] && printf 'Using a STAGING certificate: browsers will warn. Set LETSENCRYPT_STAGING=0 in .env and rerun for a trusted one.\n'
exit 0

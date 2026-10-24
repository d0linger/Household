#!/bin/sh
set -eu
umask 077
cd "$(dirname "$0")/.."
if [ -e .local/secrets ]; then echo 'Secrets already exist; preserve them and use the recovery runbook.' >&2; exit 1; fi
mkdir -p .local/secrets
for name in postgres_password owner_password app_password csrf_secret encryption_key rate_secret bootstrap_password restic_password; do
  dd if=/dev/urandom bs=36 count=1 2>/dev/null | base64 | tr '+/' '-_' | tr -d '\n=' > ".local/secrets/$name"
done
printf 'postgres://household_app:%s@db:5432/household?sslmode=disable' "$(cat .local/secrets/app_password)" > .local/secrets/app_database_url
printf 'postgres://household_owner:%s@db:5432/household?sslmode=disable' "$(cat .local/secrets/owner_password)" > .local/secrets/owner_database_url
printf 'postgres://household_owner:%s@restore-db:5432/household_restore?sslmode=disable' "$(cat .local/secrets/owner_password)" > .local/secrets/restore_database_url
if [ ! -e .env ]; then
  if [ "${1:-}" = '--development' ]; then printf 'HOUSEHOLD_ENV=development\nHOUSEHOLD_ORIGIN=http://localhost:8100\nHOUSEHOLD_PORT=8100\nHOUSEHOLD_NAME=Mein Haushalt\n' > .env
  else cp .env.example .env; fi
fi
echo 'Secrets created in .local/secrets. Read bootstrap_password locally for first login; change it immediately.'

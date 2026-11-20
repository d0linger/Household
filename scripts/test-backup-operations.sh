#!/usr/bin/env bash
# End-to-end rehearsal of backup operations with the image under test:
# local backup, offsite copy, restore checks, a live restore while the app
# runs, rollback and discarding the kept previous state.
#
# Every container, network, volume and secret belongs to this run only and is
# removed afterwards. The offsite repository is a second local volume mounted as
# a path; S3/REST transports use the same restic commands but are not contacted.
#
#   scripts/test-backup-operations.sh [IMAGE]   (default: builds household:backup-ops-test)
set -euo pipefail

run="$(od -An -N6 -tx1 /dev/urandom | tr -d ' \n')"
prefix="household-backup-ops-$run"
image="${1:-}"
network="$prefix-net"
db="$prefix-db"
app="$prefix-app"
backup="$prefix-backup"
work_dir="$(mktemp -d)"
report_dir="outputs/backup-operations"
failures=0

log() { printf '[backup-ops %s] %s\n' "$run" "$*"; }
fail() { log "FAIL: $*"; failures=$((failures + 1)); }
random() { od -An -N24 -tx1 /dev/urandom | tr -d ' \n'; }

cleanup() {
  log "cleaning up"
  docker rm -f "$app" "$backup" "$db" >/dev/null 2>&1 || true
  for volume in uploads backups offsite work; do docker volume rm "$prefix-$volume" >/dev/null 2>&1 || true; done
  docker network rm "$network" >/dev/null 2>&1 || true
  rm -rf "$work_dir"
}
trap cleanup EXIT

if [[ -z "$image" ]]; then
  image="household:backup-ops-test"
  log "building $image (runtime target)"
  docker build --quiet --target runtime -t "$image" . >/dev/null
fi

postgres_password="$(random)"; owner_password="$(random)"; app_password="$(random)"
env_file="$work_dir/household.env"
umask 077
cat >"$env_file" <<EOF
HOUSEHOLD_ENV=development
HOUSEHOLD_ORIGIN=http://localhost:8199
HOUSEHOLD_DATABASE_HOST=$db:5432
POSTGRES_PASSWORD=$postgres_password
HOUSEHOLD_OWNER_PASSWORD=$owner_password
HOUSEHOLD_APP_PASSWORD=$app_password
HOUSEHOLD_CSRF_SECRET=$(random)
HOUSEHOLD_RATE_SECRET=$(random)
HOUSEHOLD_ENCRYPTION_KEY=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
HOUSEHOLD_ADMIN_PASSWORD=Backup operations fixture 1234
HOUSEHOLD_WEB_PUSH_ENABLED=false
RESTIC_PASSWORD=$(random)
HOUSEHOLD_BACKUP_OFFSITE_REPOSITORY=/offsite/household
HOUSEHOLD_BACKUP_OFFSITE_PASSWORD=$(random)
EOF

docker network create "$network" >/dev/null
for volume in uploads backups offsite work; do docker volume create "$prefix-$volume" >/dev/null; done
# The image runs as 10001; give it its writable volumes like Compose does.
docker run --rm -v "$prefix-uploads:/data/uploads" -v "$prefix-backups:/data/backups" -v "$prefix-offsite:/offsite" -v "$prefix-work:/work" \
  --user 0 --entrypoint sh "$image" -c 'chown 10001:10001 /data/uploads /data/backups /offsite /work' >/dev/null

docker run -d --name "$db" --network "$network" --user 70:70 -e POSTGRES_DB=household -e POSTGRES_PASSWORD="$postgres_password" \
  --tmpfs /var/lib/postgresql:rw,size=768m,uid=70,gid=70 \
  postgres:18-alpine@sha256:ff64c9e3bdf8e2120107d3559de8db314e8305f7e38c5105903c6295876738ff >/dev/null
for _ in $(seq 1 60); do docker exec "$db" pg_isready -U postgres -d household >/dev/null 2>&1 && break; sleep 1; done

psql() { docker exec -i "$db" psql -U postgres -d household -v ON_ERROR_STOP=1 -Atqc "$1"; }

start_app() {
  docker run -d --name "$app" --network "$network" --env-file "$env_file" --read-only --tmpfs /tmp:size=64m \
    -v "$prefix-uploads:/data/uploads" "$image" start >/dev/null
  for _ in $(seq 1 180); do docker exec "$app" household healthcheck >/dev/null 2>&1 && return 0; sleep 2; done
  docker logs --tail 40 "$app"; return 1
}
log "starting app"
start_app || { fail "app did not become ready"; exit 1; }
docker run -d --name "$backup" --network "$network" --env-file "$env_file" --read-only --tmpfs /tmp:size=64m \
  -e HOUSEHOLD_DATABASE_USER=household_owner -e HOUSEHOLD_DATABASE_PASSWORD="$owner_password" \
  -v "$prefix-uploads:/data/uploads" -v "$prefix-backups:/data/backups" -v "$prefix-offsite:/offsite" -v "$prefix-work:/work" \
  "$image" backup-loop >/dev/null

# The web process never receives repository credentials.
# Read the environment first, so a failed inspection is a failure and not proof
# of absence; then scan the complete copy without printing any value.
if ! serve_environment="$(docker exec "$app" sh -c 'tr "\0" "\n" </proc/1/environ')"; then
  fail "could not inspect the serve process environment"
elif [[ -z "$serve_environment" ]]; then
  fail "serve process environment is empty"
else
  inherited="$(grep -Ec '^(RESTIC_PASSWORD|HOUSEHOLD_BACKUP_OFFSITE_PASSWORD|HOUSEHOLD_BACKUP_S3_SECRET_ACCESS_KEY)=' <<<"$serve_environment" || true)"
  [[ "$inherited" -eq 0 ]] || fail "serve process inherited a repository password"
fi
unset serve_environment

psql "UPDATE core.households SET name='Before restore'"
psql "UPDATE core.backup_settings SET offsite_enabled=true,probe_interval_days=0"
log "backup with offsite copy"
docker exec "$backup" household backup --offsite || fail "backup --offsite"
[[ "$(psql "SELECT count(*) FROM core.backup_runs WHERE state='complete' AND repository_kind='local'")" -ge 1 ]] || fail "no complete local run"
offsite_snapshot="$(psql "SELECT snapshot_reference FROM core.backup_runs WHERE state='complete' AND repository_kind='offsite' ORDER BY started_at DESC LIMIT 1")"
[[ ${#offsite_snapshot} -eq 64 ]] || fail "no complete offsite run"

log "restore checks"
docker exec "$backup" household restore-check latest || fail "local restore-check"
docker exec "$backup" household restore-check --offsite latest || fail "offsite restore-check"
[[ "$(psql "SELECT count(*) FROM core.backup_runs WHERE verified_at IS NOT NULL")" -eq 2 ]] || fail "both snapshots not verified"
docker exec "$backup" household backup-health >/dev/null || fail "backup-health reports unhealthy"

psql "UPDATE core.households SET name='After backup'"
# The safety backup taken by the live restore must not apply retention: with
# keep-daily it would forget today's earlier local snapshot.
local_snapshot="$(psql "SELECT snapshot_reference FROM core.backup_runs WHERE state='complete' AND repository_kind='local' ORDER BY started_at DESC LIMIT 1")"
[[ ${#local_snapshot} -eq 64 ]] || fail "no complete local snapshot before the restore"
log "live restore from the offsite snapshot while the app runs"
confirmation="WIEDERHERSTELLEN ${offsite_snapshot:0:8}"
confirmation="${confirmation^^}"
docker exec "$backup" household restore --offsite "$offsite_snapshot" --confirm "$confirmation" || fail "live restore"
[[ "$(psql "SELECT name FROM core.households")" == "Before restore" ]] || fail "restored data differs"
[[ "$(psql "SELECT state FROM core.backup_jobs WHERE kind='restore' ORDER BY created_at DESC LIMIT 1")" == "complete" ]] || fail "restore job not complete"
[[ "$(psql "SELECT count(*) FROM core.sessions")" -eq 0 ]] || fail "sessions survived the restore"
[[ "$(psql "SELECT count(*) FROM core.backup_runs WHERE state='complete'")" -ge 3 ]] || fail "backup history lost (safety backup missing)"
[[ "$(psql "SELECT state FROM core.backup_runs WHERE snapshot_reference='$local_snapshot' AND repository_kind='local'")" == "complete" ]] \
  || fail "the safety backup pruned today's earlier local snapshot"
repository_snapshots="$(docker exec "$backup" restic --no-cache snapshots --json)" || fail "could not list local snapshots"
[[ "$repository_snapshots" == *"$local_snapshot"* ]] || fail "today's earlier local snapshot is gone from the repository"
previous="$(psql "SELECT datname FROM pg_database WHERE datname LIKE 'household_pre_restore_%'")"
[[ -n "$previous" ]] || fail "previous database not kept"
for _ in $(seq 1 30); do docker exec "$app" household healthcheck >/dev/null 2>&1 && break; sleep 2; done
docker exec "$app" household healthcheck >/dev/null 2>&1 || fail "app did not resume after the restore"
docker logs "$app" 2>&1 | grep -q 'live restore: pausing the app' || fail "app never paused"

log "rollback"
docker stop "$app" >/dev/null && docker rm "$app" >/dev/null
docker exec "$backup" household restore-rollback || fail "restore-rollback"
[[ "$(psql "SELECT name FROM core.households")" == "After backup" ]] || fail "rollback did not return to the previous state"
[[ "$(psql "SELECT result_code FROM core.backup_jobs WHERE kind='restore' ORDER BY created_at DESC LIMIT 1")" == "rolled_back" ]] || fail "rolled back restore job not ended"
start_app || fail "app did not start after rollback"

log "discard previous state through a request"
admin="$(psql "SELECT id FROM core.users WHERE username='admin'")"
# The bootstrap admin must change its password first; the service accepts only active administrators.
psql "UPDATE core.users SET must_change_password=false WHERE id='$admin'"
# Discarding needs a recently re-authenticated session, as from the web page.
session="$(psql "INSERT INTO core.sessions(user_id,token_hash,auth_version,idle_expires_at,absolute_expires_at,user_agent,reauthenticated_at)
 VALUES('$admin',sha256(gen_random_uuid()::text::bytea),1,now()+interval '1 hour',now()+interval '1 hour','backup rehearsal',now()) RETURNING id")"
[[ ${#session} -eq 36 ]] || fail "rehearsal session"
psql "INSERT INTO core.backup_jobs(household_id,kind,target,requested_by,requested_session) SELECT id,'discard_previous','local','$admin','$session' FROM core.households" \
  || fail "discard request"
for _ in $(seq 1 40); do
  state="$(psql "SELECT state FROM core.backup_jobs WHERE kind='discard_previous' ORDER BY created_at DESC LIMIT 1")"
  [[ "$state" == "complete" || "$state" == "failed" ]] && break; sleep 3
done
[[ "$state" == "complete" ]] || fail "discard job ended as $state ($(psql "SELECT coalesce(result_code,'') FROM core.backup_jobs WHERE kind='discard_previous' ORDER BY created_at DESC LIMIT 1"))"
[[ -z "$(psql "SELECT datname FROM pg_database WHERE datname LIKE 'household_pre_restore_%' OR datname LIKE 'household_restore_job_%'")" ]] || fail "kept restore databases still exist"

log "recovery of a swap interrupted after the first rename"
# Stop everything that holds the live database, rename it as the swap's
# first step would, and leave the journal that swap writes beforehand. The
# restarted service must put the live name back before it opens its pool.
docker stop "$app" >/dev/null 2>&1 || true
docker rm "$app" >/dev/null 2>&1 || true
docker stop "$backup" >/dev/null
interrupted_previous="household_pre_restore_$(date +%s)_0badc0de"
docker exec -i "$db" psql -U postgres -d postgres -v ON_ERROR_STOP=1 -Atqc "ALTER DATABASE household RENAME TO $interrupted_previous" \
  || fail "simulate the first rename"
printf '{"job":"00000000-0000-4000-8000-000000000001","phase":"swapping","live":"household","stage":"household_restore_job_000000000001","previous":"%s","directory":"","moved":"","snapshot":"","target":"local","started":"2026-01-01T00:00:00Z"}' "$interrupted_previous" \
  | docker run --rm -i -v "$prefix-work:/work" --user 10001 --entrypoint sh "$image" -c 'cat > /work/restore-journal.json' || fail "write the interrupted journal"
docker start "$backup" >/dev/null
recovered=""
for _ in $(seq 1 30); do
  recovered="$(docker exec -i "$db" psql -U postgres -d postgres -Atqc "SELECT count(*) FROM pg_database WHERE datname='household'" 2>/dev/null || true)"
  [[ "$recovered" == "1" ]] && break; sleep 2
done
[[ "$recovered" == "1" ]] || { docker logs --tail 30 "$backup" 2>&1 | grep -v '"http request"' || true; fail "the live database name was not restored after the interruption"; }
[[ "$(psql "SELECT name FROM core.households")" == "After backup" ]] || fail "recovered database lost its data"
[[ -z "$(docker exec -i "$db" psql -U postgres -d postgres -Atqc "SELECT datname FROM pg_database WHERE datname='$interrupted_previous'")" ]] \
  || fail "the interrupted previous name still exists"
journal_left="$(docker run --rm -v "$prefix-work:/work" --user 10001 --entrypoint sh "$image" -c 'test -e /work/restore-journal.json && echo yes || echo no')"
[[ "$journal_left" == "no" ]] || fail "the recovered journal was not removed"

mkdir -p "$report_dir"
status="passed"; [[ $failures -eq 0 ]] || { status="failed"; docker logs --tail 60 "$backup" 2>&1 | grep -v '"http request"' || true; }
printf '{"run":"%s","image":"%s","status":"%s","failures":%d,"offsite_transport":"mounted path"}\n' "$run" "$image" "$status" "$failures" >"$report_dir/report-$run.json"
log "$status ($failures failures); report $report_dir/report-$run.json"
[[ $failures -eq 0 ]]

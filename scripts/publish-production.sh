#!/usr/bin/env bash
set -euo pipefail

# Only the protected main push job owns this short-lived write token. No build
# runs here: copy the same OCI graph accepted by all release dependencies.
: "${RUNNER_TEMP:?}" "${CANDIDATE_DIRECTORY:?}" "${GH_TOKEN:?}"
: "${GITHUB_ACTOR:?}" "${GITHUB_RUN_ID:?}" "${GITHUB_RUN_ATTEMPT:?}"
[[ "$GITHUB_RUN_ID" =~ ^[0-9]+$ && "$GITHUB_RUN_ATTEMPT" =~ ^[0-9]+$ ]]
node scripts/release-guard.cjs

task_directory="$(mktemp -d "$RUNNER_TEMP/household-publish.XXXXXX")"
trap 'rm -rf -- "$task_directory"' EXIT
umask 077
task_auth="$task_directory/auth.json"
task_layout="$task_directory/layout"
task_image='ghcr.io/d0linger/household'
task_candidate="build-${GITHUB_RUN_ID}-${GITHUB_RUN_ATTEMPT}"
mkdir "$task_layout"
tar --extract --file "$CANDIDATE_DIRECTORY/household.oci.tar" --directory "$task_layout" --no-same-owner --no-same-permissions
node scripts/check-oci.cjs "$task_layout" "$task_directory/verified.json"
task_digest="$(node scripts/release-image.cjs "$task_layout" "$task_directory/verified.json" "$CANDIDATE_DIRECTORY/household-candidate.json")"
printf '%s' "$GH_TOKEN" | skopeo login --authfile "$task_auth" --username "$GITHUB_ACTOR" --password-stdin ghcr.io
node scripts/release-guard.cjs
skopeo --command-timeout 5m copy --all --preserve-digests --retry-times 2 --authfile "$task_auth" \
  --digestfile "$task_directory/published-digest" "oci:$task_layout" "docker://$task_image:$task_candidate"
test "$(cat "$task_directory/published-digest")" = "$task_digest"
task_remote_digest="$(skopeo --command-timeout 1m inspect --raw --authfile "$task_auth" "docker://$task_image@$task_digest" | sha256sum | cut -d ' ' -f 1)"
test "sha256:$task_remote_digest" = "$task_digest"

# The serialized job plus a current-main check before each tag prevents an old
# rerun from replacing a newer release. Digest references remain authoritative.
for task_tag in "sha-${GITHUB_SHA}" production latest; do
  node scripts/release-guard.cjs
  skopeo --command-timeout 3m copy --all --preserve-digests --retry-times 2 --authfile "$task_auth" \
    --digestfile "$task_directory/promoted-digest" "docker://$task_image@$task_digest" "docker://$task_image:$task_tag"
  test "$(cat "$task_directory/promoted-digest")" = "$task_digest"
done
printf 'Published `%s@%s` as `latest`, `production` and `sha-%s`. No deployment was performed.\n' \
  "$task_image" "$task_digest" "$GITHUB_SHA" >> "$GITHUB_STEP_SUMMARY"

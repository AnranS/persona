#!/bin/bash
# Maintainer-approved, run-scoped policy for a fresh, exclusive GitHub-hosted runner; never a shared host.
set -euo pipefail
case "${1:?usage: load|first-run-ok|first-run-failed|cleanup}" in load|first-run-ok|first-run-failed|cleanup) ;; *) exit 2 ;; esac
: "${GITHUB_WORKSPACE:?workspace required}" "${GITHUB_RUN_ID:?run id required}" "${GITHUB_RUN_ATTEMPT:?run attempt required}" "${RUNNER_TEMP:?temporary directory required}"
[[ "${GITHUB_ACTIONS:-}" == true && "${CI:-}" == true ]] || { printf 'approved hosted CI environment required\n' >&2; exit 2; }
[[ "$GITHUB_RUN_ID" =~ ^[0-9]+$ ]] || { printf 'invalid run id\n' >&2; exit 2; }
[[ "$GITHUB_RUN_ATTEMPT" =~ ^[0-9]+$ ]] || { printf 'invalid run attempt\n' >&2; exit 2; }
[[ "$GITHUB_WORKSPACE" =~ ^/[A-Za-z0-9._/-]+$ ]] || { printf 'unsafe workspace path\n' >&2; exit 2; }
[[ "$RUNNER_TEMP" =~ ^/[A-Za-z0-9._/-]+$ ]] || { printf 'unsafe runner temp path\n' >&2; exit 2; }
name="persona-first-run-$GITHUB_RUN_ID-$GITHUB_RUN_ATTEMPT"
exe="$GITHUB_WORKSPACE/desktop/release/linux-unpacked/persona-desktop"
profile="$RUNNER_TEMP/$name.apparmor"
compiled="$RUNNER_TEMP/$name.policy"
receipt="$RUNNER_TEMP/$name.run.json"
cleanup_receipt="$RUNNER_TEMP/$name.cleanup.json"
ownership="$RUNNER_TEMP/$name.ownership"
profiles_file=/sys/kernel/security/apparmor/profiles
receipt_helper="$GITHUB_WORKSPACE/scripts/ci/desktop_first_run_apparmor_receipt.py"
[[ "$receipt_helper" =~ ^/[A-Za-z0-9._/-]+$ && -f "$receipt_helper" && ! -L "$receipt_helper" ]] || exit 2

# Read-only NAME (mode) presence is not proof that a concurrently replaced definition is ours.
profile_state() {
  local query_status
  [[ -f "$profiles_file" && ! -L "$profiles_file" && -r "$profiles_file" ]] || { printf 'error\n'; return; }
  grep -E -q -e "^${name} \\([^)]*\\)$" -- "$profiles_file" 2>/dev/null && { printf 'present\n'; return 0; }
  query_status=$?
  case "$query_status" in
    0) printf 'present\n' ;;
    1) printf 'absent\n' ;;
    *) printf 'error\n' ;;
  esac
}
query_state() {
  local state
  state="$(profile_state)"
  if [[ "$state" == error ]]; then
    printf 'error\n'
    return 1
  fi
  printf '%s\n' "$state"
}
fail() {
  local reason="$1"
  if ! python3 -I "$receipt_helper" load-failure "$name" "$RUNNER_TEMP" "$reason"; then
    printf 'load failed: %s; the prior run failure artifact is preserved\n' "$reason" >&2
  fi
  exit 1
}
cleanup_fail() {
  local removal="$1" reason="$2" verified="${3:-false}"
  if ! python3 -I "$receipt_helper" cleanup-failure "$name" "$RUNNER_TEMP" "$removal" "$reason" "$verified"; then
    printf 'cleanup failed: %s; the prior cleanup artifact is preserved\n' "$reason" >&2
  fi
  exit 1
}

if [[ "$1" == load ]]; then
  # No stale success can survive: every result path is unique and O_EXCL-published.
  python3 -I "$receipt_helper" begin-load "$name" "$RUNNER_TEMP" || exit 1
  [[ -f "$exe" && -x "$exe" && ! -L "$exe" ]] || fail missing-exact-test-executable
  [[ ! -e "$ownership" && ! -L "$ownership" ]] || fail ownership-record-already-exists
  # Establish local pending ownership before any privileged load.
  (set -o noclobber; printf 'load-pending\n' > "$ownership") || fail ownership-record-write-failed
  state="$(query_state)" || fail profile-query-failed
  [[ "$state" == absent ]] || fail profile-already-exists
  printf '%s\n' 'abi <abi/4.0>,' 'include <tunables/global>' '' \
    "profile $name $exe flags=(unconfined) {" '  userns,' '}' > "$profile" || fail profile-file-write-failed
  # Compile to an immutable binary input, then load that exact input. No cache replacement.
  apparmor_parser -Q -K -T -o "$compiled" "$profile" || fail policy-compilation-failed
  [[ -f "$compiled" && ! -L "$compiled" ]] || fail compiled-policy-not-regular
  if ! sudo apparmor_parser --abort-on-error -K -T -B -a "$compiled"; then
    # Ambiguity is never cleaned up automatically: a partial load and a foreign entry are indistinguishable here.
    rm -f -- "$ownership" || fail load-failed-ownership-invalidation-failed
    fail load-failed-owner-review-required
  fi
  state="$(query_state)" || fail profile-query-failed-after-load
  [[ "$state" == present ]] || fail load-verification-failed
  python3 -I "$receipt_helper" load-success "$name" "$RUNNER_TEMP" || {
    printf 'loaded profile requires owner cleanup because its success result could not be published\n' >&2
    exit 1
  }
elif [[ "$1" == first-run-ok || "$1" == first-run-failed ]]; then
  # The workflow calls this exactly once after its real first-run test, whether it passed or failed.
  outcome=failed
  if [[ "$1" == first-run-ok ]]; then outcome=passed; fi
  python3 -I "$receipt_helper" first-run-result "$name" "$RUNNER_TEMP" "$outcome"
else
  # Preserve the original run artifact. Cleanup cannot overwrite it.
  python3 -I "$receipt_helper" validate-first-run-result "$name" "$RUNNER_TEMP" || {
    printf 'cleanup refused: missing or failed run result is preserved\n' >&2
    exit 1
  }
  [[ -f "$ownership" && ! -L "$ownership" ]] || {
    printf 'cleanup refused: ownership record is missing\n' >&2
    exit 1
  }
  # The first attempt writes a pending cleanup artifact; retries never overwrite any result.
  python3 -I "$receipt_helper" cleanup-attempt "$name" "$RUNNER_TEMP" || {
    printf 'cleanup refused: prior cleanup artifact is preserved\n' >&2
    exit 1
  }
  state="$(query_state)" || cleanup_fail query-error profile-query-failed
  removed=already-absent
  if [[ "$state" == present ]]; then
    [[ -f "$profile" && ! -L "$profile" ]] || cleanup_fail removal-failed profile-file-not-regular
    if ! sudo apparmor_parser --abort-on-error -R "$profile"; then
      cleanup_fail removal-failed removal-failed
    fi
    removed=removed
  fi
  state="$(query_state)" || cleanup_fail query-error profile-query-failed
  [[ "$state" == absent ]] || cleanup_fail "$removed" removal-verification-failed
  if ! rm -f -- "$profile" "$compiled" "$ownership"; then
    cleanup_fail "$removed" local-file-cleanup-failed true
  fi
  python3 -I "$receipt_helper" cleanup-success "$name" "$RUNNER_TEMP" "$removed" || {
    printf 'cleanup result publication failed; pending cleanup evidence is retained\n' >&2
    exit 1
  }
  cat "$cleanup_receipt"
  if grep -F -q '"failure":"first-run-failed"' -- "$cleanup_receipt"; then
    exit 1
  fi
fi

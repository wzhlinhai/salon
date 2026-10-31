#!/usr/bin/env bash
# Usage: scripts/ci-wait.sh <pr-number>
# Exit 0: all checks pass or skip; 1: a check failed or was cancelled;
# 2: usage/API error (ten minutes of consecutive gh failures), or no workflow
# run within ten minutes of the head commit. Retry transient gh failures every
# ten seconds; any successful invocation resets the failure window.
# CI_WAIT_RETRY_SECONDS overrides the 600-second failure window.
# Accepted non-zero checks codes require non-empty stdout; otherwise retry.
# Wait for workflow runs, not gh pr checks --watch: jobs with needs: (such as
# e2e) appear only after their dependencies finish, so checks can look done early.
set -euo pipefail

if [[ $# != 1 || ! $1 =~ ^[1-9][0-9]*$ ]]; then
  echo 'usage: scripts/ci-wait.sh <pr-number>' >&2
  exit 2
fi
# Accepted check exit codes count as success only when they carry data.
retry_gh() {
  local accepted=$1 code output started=-1 now
  shift
  while true; do
    if output=$(command gh "$@"); then code=0; else code=$?; fi
    if [[ $code == 0 || ( -n $output && " $accepted " == *" $code "* ) ]]; then
      printf '%s\n' "$output"
      return "$code"
    fi
    now=$(date +%s)
    if (( started < 0 )); then started=$now; fi
    if (( now - started >= ${CI_WAIT_RETRY_SECONDS:-600} )); then return 2; fi
    sleep 10
  done
}

# Diagnostics are best effort: a failed check must keep exit 1 even offline.
print_annotations() {
  local jobs id name messages
  if ! jobs=$(command gh api "repos/{owner}/{repo}/commits/$sha/check-runs" --paginate \
      --jq '.check_runs[] | select(.conclusion == "failure" or .conclusion == "cancelled" or .conclusion == "timed_out" or .conclusion == "action_required" or .conclusion == "startup_failure" or .conclusion == "stale") | [.id, .name] | @tsv'); then
    echo 'could not fetch failed check runs for annotations' >&2
    return 0
  fi
  while IFS=$'\t' read -r id name; do
    [[ -n $id ]] || continue
    echo "annotations: $name (check-run $id)"
    if messages=$(command gh api "repos/{owner}/{repo}/check-runs/$id/annotations" --paginate --jq '.[].message'); then
      if [[ -n $messages ]]; then printf '%s\n' "$messages"; fi
    else
      echo "could not fetch annotations for $name (check-run $id)" >&2
    fi
  done <<< "$jobs"
}

pr=$1
sha=''
while true; do
  current=$(retry_gh "" pr view "$pr" --json headRefOid --jq .headRefOid) || exit 2
  if [[ $current != "$sha" ]]; then
    sha=$current
    committed=$(retry_gh "" api "repos/{owner}/{repo}/commits/$sha" --jq .commit.committer.date) || exit 2
    committed_at=$(date -d "$committed" +%s) || exit 2
  fi
  state=$(retry_gh "" run list --commit "$sha" --limit 1000 --json databaseId,status,conclusion \
    --jq 'if length == 0 then "none" elif all(.[]; .status == "completed") then "done" else "pending" end') || exit 2
  if [[ $state == none ]] && (( $(date +%s) - committed_at >= 600 )); then
    echo "no workflow run appeared within ten minutes of head commit $sha" >&2
    exit 2
  fi
  if [[ $state == done ]]; then
    # gh returns 1 for failed checks and 8 for pending checks; both still emit data.
    buckets=$(retry_gh "1 8" pr checks "$pr" --json bucket --jq '.[].bucket') || {
      code=$?
      if [[ $code != 1 && $code != 8 ]]; then exit 2; fi
    }
    # A new head invalidates the completed-run snapshot.
    current=$(retry_gh "" pr view "$pr" --json headRefOid --jq .headRefOid) || exit 2
    if [[ $current == "$sha" && -n $buckets ]]; then
      failed=false
      pending=false
      while IFS= read -r bucket; do
        case "$bucket" in
          pass|skipping) ;;
          fail|cancel) failed=true ;;
          *) pending=true ;;
        esac
      done <<< "$buckets"
      if ! $pending; then
        retry_gh "1" pr checks "$pr" || {
          code=$?
          if [[ $code != 1 ]]; then exit 2; fi
        }
        if $failed; then
          print_annotations
          exit 1
        fi
        exit 0
      fi
    fi
  fi
  sleep 10
done

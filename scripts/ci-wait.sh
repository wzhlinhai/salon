#!/usr/bin/env bash
# Usage: scripts/ci-wait.sh <pr-number>
# Exit 0: all checks pass or skip; 1: a check failed or was cancelled;
# 2: usage/API error (five consecutive failed gh invocations), or no workflow
# run within ten minutes of the head commit. Retry transient gh failures every
# ten seconds; any successful invocation resets the failure count.
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
  local accepted=$1 code output failures=0
  shift
  while true; do
    if output=$(command gh "$@"); then code=0; else code=$?; fi
    if [[ $code == 0 || ( -n $output && " $accepted " == *" $code "* ) ]]; then
      printf '%s\n' "$output"
      return "$code"
    fi
    failures=$((failures + 1))
    if (( failures >= 5 )); then return 2; fi
    sleep 10
  done
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
        if $failed; then exit 1; fi
        exit 0
      fi
    fi
  fi
  sleep 10
done

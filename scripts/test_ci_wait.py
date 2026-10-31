"""Exercise workflow waiting without a live pending PR or network."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('ci-wait.sh').resolve()


class CIWaitTests(unittest.TestCase):
    def run_fixture(self, scenario):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            (root / 'gh').write_text('''#!/bin/bash
printf '%s\\n' "$*" >> "$FIXTURE/calls"
count=0
if [[ -f $FIXTURE/count ]]; then read -r count < "$FIXTURE/count"; fi
count=$((count + 1))
echo "$count" > "$FIXTURE/count"
if [[ $SCENARIO == outage || ( $SCENARIO == recover && $count -le 7 ) ]]; then
  echo 'partial failed response'
  echo 'connection reset by peer' >&2
  exit 2
fi
# Three failures before each success must never accumulate across successes.
if [[ $SCENARIO == intermittent && $((count % 4)) != 0 ]]; then exit 2; fi
case "$1 $2" in
  'pr view') echo head-sha ;;
  'api repos/{owner}/{repo}/commits/head-sha') echo '2026-01-01T00:00:00Z' ;;
  'api repos/{owner}/{repo}/commits/head-sha/check-runs')
    expected='.check_runs[] | select(.conclusion == "failure" or .conclusion == "cancelled" or .conclusion == "timed_out" or .conclusion == "action_required" or .conclusion == "startup_failure" or .conclusion == "stale") | [.id, .name] | @tsv'
    if [[ $3 != --paginate || $4 != --jq || $5 != "$expected" || $# != 5 ]]; then
      echo 'check-runs requires pagination and the failed-conclusion filter' >&2
      exit 99
    fi
    if [[ $SCENARIO == check-runs-outage ]]; then echo 'EOF' >&2; exit 2; fi
    if [[ $SCENARIO == multiple-failures ]]; then
      printf '42\\tadmin\\n43\\tcancelled-job\\n44\\ttimed-out-job\\n45\\tstartup-job\\n'
    else printf '42\\tadmin\\n'; fi ;;
  'api repos/{owner}/{repo}/check-runs/42/annotations')
    if [[ $SCENARIO == annotation-outage ]]; then echo 'EOF' >&2; exit 2; fi
    echo 'The job was not started because billing is unavailable.' ;;
  'api repos/{owner}/{repo}/check-runs/43/annotations') echo 'Job was cancelled.' ;;
  'api repos/{owner}/{repo}/check-runs/44/annotations') echo 'Job timed out.' ;;
  'api repos/{owner}/{repo}/check-runs/45/annotations') echo 'Job failed to start.' ;;
  'run list')
    if [[ $SCENARIO == timeout ]]; then echo none
    elif [[ $SCENARIO == late && ! -f $FIXTURE/slept ]]; then echo pending
    else echo done; fi ;;
  'pr checks')
    mode=display
    if [[ $* == *--json* ]]; then mode=json; fi
    attempts=0
    if [[ -f $FIXTURE/checks-$mode-count ]]; then read -r attempts < "$FIXTURE/checks-$mode-count"; fi
    attempts=$((attempts + 1))
    echo "$attempts" > "$FIXTURE/checks-$mode-count"
    if [[ ( $mode == json && $SCENARIO == json-outage ) ||
          ( $mode == json && $SCENARIO == json-recover && $attempts -le 3 ) ||
          ( $mode == display && $SCENARIO == display-recover && $attempts -le 3 ) ]]; then
      echo 'connection reset by peer' >&2
      exit 1
    fi
    if [[ $* == *--json* ]]; then
      case "$SCENARIO" in
        fail|annotation-outage|check-runs-outage|multiple-failures) printf 'pass\\nfail\\n'; exit 1 ;;
        cancel) printf 'pass\\ncancel\\n'; exit 1 ;;
        *) printf 'pass\\npass\\nskipping\\n' ;;
      esac
    elif [[ $SCENARIO == multiple-failures ]]; then
      printf 'admin fail\ncancelled-job cancel\ntimed-out-job fail\nstartup-job fail\n'
    elif [[ $SCENARIO == fail || $SCENARIO == annotation-outage || $SCENARIO == check-runs-outage ]]; then
      printf 'backend pass\\nadmin fail 2s\\ne2e skipping\\n'
    else printf 'backend pass\\nadmin pass\\ne2e pass\\n'; fi ;;
  *) exit 99 ;;
esac
''')
            (root / 'date').write_text('#!/bin/sh\nif [ "$1" = -d ]; then echo 1000; else n=0; if [ -f "$FIXTURE/time" ]; then read -r n < "$FIXTURE/time"; fi; echo $((1600 + n)); fi\n')
            (root / 'sleep').write_text('#!/bin/sh\ntouch "$FIXTURE/slept"\nn=0; if [ -f "$FIXTURE/time" ]; then read -r n < "$FIXTURE/time"; fi; echo $((n + 10)) > "$FIXTURE/time"\n')
            for name in ('gh', 'date', 'sleep'):
                (root / name).chmod(0o755)
            env = dict(os.environ, PATH=str(root) + os.pathsep + os.environ['PATH'],
                       FIXTURE=str(root), SCENARIO=scenario, CI_WAIT_RETRY_SECONDS='80')
            result = subprocess.run([str(SCRIPT), '134'], env=env, capture_output=True, text=True, timeout=5)
            return result, (root / 'calls').read_text().splitlines()

    def test_completed_pass_and_skip(self):
        result, calls = self.run_fixture('pass')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, 'backend pass\nadmin pass\ne2e pass\n')
        self.assertIn('run list --commit head-sha --limit 1000 --json databaseId,status,conclusion', calls[2])

    def test_fail_and_cancel(self):
        for scenario in ('fail', 'cancel'):
            with self.subTest(scenario=scenario):
                result, _ = self.run_fixture(scenario)
                self.assertEqual(result.returncode, 1, result.stderr)
                self.assertIn('backend pass', result.stdout)

    def test_waits_for_workflow_completion_before_reading_checks(self):
        result, calls = self.run_fixture('late')
        self.assertEqual(result.returncode, 0, result.stderr)
        run_calls = [index for index, call in enumerate(calls) if call.startswith('run list')]
        check_calls = [index for index, call in enumerate(calls) if call.startswith('pr checks')]
        self.assertEqual(len(run_calls), 2)
        self.assertGreater(min(check_calls), max(run_calls))

    def test_no_run_timeout(self):
        result, calls = self.run_fixture('timeout')
        self.assertEqual(result.returncode, 2)
        self.assertEqual(result.stderr, 'no workflow run appeared within ten minutes of head commit head-sha\n')
        self.assertFalse(any(call.startswith('pr checks') for call in calls))

    def test_transient_failures_recover(self):
        for scenario in ('recover', 'intermittent'):
            with self.subTest(scenario=scenario):
                result, calls = self.run_fixture(scenario)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn('backend pass', result.stdout)
                self.assertGreater(len(calls), 6)
                if scenario == 'recover':
                    self.assertEqual(result.stderr, 'connection reset by peer\n' * 7)

    def test_failure_window_expires_exit_two(self):
        result, calls = self.run_fixture('outage')
        self.assertEqual(result.returncode, 2)
        self.assertEqual(len(calls), 9)
        self.assertEqual(result.stderr, 'connection reset by peer\n' * 9)

    def test_json_checks_empty_exit_one_stops_after_window(self):
        result, calls = self.run_fixture('json-outage')
        self.assertEqual(result.returncode, 2, result.stderr)
        self.assertEqual(result.stdout, '')
        self.assertEqual(result.stderr, 'connection reset by peer\n' * 9)
        self.assertEqual(sum(call.startswith('pr checks') and '--json' in call for call in calls), 9)
        self.assertEqual(sum(call.startswith('pr view') for call in calls), 1)
        self.assertNotIn('pr checks 134', calls)

    def test_json_checks_empty_exit_one_recovers(self):
        result, calls = self.run_fixture('json-recover')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, 'backend pass\nadmin pass\ne2e pass\n')
        self.assertEqual(result.stderr, 'connection reset by peer\n' * 3)
        self.assertEqual(sum(call.startswith('pr checks') and '--json' in call for call in calls), 4)
        self.assertEqual(calls.count('pr checks 134'), 1)

    def test_display_checks_empty_exit_one_waits_for_recovery(self):
        result, calls = self.run_fixture('display-recover')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, 'backend pass\nadmin pass\ne2e pass\n')
        self.assertEqual(result.stderr, 'connection reset by peer\n' * 3)
        self.assertEqual(calls.count('pr checks 134'), 4)
        self.assertEqual(sum(call.startswith('pr checks') and '--json' in call for call in calls), 1)

    def test_failed_job_annotations_follow_table(self):
        result, calls = self.run_fixture('fail')
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn('e2e skipping\nannotations: admin (check-run 42)\nThe job was not started because billing is unavailable.\n', result.stdout)
        self.assertTrue(any('check-runs/42/annotations' in call for call in calls))

    def test_annotation_failure_keeps_check_exit_one(self):
        result, _ = self.run_fixture('annotation-outage')
        self.assertEqual(result.returncode, 1)
        self.assertIn('could not fetch annotations for admin (check-run 42)', result.stderr)

    def test_annotations_for_several_failed_conclusions(self):
        result, calls = self.run_fixture('multiple-failures')
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertEqual(result.stderr, '')
        for check_id, name, message, conclusion in (
                (42, 'admin', 'The job was not started because billing is unavailable.', 'failure'),
                (43, 'cancelled-job', 'Job was cancelled.', 'cancelled'),
                (44, 'timed-out-job', 'Job timed out.', 'timed_out'),
                (45, 'startup-job', 'Job failed to start.', 'startup_failure')):
            with self.subTest(conclusion=conclusion):
                self.assertIn(f'annotations: {name} (check-run {check_id})\n{message}\n', result.stdout)
                self.assertEqual(sum(f'check-runs/{check_id}/annotations' in call for call in calls), 1)

    def test_check_runs_list_failure_keeps_check_exit_one(self):
        result, calls = self.run_fixture('check-runs-outage')
        self.assertEqual(result.returncode, 1)
        self.assertEqual(result.stderr, 'EOF\ncould not fetch failed check runs for annotations\n')
        self.assertFalse(any('/annotations' in call for call in calls))

    def test_usage(self):
        for args in ([], ['0'], ['abc'], ['1', '2']):
            with self.subTest(args=args):
                result = subprocess.run([str(SCRIPT), *args], capture_output=True, text=True)
                self.assertEqual(result.returncode, 2)
                self.assertEqual(result.stdout, '')
                self.assertEqual(result.stderr, 'usage: scripts/ci-wait.sh <pr-number>\n')


if __name__ == '__main__':
    unittest.main()

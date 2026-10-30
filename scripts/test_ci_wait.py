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
if [[ $SCENARIO == outage || ( $SCENARIO == recover && $count -le 3 ) ]]; then
  echo 'partial failed response'
  echo 'connection reset by peer' >&2
  exit 2
fi
# Three failures before each success must never accumulate across successes.
if [[ $SCENARIO == intermittent && $((count % 4)) != 0 ]]; then exit 2; fi
case "$1 $2" in
  'pr view') echo head-sha ;;
  'api repos/{owner}/{repo}/commits/head-sha') echo '2026-01-01T00:00:00Z' ;;
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
        fail) printf 'pass\\nfail\\n'; exit 1 ;;
        cancel) printf 'pass\\ncancel\\n'; exit 1 ;;
        *) printf 'pass\\npass\\nskipping\\n' ;;
      esac
    else printf 'backend pass\\nadmin pass\\ne2e pass\\n'; fi ;;
  *) exit 99 ;;
esac
''')
            (root / 'date').write_text('#!/bin/sh\nif [ "$1" = -d ]; then echo 1000; else echo 1600; fi\n')
            (root / 'sleep').write_text('#!/bin/sh\ntouch "$FIXTURE/slept"\n')
            for name in ('gh', 'date', 'sleep'):
                (root / name).chmod(0o755)
            env = dict(os.environ, PATH=str(root) + os.pathsep + os.environ['PATH'],
                       FIXTURE=str(root), SCENARIO=scenario)
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

    def test_five_consecutive_failures_exit_two(self):
        result, calls = self.run_fixture('outage')
        self.assertEqual(result.returncode, 2)
        self.assertEqual(len(calls), 5)
        self.assertEqual(result.stderr, 'connection reset by peer\n' * 5)

    def test_json_checks_empty_exit_one_stops_after_five_attempts(self):
        result, calls = self.run_fixture('json-outage')
        self.assertEqual(result.returncode, 2, result.stderr)
        self.assertEqual(result.stdout, '')
        self.assertEqual(result.stderr, 'connection reset by peer\n' * 5)
        self.assertEqual(sum(call.startswith('pr checks') and '--json' in call for call in calls), 5)
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

    def test_usage(self):
        for args in ([], ['0'], ['abc'], ['1', '2']):
            with self.subTest(args=args):
                result = subprocess.run([str(SCRIPT), *args], capture_output=True, text=True)
                self.assertEqual(result.returncode, 2)
                self.assertEqual(result.stdout, '')
                self.assertEqual(result.stderr, 'usage: scripts/ci-wait.sh <pr-number>\n')


if __name__ == '__main__':
    unittest.main()

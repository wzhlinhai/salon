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
case "$1 $2" in
  'pr view') echo head-sha ;;
  'api repos/{owner}/{repo}/commits/head-sha') echo '2026-01-01T00:00:00Z' ;;
  'run list')
    if [[ $SCENARIO == timeout ]]; then echo none
    elif [[ $SCENARIO == late && ! -f $FIXTURE/slept ]]; then echo pending
    else echo done; fi ;;
  'pr checks')
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

    def test_usage(self):
        for args in ([], ['0'], ['abc'], ['1', '2']):
            with self.subTest(args=args):
                result = subprocess.run([str(SCRIPT), *args], capture_output=True, text=True)
                self.assertEqual(result.returncode, 2)
                self.assertEqual(result.stdout, '')
                self.assertEqual(result.stderr, 'usage: scripts/ci-wait.sh <pr-number>\n')


if __name__ == '__main__':
    unittest.main()

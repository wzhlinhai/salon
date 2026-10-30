"""Gate summaries preserve file order and report incomplete records."""
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

GATE = Path(__file__).with_name('gate.py').resolve()


class SummaryTests(unittest.TestCase):
    def test_summary_order_prefix_and_interrupted(self):
        with tempfile.TemporaryDirectory() as folder:
            records = [dict(label='backend-last', exit=1, seconds=2.5),
                       dict(label='admin', exit=0, seconds=3),
                       dict(label='backend-first', seconds=0.25),
                       dict(label='backend-null', exit=None, seconds=0)]
            (Path(folder) / 'gates.jsonl').write_text(''.join(json.dumps(r) + '\n' for r in records))
            command = [sys.executable, str(GATE), '--summary', folder]
            result = subprocess.run(command, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0)
            self.assertEqual(result.stderr, '')
            lines = ['backend-last exit=1 seconds=2.5', 'admin exit=0 seconds=3',
                     'backend-first exit=interrupted seconds=0.25', 'backend-null exit=interrupted seconds=0']
            self.assertEqual(result.stdout.splitlines(), lines)
            filtered = subprocess.run(command + ['--prefix', 'backend-'], capture_output=True, text=True)
            self.assertEqual(filtered.returncode, 0)
            self.assertEqual(filtered.stdout.splitlines(), [lines[0], *lines[2:]])
            self.assertEqual(filtered.stderr, '')

    def test_summary_argument_in_gate_command_is_passed_through(self):
        with tempfile.TemporaryDirectory() as folder:
            result = subprocess.run([sys.executable, str(GATE), folder, 'passthrough', '--',
                                     sys.executable, '-c', 'import sys; print(sys.argv[1])', '--summary'],
                                    capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            record = json.loads(result.stdout)
            self.assertEqual(record['exit'], 0)
            self.assertEqual((Path(folder) / 'passthrough.log').read_text(), '--summary\n')

    def test_missing_and_empty(self):
        with tempfile.TemporaryDirectory() as folder:
            command = [sys.executable, str(GATE), '--summary', folder]
            missing = subprocess.run(command, capture_output=True, text=True)
            self.assertEqual((missing.returncode, missing.stdout, missing.stderr), (2, '', ''))
            (Path(folder) / 'gates.jsonl').touch()
            empty = subprocess.run(command, capture_output=True, text=True)
            self.assertEqual((empty.returncode, empty.stdout, empty.stderr), (0, '', ''))


if __name__ == '__main__':
    unittest.main()

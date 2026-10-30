"""Tooling tests: no Go toolchain, Docker, or network required."""
import importlib.util
import io
import os
import shutil
import signal
import sys
import time
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location('mutchecks', Path(__file__).with_name('mutchecks.py'))
mutchecks = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(mutchecks)


def git(root, *args):
    return subprocess.check_output(['git', *args], cwd=root, stderr=subprocess.STDOUT)


def init_repo(root):
    git(root, 'init', '-q')
    (root / '.gitignore').write_text('.dev/\nlogs/\n')


def stage_fixture(root):
    git(root, 'add', '.')


class ManifestTests(unittest.TestCase):
    def load(self, source):
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / 'manifest.py'
            path.write_text(source)
            return mutchecks.load_manifest(path)

    def test_allowed_forms(self):
        values = self.load('''"""Data only."""
ENTRIES = [{'name': 'first'}]
ENTRIES += [{'name': 'second'}]
ENTRIES.append({'name': 'third'})
ENTRIES.extend([{'name': 'fourth'}])
SKIPPED = [('1', 'a', 'reason')]
SKIPPED += [('1', 'b', 'reason')]
SKIPPED.append(('1', 'c', 'reason'))
SKIPPED.extend([('1', 'd', 'reason')])
''')
        self.assertEqual([e['name'] for e in values['ENTRIES']], ['first', 'second', 'third', 'fourth'])
        self.assertEqual([e[1] for e in values['SKIPPED']], ['a', 'b', 'c', 'd'])

    def test_rejects_code_and_non_literals(self):
        for statement in ('for entry in ENTRIES:\n    pass',
                          'print("executed")', 'ENTRIES = list()',
                          'ENTRIES = [unknown]', 'ENTRIES.append(dict())',
                          'ENTRIES += [unknown]', 'OTHER = []',
                          'ENTRIES[0] = {}', 'ENTRIES.extend([], bad=True)'):
            with self.subTest(statement=statement):
                with self.assertRaisesRegex(ValueError, r'manifest.py:2: only literal ENTRIES/SKIPPED statements are allowed; edit the entry in place'):
                    self.load('ENTRIES = []\n' + statement)


class PartitionTests(unittest.TestCase):
    def test_covers_each_entry_and_keeps_e2e_serial(self):
        entries = [dict(kind='go', packages=[p], name=str(i))
                   for i, p in enumerate(('a', 'b', 'a', 'c', 'd'))]
        entries.insert(1, dict(kind='e2e', name='first'))
        entries.append(dict(kind='e2e', name='last'))
        entries.append(dict(kind='go', packages=['c', 'b'], name='bridge'))
        for jobs in (1, 2, 4, 20):
            with self.subTest(jobs=jobs):
                workers, serial = mutchecks.partition_entries(entries, jobs)
                self.assertEqual([e['name'] for e in serial], ['first', 'last'])
                flattened = [e for worker in workers for e in worker] + serial
                self.assertCountEqual([id(e) for e in flattened], [id(e) for e in entries])
                sizes = [len(worker) for worker in workers] + [0] * (jobs - len(workers))
                self.assertLessEqual(max(sizes) - min(sizes), 1)
                go = [entry for entry in entries if entry['kind'] == 'go']
                for index, worker in enumerate(workers):
                    self.assertEqual(worker, go[index::jobs])

    def test_merge_uses_manifest_order_including_e2e(self):
        entries = [dict(step='127 preview batch', name=n) for n in ('e2e', 'a', 'b')]
        lines = ['127 preview batch b exit=0 PASS', '127 preview batch e2e exit=1 FAIL', '127 preview batch a exit=0 PASS']
        self.assertEqual(mutchecks.merge_results(entries, lines), [lines[1], lines[2], lines[0]])


class BaselineTests(unittest.TestCase):
    def test_commands_use_exact_selected_guard_union_per_package(self):
        entries = [dict(kind='go', name='first', step='1', packages=['a', 'b'], guards=['TestA', 'TestShared']),
                   dict(kind='go', name='second', step='1', packages=['a'], guards=['TestB', 'TestShared']),
                   dict(kind='go', name='unselected', step='1', packages=['a'], guards=['TestUnselected']),
                   dict(kind='e2e', name='browser', step='1', guards=['Ignored'])]
        selected = mutchecks.select_entries(entries, ['first', 'second', 'browser'])
        with tempfile.TemporaryDirectory() as folder:
            logs = Path(folder)
            with patch.object(mutchecks.subprocess, 'Popen') as start, patch.object(mutchecks, 'stop_processes'):
                start.return_value.poll.return_value = 0
                self.assertTrue(mutchecks.baseline(selected, logs, io.StringIO()))
            self.assertEqual([call.args[0] for call in start.call_args_list], [
                ['go', 'test', '-count=1', '-run', '^(TestA|TestShared|TestB)$', 'a'],
                ['go', 'test', '-count=1', '-run', '^(TestA|TestShared)$', 'b']])
            self.assertTrue(all(call.kwargs['cwd'] == mutchecks.ROOT / 'backend' for call in start.call_args_list))


    def test_failure_cancels_running_baselines_without_starting_queued_packages(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            (root / 'backend').mkdir()
            (root / 'bin').mkdir()
            logs = root / 'logs'
            logs.mkdir()
            go = root / 'bin/go'
            go.write_text('#!' + sys.executable + '\n' + "import pathlib, sys, time\nlogs = pathlib.Path(__file__).resolve().parent.parent / 'logs'\nname = sys.argv[-1]\n(logs / f'started-{name}').touch()\nif name == 'a':\n    deadline = time.monotonic() + 5\n    while not (logs / 'started-b').exists():\n        assert time.monotonic() < deadline\n        time.sleep(0.01)\n    print('baseline fixture failed', flush=True)\n    sys.exit(1)\ntime.sleep(30)\n")
            go.chmod(0o755)
            entries = [dict(kind='go', packages=[name], guards=['TestGuard']) for name in ('a', 'b', 'c')]
            env = dict(os.environ, PATH=str(root / 'bin') + os.pathsep + os.environ['PATH'])
            started = []
            popen = subprocess.Popen

            def start(*args, **kwargs):
                process = popen(*args, **kwargs)
                started.append(process)
                return process

            with patch.object(mutchecks, 'ROOT', root), patch.dict(os.environ, env, clear=True), patch.object(mutchecks.subprocess, 'Popen', side_effect=start), patch('sys.stderr', new=io.StringIO()) as error:
                results = io.StringIO()
                self.assertFalse(mutchecks.baseline(entries, logs, results, jobs=2))
            self.assertTrue((logs / 'started-b').exists())
            self.assertFalse((logs / 'started-c').exists())
            self.assertEqual(len(started), 2)
            self.assertTrue(all(process.poll() is not None for process in started))
            self.assertEqual(results.getvalue(), 'baseline a exit=1 FAIL\n')
            self.assertEqual(error.getvalue(), 'baseline fixture failed\n')



class WorkerTests(unittest.TestCase):
    def test_copy_reproduces_files_with_only_read_only_git_commands(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder) / 'source'
            root.mkdir()
            init_repo(root)
            for name in ('modified', 'deleted', 'staged-deleted'):
                (root / name).write_bytes(b'original\r\n')
            (root / '.gitignore').write_text('ignored\n')
            stage_fixture(root)
            (root / 'modified').write_bytes(b'changed\x00\r\n')
            (root / 'modified').chmod(0o755)
            (root / 'deleted').unlink()
            git(root, 'rm', '-f', 'staged-deleted')
            (root / 'untracked').write_bytes(b'new\xff')
            (root / 'ignored').write_bytes(b'not copied')
            (root / 'link').symlink_to('modified')
            worker = Path(folder) / 'worker'
            with patch.object(mutchecks.subprocess, 'check_output', wraps=subprocess.check_output) as commands:
                mutchecks.copy_working_tree(root, worker)
            self.assertEqual([call.args[0] for call in commands.call_args_list], [
                ['git', 'ls-files', '-z', '--cached', '--others', '--exclude-standard']])
            self.assertFalse((worker / '.git').exists())
            for name in ('modified', 'untracked'):
                self.assertEqual((worker / name).read_bytes(), (root / name).read_bytes())
            self.assertEqual((worker / 'modified').stat().st_mode, (root / 'modified').stat().st_mode)
            self.assertEqual(os.readlink(worker / 'link'), 'modified')
            for name in ('deleted', 'staged-deleted', 'ignored'):
                self.assertFalse((worker / name).exists(), name)

    def test_cleanup_removes_worker_copies_and_is_idempotent(self):
        with tempfile.TemporaryDirectory() as folder:
            workers = Path(folder) / '.dev/mutcheck-worktrees'
            (workers / '0').mkdir(parents=True)
            (workers / '0/file').write_text('mutation')
            mutchecks.cleanup_workers(workers)
            mutchecks.cleanup_workers(workers)
            self.assertFalse(workers.exists())

    def test_baseline_failure_never_copies_or_starts_workers(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            logs = root / 'logs'
            logs.mkdir()
            stale = root / '.dev/mutcheck-worktrees/0'
            stale.mkdir(parents=True)
            (stale / 'file').write_text('stale')
            with patch.object(mutchecks, 'ROOT', root), patch.object(mutchecks, 'baseline', return_value=False) as baseline, patch.object(mutchecks, 'copy_working_tree') as copy:
                self.assertEqual(mutchecks.run_parallel([], root / 'manifest.py', 4, logs, io.StringIO()), 1)
            baseline.assert_called_once()
            copy.assert_not_called()
            self.assertFalse(stale.parent.exists())

    def test_baselines_overlap_within_limit_and_all_finish_before_mutations(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            init_repo(root)
            (root / 'guard').write_text('original')
            stage_fixture(root)
            (root / 'guard').write_text('modified')
            script = root / 'backend/scripts/mutchecks.py'
            script.parent.mkdir(parents=True)
            script.write_text("import pathlib, sys\nlogs = pathlib.Path(sys.argv[sys.argv.index('--worker-logs') + 1])\nnames = sys.argv[sys.argv.index('--worker-logs') + 2:]\nassert pathlib.Path('guard').read_text() == 'modified'\nassert not pathlib.Path('.git').exists()\nassert pathlib.Path(sys.argv[1]) == logs.parent.parent / 'manifest.py'\nassert all((logs.parent / f'finished-{p}').exists() for p in ('a', 'b', 'c'))\nlines = []\nfor selected in names:\n    name = selected.split('/')[1]\n    lines.append(f'1 {name} exit=0 caught=TestGuard restored=True PASS')\n    (logs / f'1-{name}.log').write_text('mutation log')\n(logs / 'results.log').write_text('\\n'.join(lines) + '\\n')\n")
            (root / 'bin').mkdir()
            go = root / 'bin/go'
            go.write_text('#!' + sys.executable + '\n' + "import pathlib, sys, time\nlogs = pathlib.Path(__file__).resolve().parent.parent / 'logs'\nname = sys.argv[-1]\n(logs / f'started-{name}').touch()\ndeadline = time.monotonic() + 5\n\ndef wait_for(marker):\n    while not (logs / marker).exists():\n        assert time.monotonic() < deadline, f'timed out waiting for {marker}'\n        time.sleep(0.01)\n\nif name == 'a':\n    wait_for('started-b')\nelif name == 'b':\n    wait_for('started-a')\n    wait_for('release-b')\nelif name == 'c':\n    assert (logs / 'finished-a').exists(), 'more than two baselines active'\n    assert not (logs / 'finished-b').exists()\n    (logs / 'release-b').touch()\n(logs / f'finished-{name}').touch()\n")
            go.chmod(0o755)
            manifest = root / 'manifest.py'
            manifest.write_text('ENTRIES = []')
            logs = root / 'logs'
            logs.mkdir()
            entries = [dict(kind='e2e', step='1', name='browser', file='guard'),
                       *[dict(kind='go', step='1', name=n, file='guard', packages=[n], guards=['TestGuard']) for n in ('a', 'b', 'c')]]

            def e2e(entry, output_dir, output):
                self.assertEqual(mutchecks.ROOT, root)
                for name in ('a', 'b', 'c'):
                    self.assertTrue((output_dir / f'1-{name}.log').exists())
                mutchecks.result_line('1 browser exit=1/0 caught=Guard restored=True PASS', output)
                return True, True

            env = dict(os.environ, PATH=str(root / 'bin') + os.pathsep + os.environ['PATH'])
            with patch.object(mutchecks, 'ROOT', root), patch.dict(os.environ, env, clear=True), patch.object(mutchecks, 'run_e2e', side_effect=e2e), patch('sys.stdout', new=io.StringIO()):
                results = io.StringIO()
                self.assertEqual(mutchecks.run_parallel(entries, manifest, 2, logs, results), 0)
            self.assertEqual(results.getvalue().splitlines(), [
                'baseline a exit=0 PASS', 'baseline b exit=0 PASS', 'baseline c exit=0 PASS',
                '1 browser exit=1/0 caught=Guard restored=True PASS',
                '1 a exit=0 caught=TestGuard restored=True PASS',
                '1 b exit=0 caught=TestGuard restored=True PASS',
                '1 c exit=0 caught=TestGuard restored=True PASS'])
            self.assertFalse((root / '.dev/mutcheck-worktrees').exists())
            self.assertEqual((root / 'guard').read_text(), 'modified')

    def test_signals_remove_worker_copies_and_leave_main_file_untouched(self):
        for sig in (signal.SIGINT, signal.SIGTERM):
            with self.subTest(signal=sig), tempfile.TemporaryDirectory() as folder:
                root = Path(folder)
                init_repo(root)
                script = root / 'backend/scripts/mutchecks.py'
                script.parent.mkdir(parents=True)
                shutil.copy2(Path(mutchecks.__file__), script)
                (root / 'backend/scripts/mutcheck.sh').write_text(
                    '#!/bin/bash\nprintf mutated > "$3"\nmkdir -p .dev\ntouch .dev/ready\nsleep 30\n')
                (root / 'backend/scripts/mutcheck.sh').chmod(0o755)
                (root / 'guard').write_text('original')
                stage_fixture(root)
                (root / 'bin').mkdir()
                (root / 'bin/go').write_text('#!/bin/sh\nexit 0\n')
                (root / 'bin/go').chmod(0o755)
                entry = dict(kind='go', step='1', name='guard', file='guard',
                             original='original', replacement='mutated', packages=['a'], guards=['TestGuard'])
                manifest = root / 'manifest.py'
                manifest.write_text('ENTRIES = ' + repr([entry]))
                env = dict(os.environ, PATH=str(root / 'bin') + os.pathsep + os.environ['PATH'])
                process = subprocess.Popen([sys.executable, str(script), str(manifest), '--jobs', '4'],
                                           cwd=root, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                           start_new_session=True)
                try:
                    ready = root / '.dev/mutcheck-worktrees/0/.dev/ready'
                    deadline = time.monotonic() + 10
                    while not ready.exists() and process.poll() is None and time.monotonic() < deadline:
                        time.sleep(0.02)
                    self.assertTrue(ready.exists(), 'fixture never reached mutation')
                    self.assertEqual((root / '.dev/mutcheck-worktrees/0/guard').read_text(), 'mutated')
                    process.send_signal(sig)
                    output, error = process.communicate(timeout=15)
                    self.assertEqual(process.returncode, 130, (output, error))
                    self.assertFalse((root / '.dev/mutcheck-worktrees').exists())
                    self.assertEqual((root / 'guard').read_text(), 'original')
                finally:
                    if process.poll() is None:
                        process.kill()
                    process.communicate()


if __name__ == '__main__':
    unittest.main()

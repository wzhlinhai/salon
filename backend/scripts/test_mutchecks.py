"""Tooling tests: no Go toolchain, Docker, or network required."""
import importlib.util
import io
import json
import os
import shutil
import signal
import sys
import time
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import Mock, patch

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


class AllManifestTests(unittest.TestCase):
    def invoke(self, root, *args):
        with patch.object(mutchecks, 'ROOT', root), patch.object(sys, 'argv', ['mutchecks.py', *args]), patch('sys.stdout', new=io.StringIO()) as output, patch('sys.stderr', new=io.StringIO()) as error:
            code = mutchecks.main()
        return code, output.getvalue(), error.getvalue()

    def test_requires_since_for_real_runs_and_rejects_names(self):
        for args in ([], ['--jobs', '2'], ['--since', 'main', 'unknown'],
                     ['--since', 'main', 'unknown', 'other']):
            with self.subTest(args=args), tempfile.TemporaryDirectory() as folder:
                with self.assertRaises(SystemExit) as raised:
                    self.invoke(Path(folder), *args)
                self.assertEqual(raised.exception.code, 2)

    def test_runs_selected_entries_in_path_order_with_separate_logs(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            manifests = []
            for name in ('b', 'a', 'empty'):
                manifest = root / '.dev/codex' / name / 'manifest.py'
                manifest.parent.mkdir(parents=True)
                entries = [dict(kind='go', step='1', name=name, file='guard', guards=['TestGuard'], packages=['a']),
                           dict(kind='go', step='1', name='unselected')]
                manifest.write_text('ENTRIES = ' + repr(entries))
                manifests.append(manifest)
            seen = []

            def select(entries, ref):
                self.assertEqual(ref, 'main')
                return [] if entries[0]['name'] == 'empty' else entries[:1]

            def run(groups, jobs):
                self.assertEqual(jobs, 8)
                for entries, manifest, logs, results in groups:
                    self.assertEqual(logs.parent, manifest.parent)
                    seen.append(manifest)
                    status = 'PASS' if manifest.parent.name == 'b' else 'FAIL'
                    mutchecks.result_line(f"1 {entries[0]['name']} exit=0 {status}", results)
                return 1

            with patch.object(mutchecks, 'affected_entries', side_effect=select), patch.object(mutchecks, 'run_queue', side_effect=run):
                code, output, error = self.invoke(root, '--since', 'main', '--jobs', '8')
            self.assertEqual(code, 1)
            self.assertEqual(seen, sorted(manifests[:2]))
            self.assertEqual(output.splitlines()[-1], 'total selected=2 passed=1 failed=1 environment=0')
            self.assertIn('.dev/codex/empty/manifest.py selected=0 passed=0 failed=0', output)
            self.assertEqual(error.count('logs='), 2)
            self.assertNotIn('1/unselected', error)

    def single_manifest_fixture(self, worker_exit):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            init_repo(root)
            (root / 'guard').write_text('original')
            script = root / 'backend/scripts/mutchecks.py'
            script.parent.mkdir(parents=True)
            script.write_text(
                "import pathlib, sys\n"
                "logs = pathlib.Path(sys.argv[sys.argv.index('--worker-logs') + 1])\n"
                "(logs / 'results.log').write_text('1 guard exit=0 caught=TestGuard restored=True PASS\\n')\n"
                f"pathlib.Path({str(root / 'go-finished')!r}).touch()\n"
                f"sys.exit({worker_exit})\n")
            stage_fixture(root)
            manifest = root / '.dev/codex/a/manifest.py'
            manifest.parent.mkdir(parents=True)
            entries = [dict(kind='e2e', step='1', name='browser', file='guard'),
                       dict(kind='go', step='1', name='guard', file='guard',
                            guards=['TestGuard'], packages=['a'])]
            manifest.write_text('ENTRIES = ' + repr(entries))

            def e2e(entry, logs, results):
                self.assertTrue((root / 'go-finished').exists(), 'e2e ran before Go')
                mutchecks.result_line('1 browser exit=1/0 caught=Guard restored=True PASS', results)
                return True, True

            with patch.object(mutchecks, 'affected_entries', side_effect=lambda entries, ref: entries), patch.object(mutchecks, 'baseline', return_value=True) as baseline, patch.object(mutchecks, 'run_e2e', side_effect=e2e):
                code, output, error = self.invoke(root, '--since', 'main', '--jobs', '1')
            baseline.assert_called_once()
            logs = next(manifest.parent.glob('mutchecks-*'))
            return code, output, error, (logs / 'results.log').read_text()

    def test_single_manifest_jobs_one_runs_go_before_earlier_e2e(self):
        code, output, _, _ = self.single_manifest_fixture(0)
        self.assertEqual(code, 0, output)
        self.assertIn('total selected=2 passed=2 failed=0 environment=0', output)

    def test_worker_pass_then_nonzero_exit_is_counted_as_failed(self):
        code, output, error, results = self.single_manifest_fixture(1)
        self.assertEqual(code, 1, output)
        self.assertIn('.dev/codex/a/manifest.py selected=2 passed=1 failed=1 environment=0', output)
        self.assertIn('total selected=2 passed=1 failed=1 environment=0', output)
        self.assertIn('worker exited 1 after reporting PASS', error)
        self.assertIn('1 guard exit=worker-1', results)
        self.assertIn('worker.log FAIL', results)
        self.assertNotIn('1 guard exit=0', results)

    def test_union_baseline_failure_blocks_all_manifests(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            entries = [dict(kind='go', step='1', name='guard', guards=['TestGuard'], packages=['a'])]
            for name in ('a', 'b'):
                manifest = root / '.dev/codex' / name / 'manifest.py'
                manifest.parent.mkdir(parents=True)
                manifest.write_text('ENTRIES = ' + repr(entries))
            with patch.object(mutchecks, 'affected_entries', return_value=entries), patch.object(mutchecks, 'baseline', return_value=False) as baseline, patch.object(mutchecks, 'run_go') as mutate:
                code, output, _ = self.invoke(root, '--since', 'main')
            self.assertEqual(baseline.call_count, 1)
            mutate.assert_not_called()
            self.assertEqual(code, 1)
            self.assertIn('total selected=2 passed=0 failed=2', output)

    def test_locate_and_broken_manifest_continue_and_report_totals(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            for name, source in [('a', 'ENTRIES = missing'), ('b', "ENTRIES = [{'step':'1', 'name':'guard'}]")]:
                manifest = root / '.dev/codex' / name / 'manifest.py'
                manifest.parent.mkdir(parents=True)
                manifest.write_text(source)
            with patch.object(mutchecks, 'locate') as locate:
                code, output, error = self.invoke(root, '--locate')
            locate.assert_called_once_with(dict(step='1', name='guard'))
            self.assertEqual(code, 1)
            self.assertIn('total selected=1 passed=1 failed=1', output)
            self.assertIn('only literal', error)


class PartitionTests(unittest.TestCase):
    def test_covers_each_entry_and_keeps_e2e_serial(self):
        entries = [dict(kind='go', packages=[p], name=str(i))
                   for i, p in enumerate(('a', 'b', 'a', 'c', 'd'))]
        entries.insert(1, dict(kind='e2e', name='first'))
        entries.append(dict(kind='e2e', name='last'))
        entries.append(dict(kind='go', packages=['c', 'b'], name='bridge'))
        go, serial = mutchecks.partition_entries(entries)
        self.assertEqual([e['name'] for e in serial], ['first', 'last'])
        self.assertCountEqual([id(e) for e in go + serial], [id(e) for e in entries])
        self.assertEqual(go, sorted([e for e in entries if e['kind'] == 'go'], key=lambda e: tuple(e['packages'])))

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

    def test_cross_manifest_queue_reuses_free_worker_and_counts_results(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            init_repo(root)
            script = root / 'backend/scripts/mutchecks.py'
            script.parent.mkdir(parents=True)
            script.write_text(Path(mutchecks.__file__).read_text())
            (root / 'guard').write_text('original')
            stage_fixture(root)
            (root / 'backend/scripts/mutcheck.sh').write_text(
                '#!' + sys.executable + '\n' +
                "import pathlib, sys, time\n"
                "root = pathlib.Path(sys.argv[-1])\n"
                "name = sys.argv[5]\n"
                "if name == 'long':\n"
                "    deadline = time.monotonic() + 5\n"
                "    while not (root / 'next-finished').exists():\n"
                "        assert time.monotonic() < deadline, 'queue stalled'\n"
                "        time.sleep(0.01)\n"
                "with (root / 'finished').open('a') as out: out.write(name + '\\n')\n"
                "if name == 'next': (root / 'next-finished').touch()\n"
                "if name == 'short': sys.exit(4)\n"
                "if name == 'next': sys.exit(1)\n"
                "print('caught by:\\n--- FAIL: TestGuard (0.00s)')\n")
            (root / 'backend/scripts/mutcheck.sh').chmod(0o755)
            # Both manifests deliberately use identical step/name pairs.
            for manifest_name, replacements in [('a', ['long']), ('b', ['short', 'next'])]:
                manifest = root / '.dev/codex' / manifest_name / 'manifest.py'
                manifest.parent.mkdir(parents=True)
                entries = [dict(kind='go', step='1', name=str(i), file='guard',
                                original='original', replacement=value, packages=[str(root)], guards=['TestGuard'])
                           for i, value in enumerate(replacements)]
                manifest.write_text('ENTRIES = ' + repr(entries))
            with patch.object(mutchecks, 'affected_entries', side_effect=lambda entries, ref: entries), patch.object(mutchecks, 'baseline', return_value=True) as baseline:
                code, output, _ = AllManifestTests().invoke(root, '--since', 'main', '--jobs', '2')
            self.assertEqual(code, 1, output)
            baseline.assert_called_once()
            self.assertEqual(len(baseline.call_args.args[0]), 3)
            self.assertEqual((root / 'finished').read_text().splitlines(), ['short', 'next', 'long'])
            self.assertIn('.dev/codex/a/manifest.py selected=1 passed=1 failed=0 environment=0', output)
            self.assertIn('.dev/codex/b/manifest.py selected=2 passed=0 failed=1 environment=1', output)
            self.assertIn('total selected=3 passed=1 failed=1 environment=1', output)
            for manifest_name in ('a', 'b'):
                logs = next((root / '.dev/codex' / manifest_name).glob('mutchecks-*'))
                self.assertTrue((logs / '1-0.log').exists())
            self.assertFalse((root / '.dev/mutcheck-worktrees').exists())

    def test_signals_remove_worker_copies_and_leave_main_file_untouched(self):
        for sig in (signal.SIGINT, signal.SIGTERM):
            with self.subTest(signal=sig), tempfile.TemporaryDirectory() as folder:
                root = Path(folder)
                init_repo(root)
                script = root / 'backend/scripts/mutchecks.py'
                script.parent.mkdir(parents=True)
                script.write_text(Path(mutchecks.__file__).read_text().replace('selected = affected_entries(selected, args.since)', 'selected = selected'))
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
                manifest = root / '.dev/codex/a/manifest.py'
                manifest.parent.mkdir(parents=True)
                manifest.write_text('ENTRIES = ' + repr([entry]))
                second = root / '.dev/codex/b/manifest.py'
                second.parent.mkdir(parents=True)
                second.write_text('ENTRIES = ' + repr([entry]))
                env = dict(os.environ, PATH=str(root / 'bin') + os.pathsep + os.environ['PATH'])
                process = subprocess.Popen([sys.executable, str(script), '--since', 'HEAD', '--jobs', '4'],
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


class SymlinkQueryTests(unittest.TestCase):
    def test_shell_recognises_query_through_symlink(self):
        with tempfile.TemporaryDirectory() as folder:
            base = Path(folder)
            root = base / 'repo'
            scripts = root / 'backend/scripts'
            scripts.mkdir(parents=True)
            shutil.copy(Path(__file__).with_name('mutcheck.sh'), scripts / 'mutcheck.sh')
            query = root / 'backend/internal/db/queries/example.sql'
            query.parent.mkdir(parents=True)
            query.write_text('SELECT 1;')
            link = base / 'linked'
            link.symlink_to(root, target_is_directory=True)
            bin_dir = base / 'bin'
            bin_dir.mkdir()
            calls = base / 'calls'
            go = bin_dir / 'go'
            go.write_text("#!/bin/bash\necho \"$*\" >> \"$CALLS\"\nif [[ $* == *\"-run ^$\"* || $1 == tool ]]; then exit 0; fi\necho '--- FAIL: TestQuery (0.00s)'\nexit 1\n")
            go.chmod(0o755)
            result = subprocess.run(['bash', str(link / 'backend/scripts/mutcheck.sh'),
                                     str(link / 'backend/internal/db/queries/example.sql'),
                                     'SELECT 1;', 'SELECT 2;', './internal/db'],
                                    env={**os.environ, 'PATH': str(bin_dir) + os.pathsep + os.environ['PATH'], 'CALLS': str(calls)},
                                    text=True, capture_output=True)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertEqual(calls.read_text().splitlines().count('tool sqlc generate'), 2)
            self.assertEqual(query.read_text(), 'SELECT 1;')


class EnvironmentTests(unittest.TestCase):
    def test_baseline_retries_only_environment_package_once(self):
        for persistent in (False, True):
            with self.subTest(persistent=persistent), tempfile.TemporaryDirectory() as folder:
                root = Path(folder)
                (root / 'backend').mkdir()
                (root / 'bin').mkdir()
                logs = root / 'logs'
                logs.mkdir()
                go = root / 'bin/go'
                go.write_text('#!' + sys.executable + '\n' +
                              "import pathlib, sys\n"
                              "root = pathlib.Path(__file__).resolve().parent.parent\n"
                              "marker = root / ('attempts-' + sys.argv[-1])\n"
                              "n = int(marker.read_text()) if marker.exists() else 0\n"
                              "marker.write_text(str(n + 1))\n" +
                              f"if sys.argv[-1] == 'a' and (n == 0 or {persistent!r}):\n" +
                              f"    print({mutchecks.ENVIRONMENT_SIGNATURE!r})\n"
                              "    print('--- FAIL: TestGuard (0.00s)')\n"
                              "    sys.exit(1)\n")
                go.chmod(0o755)
                entries = [dict(kind='go', packages=[name], guards=['TestGuard']) for name in ('a', 'b')]
                with patch.object(mutchecks, 'ROOT', root), patch.dict(os.environ, PATH=str(root / 'bin') + os.pathsep + os.environ['PATH']), patch('sys.stderr', new=io.StringIO()):
                    results = io.StringIO()
                    self.assertEqual(mutchecks.baseline(entries, logs, results), not persistent)
                self.assertEqual((root / 'attempts-a').read_text(), '2')
                self.assertIn('baseline a environment failure; retrying once', results.getvalue())
                self.assertIn('baseline a exit=4 not run (environment)' if persistent else 'baseline a exit=0 PASS', results.getvalue())
                self.assertIn(mutchecks.ENVIRONMENT_SIGNATURE, (logs / 'baseline-0.log').read_text())
                if not persistent:
                    self.assertEqual((root / 'attempts-b').read_text(), '1')

    def test_baseline_retry_launch_error_is_terminal_and_summarized(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            manifest = root / '.dev/codex/a/manifest.py'
            manifest.parent.mkdir(parents=True)
            entry = dict(kind='go', step='1', name='guard', guards=['TestGuard'], packages=['a'])
            manifest.write_text('ENTRIES = ' + repr([entry]))
            process = Mock()
            process.poll.return_value = 1
            handles = []

            def start(command, **kwargs):
                handles.append(kwargs['stdout'])
                if len(handles) == 2:
                    raise OSError('retry launch failed')
                kwargs['stdout'].write(mutchecks.ENVIRONMENT_SIGNATURE + '\n')
                kwargs['stdout'].flush()
                return process

            with patch.object(mutchecks, 'affected_entries', return_value=[entry]), patch.object(mutchecks.subprocess, 'Popen', side_effect=start), patch.object(mutchecks, 'stop_processes') as stop, patch.object(mutchecks, 'run_go') as mutate:
                code, output, error = AllManifestTests().invoke(root, '--since', 'main')
            mutate.assert_not_called()
            self.assertEqual([call.args[0] for call in stop.call_args_list], [[process], []])
            self.assertTrue(all(handle.closed for handle in handles))
            self.assertEqual(code, 1)
            self.assertIn('baseline a exit=2 FAIL', output)
            self.assertIn('retry launch failed', error)
            self.assertIn('total selected=1 passed=0 failed=1 environment=0', output)
            logs = next(manifest.parent.glob('mutchecks-*'))
            self.assertIn(mutchecks.ENVIRONMENT_SIGNATURE, (logs / 'baseline-0.log').read_text())
            self.assertEqual((logs / 'baseline-0-retry.log').read_text(), 'retry launch failed\n')
            self.assertEqual(json.loads((logs / 'baseline-outcomes.json').read_text()), {'a': {'code': 2, 'environment': False}})

    def test_mixed_baseline_outcomes_count_ordinary_failure_per_entry(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            manifest = root / '.dev/codex/a/manifest.py'
            manifest.parent.mkdir(parents=True)
            entries = [dict(kind='go', step='1', name=name, guards=['TestGuard'], packages=packages)
                       for name, packages in [('environment', ['a']), ('ordinary', ['b']), ('both', ['a', 'b'])]]
            manifest.write_text('ENTRIES = ' + repr(entries))
            processes = []

            def start(command, **kwargs):
                process = Mock()
                if command[-1] == 'a':
                    kwargs['stdout'].write(mutchecks.ENVIRONMENT_SIGNATURE + '\n')
                    process.poll.return_value = 1
                else:
                    kwargs['stdout'].write('ordinary failure\n')
                    process.poll.side_effect = [None, 1]
                kwargs['stdout'].flush()
                processes.append(process)
                return process

            with patch.object(mutchecks, 'affected_entries', return_value=entries), patch.object(mutchecks.subprocess, 'Popen', side_effect=start), patch.object(mutchecks, 'stop_processes'), patch.object(mutchecks, 'run_go') as mutate:
                code, output, _ = AllManifestTests().invoke(root, '--since', 'main', '--jobs', '2')
            mutate.assert_not_called()
            self.assertEqual(len(processes), 3)
            self.assertEqual(code, 1)
            self.assertIn('baseline a exit=4 not run (environment)', output)
            self.assertIn('baseline b exit=1 FAIL', output)
            self.assertIn('.dev/codex/a/manifest.py selected=3 passed=0 failed=2 environment=1', output)
            self.assertIn('total selected=3 passed=0 failed=2 environment=1', output)

    def test_entry_environment_never_parses_caught_and_has_separate_counts(self):
        for code, signature in ((0, True), (4, True), (4, False)):
            with self.subTest(code=code, signature=signature), tempfile.TemporaryDirectory() as folder:
                root = Path(folder)
                init_repo(root)
                scripts = root / 'backend/scripts'
                scripts.mkdir(parents=True)
                (scripts / 'mutchecks.py').write_text(Path(mutchecks.__file__).read_text())
                worker = scripts / 'mutcheck.sh'
                worker.write_text(
                    '#!' + sys.executable + '\nimport sys\n' +
                    f'print({mutchecks.ENVIRONMENT_SIGNATURE!r})\n' * signature +
                    "print('caught by:\\n--- FAIL: TestGuard (0.00s)')\n" +
                    f'sys.exit({code})\n')
                worker.chmod(0o755)
                (root / 'guard').write_text('original')
                stage_fixture(root)
                manifest = root / '.dev/codex/a/manifest.py'
                manifest.parent.mkdir(parents=True)
                entry = dict(kind='go', step='1', name='guard', file='guard', guards=['TestGuard'], packages=['a'], original='original', replacement='mutated')
                manifest.write_text('ENTRIES = ' + repr([entry]))

                with patch.object(mutchecks, 'affected_entries', return_value=[entry]), patch.object(mutchecks, 'baseline', return_value=True):
                    status, output, _ = AllManifestTests().invoke(root, '--since', 'main')
                self.assertEqual(status, 1)
                self.assertIn('exit=4 caught=- restored=True not run (environment)', output)
                self.assertIn('.dev/codex/a/manifest.py selected=1 passed=0 failed=0 environment=1', output)
                self.assertIn('total selected=1 passed=0 failed=0 environment=1', output)

    def test_baseline_environment_counts_whole_unrun_manifest(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            manifest = root / '.dev/codex/a/manifest.py'
            manifest.parent.mkdir(parents=True)
            entries = [dict(kind='go', step='1', name=name, guards=['TestGuard'], packages=['a']) for name in ('one', 'two')]
            manifest.write_text('ENTRIES = ' + repr(entries))

            def baseline(entries, logs, results):
                mutchecks.result_line('baseline a exit=4 not run (environment)', results)
                (logs / 'baseline-outcomes.json').write_text(json.dumps({'a': {'code': 4, 'environment': True}}))
                return False

            with patch.object(mutchecks, 'affected_entries', return_value=entries), patch.object(mutchecks, 'baseline', side_effect=baseline), patch.object(mutchecks, 'run_go') as mutate:
                code, output, _ = AllManifestTests().invoke(root, '--since', 'main')
            mutate.assert_not_called()
            self.assertEqual(code, 1)
            self.assertIn('total selected=2 passed=0 failed=0 environment=2', output)

    def test_shell_retries_and_restores_without_counting_environment_as_caught(self):
        for outcome, expected in (('environment', 4), ('caught', 0), ('passed', 1)):
            with self.subTest(outcome=outcome), tempfile.TemporaryDirectory() as folder:
                root = Path(folder)
                scripts = root / 'backend/scripts'
                scripts.mkdir(parents=True)
                shutil.copy(Path(__file__).with_name('mutcheck.sh'), scripts / 'mutcheck.sh')
                target = root / 'guard.go'
                target.write_text('original')
                (root / 'bin').mkdir()
                go = root / 'bin/go'
                go.write_text('#!' + sys.executable + '\n' +
                              "import pathlib, sys\n"
                              "if '^$' in sys.argv: sys.exit(0)\n"
                              "marker = pathlib.Path(__file__).resolve().parent.parent / 'attempts'\n"
                              "n = int(marker.read_text()) if marker.exists() else 0\n"
                              "marker.write_text(str(n + 1))\n" +
                              f"if n == 0 or {outcome == 'environment'!r}:\n" +
                              f"    print({mutchecks.ENVIRONMENT_SIGNATURE!r})\n"
                              "    print('--- FAIL: TestGuard (0.00s)')\n"
                              "    sys.exit(1)\n" +
                              f"if {outcome == 'caught'!r}:\n"
                              "    print('--- FAIL: TestGuard (0.00s)')\n"
                              "    sys.exit(1)\n")
                go.chmod(0o755)
                result = subprocess.run(['bash', str(scripts / 'mutcheck.sh'), str(target), 'original', 'mutated', './internal/example'],
                                        env={**os.environ, 'PATH': str(root / 'bin') + os.pathsep + os.environ['PATH']}, text=True, capture_output=True)
                content = result.stdout + result.stderr
                self.assertEqual(result.returncode, expected, content)
                self.assertEqual((root / 'attempts').read_text(), '2')
                self.assertEqual(target.read_text(), 'original')
                self.assertFalse((root / '.dev/mutcheck-backup').exists())
                self.assertEqual('caught by:' in content, outcome == 'caught')
                self.assertIn('retrying test run once', content)
                if outcome == 'environment':
                    self.assertIn('not run (environment)', content)
                else:
                    self.assertNotIn(mutchecks.ENVIRONMENT_SIGNATURE, content)


if __name__ == '__main__':
    unittest.main()

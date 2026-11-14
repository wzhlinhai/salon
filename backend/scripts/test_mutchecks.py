"""Tooling tests: no Go toolchain, Docker, or network required."""
import importlib.util
import io
import json
import os
import shutil
import signal
import socket
import sys
import time
from pathlib import Path
import subprocess
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import MagicMock, Mock, patch

SPEC = importlib.util.spec_from_file_location('mutchecks', Path(__file__).with_name('mutchecks.py'))
mutchecks = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(mutchecks)

TEST_TEMP_ROOT = None


class FakeSharedServers:
    """Record worker servers without starting Docker in tooling tests."""

    def __init__(self, count):
        self.dsns = [f'postgres://test/{index}' for index in range(count)]

    def start(self, timeout=60):
        return self

    def dsn(self, index):
        return self.dsns[index % len(self.dsns)]

    def close(self):
        pass


def setUpModule():
    global TEST_TEMP_ROOT
    base = mutchecks.ROOT / '.dev'
    base.mkdir(parents=True, exist_ok=True)
    TEST_TEMP_ROOT = tempfile.mkdtemp(prefix='test-mutchecks-', dir=base)
    # Worker subprocesses check the real filesystem; /tmp is a small tmpfs here.
    tempfile.tempdir = TEST_TEMP_ROOT


def tearDownModule():
    tempfile.tempdir = None
    if TEST_TEMP_ROOT is not None:
        shutil.rmtree(TEST_TEMP_ROOT, ignore_errors=True)


def relaxed_disk_headroom():
    """Let subprocess tests run on CI filesystems with less than 20 GB free."""
    return patch.dict(os.environ, {mutchecks.DISK_HEADROOM_ENV: '1'})


def git(root, *args):
    return subprocess.check_output(['git', *args], cwd=root, stderr=subprocess.STDOUT)


def init_repo(root):
    git(root, 'init', '-q')
    (root / '.gitignore').write_text('.dev/\nlogs/\n')


def stage_fixture(root):
    git(root, 'add', '.')


def commit_fixture(root):
    stage_fixture(root)
    subprocess.check_output(
        ['git', '-c', 'user.email=test@example.com', '-c', 'user.name=Test', 'commit', '-q', '-m', 'initial'],
        cwd=root)


def write(path, content):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content)


def bin_dir_env(binary):
    return {'PATH': str(binary) + os.pathsep + os.environ['PATH']}


def migration_entry(name, file, original):
    return dict(kind='go', step='1', name=name, file=file, original=original,
                replacement=original + ' -- mutated', guards=['TestGuard'], packages=['a'])


def query_entry(name, file, original):
    return migration_entry(name, file, original)


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


class ManifestEditTests(unittest.TestCase):
    def setUp(self):
        self.folder = tempfile.TemporaryDirectory()
        self.addCleanup(self.folder.cleanup)
        self.root = Path(self.folder.name)
        self.path = self.root / 'manifest.py'
        self.value = self.root / 'value'
        (self.root / 'guard').write_text('原文\n')
        self.entry = dict(kind='go', step='1', name='guard', file='guard',
                          original='原文', replacement='changed', guards=['TestGuard'], packages=['a'])

    def invoke(self, *args):
        return AllManifestTests().invoke(self.root, *args)

    def test_set_preserves_other_bytes_in_each_literal_form(self):
        for form in ('ENTRIES = [{entry}]', 'ENTRIES = []\nENTRIES += [{entry}]',
                     'ENTRIES = []\nENTRIES.append({entry})',
                     'ENTRIES = []\nENTRIES.extend([{entry}])'):
            with self.subTest(form=form):
                source = ('# 中文 comment\r\n' + form.format(entry=repr(self.entry)) + ' # keep\r\n').encode()
                self.path.write_bytes(source)
                self.value.write_text('new replacement\n')
                code, _, error = self.invoke('--set', str(self.path), 'guard', 'replacement', str(self.value))
                self.assertEqual(code, 0, error)
                self.assertEqual(self.path.read_bytes(), source.replace(b"'changed'", b"'new replacement'"))

    def test_multiline_literal_span_and_effective_assignment(self):
        source = ("ENTRIES = [{'name': 'discarded'}]\nSKIPPED = []\n"
                  "ENTRIES = [" + repr(self.entry).replace("'changed'", "\"\"\"old\nreplacement\"\"\"") + "] # keep\n")
        self.path.write_text(source)
        self.value.write_text('new\nreplacement')
        code, _, error = self.invoke('--set', str(self.path), 'guard', 'replacement', str(self.value))
        self.assertEqual(code, 0, error)
        self.assertEqual(self.path.read_text(), source.replace('\"\"\"old\nreplacement\"\"\"', repr('new\nreplacement')))

    def test_step_name_disambiguates_and_guards_are_json(self):
        second = dict(self.entry, step='2')
        self.path.write_text('ENTRIES = ' + repr([self.entry, second]))
        self.value.write_text('["TestOther", "TestGuard"]\n')
        code, _, error = self.invoke('--set', str(self.path), '2/guard', 'guards', str(self.value))
        self.assertEqual(code, 0, error)
        entries = mutchecks.load_manifest(self.path)['ENTRIES']
        self.assertEqual(entries[0]['guards'], ['TestGuard'])
        self.assertEqual(entries[1]['guards'], ['TestOther', 'TestGuard'])

    def test_ambiguous_name_leaves_bytes_unchanged(self):
        self.path.write_text('ENTRIES = ' + repr([self.entry, dict(self.entry, step='2')]))
        before = self.path.read_bytes()
        self.value.write_text('new')
        code, _, error = self.invoke('--set', str(self.path), 'guard', 'replacement', str(self.value))
        self.assertEqual(code, 1)
        self.assertIn('found 2', error)
        self.assertEqual(self.path.read_bytes(), before)

    def test_rejected_edits_leave_bytes_unchanged(self):
        self.path.write_text('ENTRIES = ' + repr([self.entry]))
        before = self.path.read_bytes()
        for field, value in [('original', 'missing'), ('guards', '{}')]:
            with self.subTest(field=field):
                self.value.write_text(value)
                code, _, _ = self.invoke('--set', str(self.path), 'guard', field, str(self.value))
                self.assertEqual(code, 1)
                self.assertEqual(self.path.read_bytes(), before)
        self.value.write_text(json.dumps([self.entry, dict(self.entry, original='missing')]))
        code, _, error = self.invoke('--append', str(self.path), str(self.value))
        self.assertEqual(code, 1)
        self.assertIn('original occurs 0 times', error)
        self.assertEqual(self.path.read_bytes(), before)
        self.value.write_text('[{}]')
        code, _, _ = self.invoke('--append', str(self.path), str(self.value))
        self.assertEqual(code, 1)
        self.assertEqual(self.path.read_bytes(), before)
        self.path.write_text('ENTRIES = []\nprint("no code")')
        before = self.path.read_bytes()
        code, _, _ = self.invoke('--append', str(self.path), str(self.value))
        self.assertEqual(code, 1)
        self.assertEqual(self.path.read_bytes(), before)

    def test_text_newline_rule_and_packages_json(self):
        for old, value, expected in [('changed', 'new\n', 'new'),
                                     ('changed\n', 'new\n', 'new\n'),
                                     ('changed', 'new\n\n', 'new\n\n'),
                                     ('changed', 'new', 'new')]:
            with self.subTest(old=old, value=value):
                self.path.write_text('ENTRIES = ' + repr([dict(self.entry, replacement=old)]))
                self.value.write_text(value)
                code, _, error = self.invoke('--set', str(self.path), 'guard', 'replacement', str(self.value))
                self.assertEqual(code, 0, error)
                self.assertEqual(mutchecks.load_manifest(self.path)['ENTRIES'][0]['replacement'], expected)
        self.value.write_text('["./internal/dbtest"]')
        code, _, error = self.invoke('--set', str(self.path), 'guard', 'packages', str(self.value))
        self.assertEqual(code, 0, error)
        self.assertEqual(mutchecks.load_manifest(self.path)['ENTRIES'][0]['packages'], ['./internal/dbtest'])

    def test_append_python_literals_preserves_prefix(self):
        before = b'ENTRIES = [] # keep'
        self.path.write_bytes(before)
        self.value.write_text(json.dumps([self.entry]))
        code, _, error = self.invoke('--append', str(self.path), str(self.value))
        self.assertEqual(code, 0, error)
        self.assertEqual(self.path.read_bytes(), before + ('\nENTRIES.extend(' + repr([self.entry]) + ')\n').encode())
        self.assertEqual(mutchecks.load_manifest(self.path)['ENTRIES'], [self.entry])


class AllManifestTests(unittest.TestCase):
    def invoke(self, root, *args, free=1 << 40, disk_usage=None):
        usage = (patch.object(mutchecks.shutil, 'disk_usage', side_effect=disk_usage)
                 if disk_usage is not None else
                 patch.object(mutchecks.shutil, 'disk_usage', return_value=Mock(free=free)))
        with patch.object(mutchecks, 'ROOT', root), patch.object(mutchecks, 'SharedServers', FakeSharedServers), patch.object(sys, 'argv', ['mutchecks.py', *args]), patch('sys.stdout', new=io.StringIO()) as output, patch('sys.stderr', new=io.StringIO()) as error, usage:
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

            def select(entries, ref, **kwargs):
                self.assertEqual(ref, 'main')
                return [] if entries[0]['name'] == 'empty' else entries[:1]

            def run(groups, jobs, **kwargs):
                self.assertEqual(jobs, 4)
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
            self.assertIn('--jobs 8 超过上限 4，按 4 运行', error)

    def test_jobs_are_clamped_to_four(self):
        cases = [(None, '8', 4, '--jobs 8 超过上限 4，按 4 运行'),
                 ('2', '8', 2, '--jobs 8 超过上限 2，按 2 运行'),
                 ('8', '8', 8, None),
                 (None, '4', 4, None),
                 (None, '2', 2, None)]
        for cap, requested, expected, notice in cases:
            with self.subTest(cap=cap, requested=requested), tempfile.TemporaryDirectory() as folder:
                root = Path(folder)
                manifest = root / '.dev/codex/a/manifest.py'
                manifest.parent.mkdir(parents=True)
                entry = dict(kind='go', step='1', name='guard', file='guard',
                             original='original', replacement='mutated',
                             guards=['TestGuard'], packages=['a'])
                manifest.write_text('ENTRIES = ' + repr([entry]))
                seen = []

                def queue(groups, jobs, **kwargs):
                    seen.append(jobs)
                    for entries, manifest, logs, results in groups:
                        for entry in entries:
                            mutchecks.result_line(
                                f"{entry['step']} {entry['name']} exit=0 PASS", results)
                    return 0

                env = {} if cap is None else {mutchecks.MAX_JOBS_ENV: cap}
                with patch.dict(os.environ, env, clear=True), \
                        patch.object(mutchecks, 'affected_entries', return_value=[entry]), \
                        patch.object(mutchecks, 'run_queue', side_effect=queue):
                    code, output, error = self.invoke(root, '--since', 'main', '--jobs', requested)
                self.assertEqual(seen, [expected])
                self.assertEqual(code, 0, error)
                if notice:
                    self.assertIn(notice, error)
                else:
                    self.assertNotIn('超过上限', error)

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

            with patch.object(mutchecks, 'affected_entries', side_effect=lambda entries, ref, **kwargs: entries), patch.object(mutchecks, 'baseline', return_value=True) as baseline, patch.object(mutchecks, 'run_e2e', side_effect=e2e):
                code, output, error = self.invoke(root, '--since', 'main', '--jobs', '1')
            baseline.assert_called_once()
            logs = next(manifest.parent.glob('mutchecks-*'))
            self.assertFalse((root / '.dev/mutcheck-gocache').exists())
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

    def test_stale_cache_dirs_are_removed_before_the_run(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            stale = root / '.dev/mutcheck-gocache/killed-run'
            stale.mkdir(parents=True)
            (stale / 'entry').write_text('stale')
            manifest = root / '.dev/codex/a/manifest.py'
            manifest.parent.mkdir(parents=True)
            entry = dict(kind='go', step='1', name='guard', guards=['TestGuard'], packages=['a'])
            manifest.write_text('ENTRIES = ' + repr([entry]))
            observed = []

            def run_baseline(entries, logs, results, **kwargs):
                observed.append((stale.exists(),
                                 Path(os.environ['GOCACHE']).parent == root / '.dev/mutcheck-gocache'))
                return False

            with patch.object(mutchecks, 'affected_entries', return_value=[entry]), \
                    patch.object(mutchecks, 'baseline', side_effect=run_baseline), \
                    patch.object(mutchecks, 'run_go') as mutate:
                code, _, _ = self.invoke(root, '--since', 'main')
            self.assertEqual(code, 1)
            self.assertEqual(observed, [(False, True)])
            mutate.assert_not_called()
            self.assertFalse((root / '.dev/mutcheck-gocache').exists())

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

    def test_locate_does_not_create_or_remove_run_caches(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            stale = root / '.dev/mutcheck-gocache/run-existing'
            stale.mkdir(parents=True)
            (stale / 'keep').write_text('keep')
            manifest = root / '.dev/codex/a/manifest.py'
            manifest.parent.mkdir(parents=True)
            entry = dict(kind='go', step='1', name='guard', file='guard',
                         original='original', replacement='mutated',
                         guards=['TestGuard'], packages=['a'])
            manifest.write_text('ENTRIES = ' + repr([entry]))
            (root / 'guard').write_text('original')
            code, output, error = self.invoke(root, '--locate')
            self.assertEqual(code, 0, error)
            self.assertTrue((stale / 'keep').exists())
            self.assertEqual([path.name for path in (root / '.dev/mutcheck-gocache').iterdir()],
                             ['run-existing'])
            self.assertIn('total selected=1 passed=1 failed=0', output)

    def test_locate_does_not_take_the_heavy_lock(self):
        with tempfile.TemporaryDirectory() as folder:
            with patch.object(mutchecks, 'acquire_heavy_lock') as acquire:
                code, output, error = self.invoke(Path(folder), '--locate')
            acquire.assert_not_called()
            self.assertEqual(code, 0, error)

    def test_worker_logs_does_not_take_the_heavy_lock(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            manifest = root / 'manifest.py'
            manifest.write_text('ENTRIES = []')
            logs = root / 'logs'
            logs.mkdir()
            with patch.object(mutchecks, 'acquire_heavy_lock') as acquire:
                code, output, error = self.invoke(root, str(manifest), '--worker-logs', str(logs))
            acquire.assert_not_called()
            self.assertEqual(code, 0, error)


class SelectionTests(unittest.TestCase):
    def setUp(self):
        self.folder = tempfile.TemporaryDirectory()
        self.addCleanup(self.folder.cleanup)
        self.root = Path(self.folder.name)
        init_repo(self.root)
        self.migrations = self.root / 'backend/internal/db/migrations'
        self.queries = self.root / 'backend/internal/db/queries'
        write(self.migrations / '00001_alpha.sql',
              '-- +goose Up\nCREATE TABLE alpha (id int);\nCREATE TABLE beta (id int);\n'
              '-- +goose Down\nDROP TABLE beta;\nDROP TABLE alpha;\n')
        write(self.queries / 'alpha.sql',
              '-- name: GetAlpha :one\nSELECT * FROM alpha;\n'
              '-- name: AddAlpha :one\nINSERT INTO alpha (id) VALUES ($1) RETURNING *;\n')
        write(self.queries / 'beta.sql', '-- name: GetBeta :one\nSELECT * FROM beta;\n')
        self.entries = [
            migration_entry('m-alpha', 'backend/internal/db/migrations/00001_alpha.sql',
                            'CREATE TABLE alpha (id int);'),
            migration_entry('m-beta', 'backend/internal/db/migrations/00001_alpha.sql',
                            'CREATE TABLE beta (id int);'),
            query_entry('q-alpha', 'backend/internal/db/queries/alpha.sql', 'SELECT * FROM alpha;'),
            query_entry('q-alpha-insert', 'backend/internal/db/queries/alpha.sql',
                        'INSERT INTO alpha (id) VALUES ($1) RETURNING *;'),
            query_entry('q-beta', 'backend/internal/db/queries/beta.sql', 'SELECT * FROM beta;'),
        ]
        commit_fixture(self.root)

    def affected(self):
        with patch.object(mutchecks, 'ROOT', self.root), patch('sys.stderr', new=io.StringIO()) as error:
            selected = mutchecks.affected_entries(self.entries, 'HEAD')
        return {entry['name'] for entry in selected}, error.getvalue()

    def test_changed_migration_table_selects_only_related_entries(self):
        write(self.migrations / '00002_change.sql', 'ALTER TABLE alpha ADD COLUMN name text;\n')
        selected, error = self.affected()
        self.assertEqual(selected, {'m-alpha', 'q-alpha', 'q-alpha-insert'})
        self.assertIn('changed tables: alpha;', error)
        self.assertIn('skipped 2 migration/query entries', error)

    def test_own_migration_file_change_selects_all_its_entries(self):
        write(self.migrations / '00001_alpha.sql',
              (self.migrations / '00001_alpha.sql').read_text() + '-- changed\n')
        selected, _ = self.affected()
        self.assertIn('m-alpha', selected)
        self.assertIn('m-beta', selected)

    def test_another_query_file_does_not_select_a_query(self):
        write(self.queries / 'beta.sql', (self.queries / 'beta.sql').read_text() + '-- changed\n')
        selected, _ = self.affected()
        self.assertEqual(selected, {'q-beta'})

    def test_unattributable_statement_selects_every_migration_and_query(self):
        write(self.migrations / '00002_function.sql',
              'CREATE FUNCTION one() RETURNS int LANGUAGE sql AS $$ SELECT 1; $$;\n')
        selected, error = self.affected()
        self.assertEqual(selected, {entry['name'] for entry in self.entries})
        self.assertIn('changed tables: all', error)

    def test_entry_without_a_determinable_table_is_selected(self):
        odd = migration_entry('m-odd', 'backend/internal/db/migrations/00003_odd.sql',
                              "COMMENT ON COLUMN missing.column IS 'x';")
        write(self.migrations / '00003_odd.sql', "COMMENT ON COLUMN missing.column IS 'x';\n")
        commit_fixture(self.root)
        write(self.migrations / '00002_change.sql', 'ALTER TABLE alpha ADD COLUMN name text;\n')
        with patch.object(mutchecks, 'ROOT', self.root), patch('sys.stderr', new=io.StringIO()):
            selected = mutchecks.affected_entries(self.entries + [odd], 'HEAD')
        self.assertIn('m-odd', {entry['name'] for entry in selected})

    def test_down_section_tables_count(self):
        delta = migration_entry('m-delta', 'backend/internal/db/migrations/00001_alpha.sql',
                                'DROP TABLE beta;')
        write(self.migrations / '00002_change.sql',
              '-- +goose Up\nCREATE TABLE gamma (id int);\n-- +goose Down\nDROP TABLE beta;\n')
        with patch.object(mutchecks, 'ROOT', self.root), patch('sys.stderr', new=io.StringIO()):
            selected = mutchecks.affected_entries([delta], 'HEAD')
        self.assertEqual([entry['name'] for entry in selected], ['m-delta'])

    def test_drop_table_named_in_the_original_is_used(self):
        drop = migration_entry('m-drop', 'backend/internal/db/migrations/00004_drop.sql', 'DROP TABLE beta;')
        write(self.migrations / '00004_drop.sql', 'DROP TABLE beta;\n')
        commit_fixture(self.root)
        write(self.migrations / '00005_change.sql', 'ALTER TABLE alpha ADD COLUMN name text;\n')
        with patch.object(mutchecks, 'ROOT', self.root), patch('sys.stderr', new=io.StringIO()):
            selected = mutchecks.affected_entries([drop], 'HEAD')
        self.assertEqual(selected, [])

    def test_reference_target_selects_a_foreign_key_protection(self):
        fk = migration_entry(
            'm-fk', 'backend/internal/db/migrations/00006_fk.sql',
            'ADD CONSTRAINT fk FOREIGN KEY (order_id) REFERENCES orders (id);')
        write(self.migrations / '00006_fk.sql',
              'ALTER TABLE order_items ADD CONSTRAINT fk FOREIGN KEY (order_id) REFERENCES orders (id);\n')
        commit_fixture(self.root)
        write(self.migrations / '00007_change.sql', 'ALTER TABLE orders ADD COLUMN name text;\n')
        with patch.object(mutchecks, 'ROOT', self.root), patch('sys.stderr', new=io.StringIO()):
            selected = mutchecks.affected_entries([fk], 'HEAD')
        self.assertEqual([entry['name'] for entry in selected], ['m-fk'])


class TableExtractionTests(unittest.TestCase):
    def test_query_join(self):
        self.assertEqual(mutchecks.query_tables('SELECT * FROM orders o JOIN customers c ON c.id=o.id'),
                         {'orders', 'customers'})

    def test_query_update(self):
        self.assertEqual(mutchecks.query_tables('UPDATE orders SET status=$1 WHERE id=$2'), {'orders'})

    def test_query_delete_from(self):
        self.assertEqual(mutchecks.query_tables('DELETE FROM orders WHERE id=$1'), {'orders'})

    def test_query_delete_using(self):
        self.assertEqual(mutchecks.query_tables('DELETE FROM orders USING customers WHERE customers.id=orders.cid'),
                         {'orders', 'customers'})

    def test_query_comma_from(self):
        self.assertEqual(mutchecks.query_tables('SELECT * FROM orders o, customers c WHERE c.id=o.cid'),
                         {'orders', 'customers'})

    def test_query_update_from(self):
        self.assertEqual(mutchecks.query_tables('UPDATE orders SET x=1 FROM customers WHERE customers.id=orders.cid'),
                         {'orders', 'customers'})

    def test_query_function_in_from_is_unknown(self):
        self.assertIsNone(mutchecks.query_tables('SELECT * FROM generate_series(1, 10)'))

    def test_quoted_identifiers(self):
        self.assertEqual(mutchecks.query_tables('SELECT * FROM "public"."orders" o JOIN "customers" c ON true'),
                         {'orders', 'customers'})

    def test_references_targets(self):
        category, tables = mutchecks.statement_tables(
            'ALTER TABLE order_items ADD CONSTRAINT fk FOREIGN KEY (order_id) REFERENCES orders (id)')
        self.assertEqual(category, 'alter_table')
        self.assertEqual(tables, {'order_items', 'orders'})

    def test_create_index_on(self):
        self.assertEqual(mutchecks.statement_tables('CREATE UNIQUE INDEX IF NOT EXISTS orders_key ON ONLY orders (id)')[1],
                         {'orders'})

    def test_policy_using_subquery(self):
        self.assertEqual(
            mutchecks.statement_tables(
                'CREATE POLICY tenant_isolation ON orders USING '
                '(EXISTS (SELECT 1 FROM customers c WHERE c.id=orders.customer_id))')[1],
            {'orders', 'customers'})
        self.assertEqual(
            mutchecks.statement_tables(
                'CREATE POLICY tenant_isolation ON orders WITH CHECK '
                '(EXISTS (SELECT 1 FROM stores s WHERE s.id=orders.store_id))')[1],
            {'orders', 'stores'})

    def test_trigger_on(self):
        self.assertEqual(mutchecks.statement_tables(
            'CREATE TRIGGER audit AFTER UPDATE ON orders FOR EACH ROW EXECUTE FUNCTION log()')[1], {'orders'})

    def test_drop_trigger_on(self):
        self.assertEqual(mutchecks.statement_tables('DROP TRIGGER IF EXISTS audit ON orders')[1], {'orders'})

    def test_grant_on(self):
        self.assertEqual(mutchecks.statement_tables('GRANT SELECT ON "orders", public.customers TO salon_app')[1],
                         {'orders', 'customers'})
        self.assertEqual(mutchecks.statement_tables('GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO salon_app')[1],
                         set())

    def test_rename_to_keeps_both_names(self):
        self.assertEqual(mutchecks.statement_tables('ALTER TABLE orders RENAME TO sales')[1], {'orders', 'sales'})

    def test_all_tables_fallbacks(self):
        for statement in ('CREATE FUNCTION f() RETURNS int LANGUAGE sql AS $$ SELECT 1 $$',
                          'DO $$ BEGIN PERFORM 1; END $$',
                          'DROP FUNCTION f(int)',
                          'CREATE VIEW order_summary AS SELECT * FROM orders',
                          'ALTER VIEW order_summary RENAME TO other',
                          'DROP VIEW order_summary',
                          'CREATE RULE r AS ON INSERT TO orders DO NOTHING',
                          'ALTER PUBLICATION p ADD TABLE orders'):
            with self.subTest(statement=statement):
                self.assertIsNone(mutchecks.statement_tables(statement)[1])

    def test_migration_entry_includes_enclosing_statement_tables(self):
        source = ('ALTER TABLE order_items ADD CONSTRAINT fk FOREIGN KEY (order_id) '
                  'REFERENCES orders (id);\n')
        entry = dict(file='m.sql', original='ADD CONSTRAINT fk FOREIGN KEY (order_id) REFERENCES orders (id)')
        statements = mutchecks.expanded_statements(source)
        self.assertEqual(mutchecks.migration_entry_tables(entry, source, statements), {'order_items', 'orders'})

    def test_query_entry_using_a_view_is_selected(self):
        source = '-- name: GetSummary :many\nSELECT * FROM order_summary;\n'
        entry = dict(file='q.sql', original='SELECT * FROM order_summary;')
        positions = mutchecks.name_positions(source)
        self.assertIsNone(mutchecks.query_entry_tables(entry, source, positions, {'order_summary'}))


class ChangedMigrationTests(unittest.TestCase):
    def setUp(self):
        self.folder = tempfile.TemporaryDirectory()
        self.addCleanup(self.folder.cleanup)
        self.root = Path(self.folder.name)
        init_repo(self.root)
        self.path = 'backend/internal/db/migrations/00001_tables.sql'
        write(self.root / self.path, 'CREATE TABLE orders (id int);\n')
        commit_fixture(self.root)

    def changed(self, paths):
        with patch.object(mutchecks, 'ROOT', self.root):
            return mutchecks.changed_migration_tables(set(paths), 'HEAD')

    def test_old_and_worktree_revisions_are_unioned(self):
        write(self.root / self.path, 'CREATE TABLE customers (id int);\n')
        self.assertEqual(self.changed({self.path}), (False, {'orders', 'customers'}, None))

    def test_deleted_migration_uses_the_ref_revision(self):
        (self.root / self.path).unlink()
        self.assertEqual(self.changed({self.path}), (False, {'orders'}, None))

    def test_path_missing_in_both_is_all_tables(self):
        all_tables, tables, reason = self.changed({'backend/internal/db/migrations/ghost.sql'})
        self.assertTrue(all_tables)
        self.assertEqual(tables, set())
        self.assertIn('missing', reason)

    def test_rename_counts_both_sides(self):
        new_path = 'backend/internal/db/migrations/00002_tables.sql'
        git(self.root, 'mv', self.path, new_path)
        write(self.root / new_path, 'CREATE TABLE sales (id int);\n')
        with patch.object(mutchecks, 'ROOT', self.root):
            paths = mutchecks.changed_paths('HEAD')
        self.assertIn(self.path, paths)
        self.assertIn(new_path, paths)
        self.assertEqual(self.changed({self.path, new_path}), (False, {'orders', 'sales'}, None))


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
                ['go', 'test', '-count=1', '-trimpath', '-run', '^(TestA|TestShared|TestB)$', 'a'],
                ['go', 'test', '-count=1', '-trimpath', '-run', '^(TestA|TestShared)$', 'b']])
            self.assertTrue(all(call.kwargs['cwd'] == mutchecks.ROOT / 'backend' for call in start.call_args_list))

    def test_children_receive_the_run_gocache(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            (root / 'backend').mkdir()
            cache = root / '.dev/mutcheck-gocache/run-test'
            cache.mkdir(parents=True)
            (root / 'guard').write_text('original')
            logs = root / 'logs'
            logs.mkdir()
            entry = dict(kind='go', step='1', name='guard', file='guard',
                         original='original', replacement='mutated',
                         guards=['TestGuard'], packages=['a'])
            with patch.object(mutchecks, 'ROOT', root), patch.dict(os.environ, {'GOCACHE': str(cache)}), \
                    patch.object(mutchecks.subprocess, 'Popen') as start, patch.object(mutchecks, 'stop_processes'):
                start.return_value.poll.return_value = 0
                self.assertTrue(mutchecks.baseline([entry], logs, io.StringIO()))
            self.assertEqual(start.call_args.kwargs['env']['GOCACHE'], str(cache))

    def test_run_go_uses_the_explicit_mutation_cache(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            (root / 'backend').mkdir()
            cache = root / '.dev/mutcheck-gocache/run-test/entry'
            cache.mkdir(parents=True)
            (root / 'guard').write_text('original')
            logs = root / 'logs'
            logs.mkdir()
            entry = dict(kind='go', step='1', name='guard', file='guard',
                         original='original', replacement='mutated',
                         guards=['TestGuard'], packages=['a'])
            with patch.object(mutchecks, 'ROOT', root), \
                    patch.object(mutchecks.subprocess, 'run', return_value=Mock(returncode=0)) as execute:
                mutchecks.run_go(entry, logs, io.StringIO(), cache)
            self.assertEqual(execute.call_args.kwargs['env']['GOCACHE'], str(cache))

    def test_disk_headroom_env_override(self):
        with patch.dict(os.environ, {mutchecks.DISK_HEADROOM_ENV: '12345'}):
            self.assertEqual(mutchecks.disk_headroom(), 12345)
        with patch.dict(os.environ, {}, clear=True):
            self.assertEqual(mutchecks.disk_headroom(), mutchecks.DISK_HEADROOM)

    def test_low_disk_stops_before_baseline_for_serial_and_parallel_runs(self):
        for jobs in ('1', '8'):
            with self.subTest(jobs=jobs), tempfile.TemporaryDirectory() as folder:
                root = Path(folder)
                manifest = root / '.dev/codex/a/manifest.py'
                manifest.parent.mkdir(parents=True)
                entry = dict(kind='go', step='1', name='guard', file='guard',
                             original='original', replacement='mutated',
                             guards=['TestGuard'], packages=['a'])
                manifest.write_text('ENTRIES = ' + repr([entry]))
                with patch.object(mutchecks, 'affected_entries', return_value=[entry]), \
                        patch.object(mutchecks, 'baseline') as baseline, \
                        patch.object(mutchecks, 'run_go') as mutate:
                    code, output, error = AllManifestTests().invoke(
                        root, '--since', 'main', '--jobs', jobs, free=mutchecks.DISK_HEADROOM - 1)
                baseline.assert_not_called()
                mutate.assert_not_called()
                self.assertEqual(code, 1)
                self.assertIn('保护验证停止', error)
                self.assertIn('selected=1 passed=0 failed=0 environment=1', output)
                results = next(manifest.parent.glob('mutchecks-*')) / 'results.log'
                content = results.read_text()
                self.assertIn('not run (environment)', content)
                self.assertNotIn('PASS', content)
                self.assertFalse((root / '.dev/mutcheck-gocache').exists())

    def test_keyboard_interrupt_removes_the_run_cache(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            manifest = root / '.dev/codex/a/manifest.py'
            manifest.parent.mkdir(parents=True)
            entry = dict(kind='go', step='1', name='guard', guards=['TestGuard'], packages=['a'])
            manifest.write_text('ENTRIES = ' + repr([entry]))
            with patch.object(mutchecks, 'affected_entries', return_value=[entry]), \
                    patch.object(mutchecks, 'baseline', side_effect=KeyboardInterrupt):
                code, output, _ = AllManifestTests().invoke(root, '--since', 'main')
            self.assertEqual(code, 130)
            self.assertFalse((root / '.dev/mutcheck-gocache').exists())

    def test_low_disk_before_an_entry_stops_a_single_manifest_run(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            manifest = root / 'manifest.py'
            entry = dict(kind='go', step='1', name='guard', file='guard',
                         original='original', replacement='mutated',
                         guards=['TestGuard'], packages=['a'])
            manifest.write_text('ENTRIES = ' + repr([entry]))
            checks = []

            def disk_usage(path):
                checks.append(path)
                return Mock(free=(1 << 40) if len(checks) == 1 else mutchecks.DISK_HEADROOM - 1)

            with patch.object(mutchecks, 'baseline', return_value=True) as baseline, \
                    patch.object(mutchecks, 'run_go') as mutate:
                code, output, error = AllManifestTests().invoke(root, str(manifest), disk_usage=disk_usage)
            baseline.assert_called_once()
            mutate.assert_not_called()
            self.assertEqual(code, mutchecks.DISK_EXIT)
            self.assertIn('保护验证停止', error)
            self.assertIn('environment=1', output)
            self.assertFalse((root / '.dev/mutcheck-gocache').exists())

    def test_low_disk_before_a_worker_entry_stops_the_parallel_run(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            init_repo(root)
            (root / 'guard').write_text('original')
            stage_fixture(root)
            manifest = root / '.dev/codex/a/manifest.py'
            manifest.parent.mkdir(parents=True)
            entry = dict(kind='go', step='1', name='guard', file='guard',
                         original='original', replacement='mutated',
                         guards=['TestGuard'], packages=['a'])
            manifest.write_text('ENTRIES = ' + repr([entry]))
            checks = []

            def disk_usage(path):
                checks.append(path)
                return Mock(free=(1 << 40) if len(checks) == 1 else mutchecks.DISK_HEADROOM - 1)

            with patch.object(mutchecks, 'affected_entries', return_value=[entry]), \
                    patch.object(mutchecks, 'baseline', return_value=True) as baseline:
                code, output, error = AllManifestTests().invoke(
                    root, '--since', 'main', '--jobs', '8', disk_usage=disk_usage)
            baseline.assert_called_once()
            self.assertEqual(code, 1)
            self.assertIn('保护验证停止', error)
            self.assertIn('environment=1', output)
            self.assertFalse((root / '.dev/mutcheck-gocache').exists())
            self.assertFalse((root / '.dev/mutcheck-worktrees').exists())


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

            def start(command, *args, **kwargs):
                process = popen(command, *args, **kwargs)
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



class ParallelismTests(unittest.TestCase):
    def test_baseline_uses_per_worker_p_flag_from_env(self):
        entries = [dict(kind='go', step='1', name='guard', packages=['a'], guards=['TestGuard'])]
        with tempfile.TemporaryDirectory() as folder, \
                patch.dict(os.environ, {mutchecks.GO_PARALLEL_ENV: '4', 'GOMAXPROCS': '4'}), \
                patch.object(mutchecks.subprocess, 'Popen') as start, \
                patch.object(mutchecks, 'stop_processes'):
            start.return_value.poll.return_value = 0
            self.assertTrue(mutchecks.baseline(entries, Path(folder), io.StringIO()))
        self.assertEqual(start.call_args.args[0],
                         ['go', 'test', '-count=1', '-trimpath', '-p', '4',
                          '-run', '^(TestGuard)$', 'a'])
        self.assertEqual(start.call_args.kwargs['env']['GOMAXPROCS'], '4')

    def test_shell_passes_p_flag_to_every_go_test(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            scripts = root / 'backend/scripts'
            scripts.mkdir(parents=True)
            shutil.copy(Path(__file__).with_name('mutcheck.sh'), scripts / 'mutcheck.sh')
            target = root / 'guard.go'
            target.write_text('original')
            bin_dir = root / 'bin'
            bin_dir.mkdir()
            calls = root / 'calls'
            go = bin_dir / 'go'
            go.write_text('#!/bin/bash\n'
                          'echo "$*" >> "$CALLS"\n'
                          'if [[ $* == *"-run ^$"* ]]; then exit 0; fi\n'
                          "echo '--- FAIL: TestGuard (0.00s)'\n"
                          'exit 1\n')
            go.chmod(0o755)
            env = {key: value for key, value in os.environ.items()
                   if key not in ('GOCACHE', 'GATE_HEAVY_LOCK', 'SALON_HEAVY_LOCK_HELD',
                                  mutchecks.GO_PARALLEL_ENV)}
            env.update(PATH=str(bin_dir) + os.pathsep + env['PATH'],
                       CALLS=str(calls), **{mutchecks.GO_PARALLEL_ENV: '4'})
            result = subprocess.run(
                ['bash', str(scripts / 'mutcheck.sh'), str(target),
                 'original', 'mutated', './internal/example'],
                env=env, text=True, capture_output=True)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            commands = calls.read_text().splitlines()
            self.assertGreaterEqual(len(commands), 2, commands)
            self.assertTrue(all('-p 4' in command for command in commands), commands)


class HeavyLockTests(unittest.TestCase):
    def test_path_uses_gate_env_or_repo_dev(self):
        with patch.object(mutchecks, 'ROOT', Path('/repo')):
            with patch.dict(os.environ, {mutchecks.HEAVY_LOCK_ENV: '/tmp/shared.lock'}, clear=True):
                self.assertEqual(mutchecks.heavy_lock_path(), Path('/tmp/shared.lock'))
            with patch.dict(os.environ, {}, clear=True):
                self.assertEqual(mutchecks.heavy_lock_path(), Path('/repo/.dev/heavy.lock'))

    def test_acquires_lock_and_prints_waiting_when_busy(self):
        with tempfile.TemporaryDirectory() as folder:
            path = (Path(folder) / 'heavy.lock').resolve()
            with patch.dict(os.environ, {mutchecks.HEAVY_LOCK_ENV: str(path)}, clear=True), \
                    patch.object(mutchecks.fcntl, 'flock',
                                 side_effect=[BlockingIOError(), None]) as flock, \
                    patch('sys.stderr', new=io.StringIO()) as error:
                lock = mutchecks.acquire_heavy_lock()
                self.assertIsNotNone(lock)
                lock.close()
                self.assertEqual(os.environ[mutchecks.HEAVY_LOCK_MARKER], str(path))
                self.assertIn(f'waiting for heavy lock {path}', error.getvalue())
            self.assertEqual(flock.call_args_list[0].args[1],
                             mutchecks.fcntl.LOCK_EX | mutchecks.fcntl.LOCK_NB)
            self.assertEqual(flock.call_args_list[1].args[1], mutchecks.fcntl.LOCK_EX)

    def test_skips_lock_when_marker_matches(self):
        with tempfile.TemporaryDirectory() as folder:
            path = (Path(folder) / 'heavy.lock').resolve()
            env = {mutchecks.HEAVY_LOCK_ENV: str(path), mutchecks.HEAVY_LOCK_MARKER: str(path)}
            with patch.dict(os.environ, env, clear=True), \
                    patch.object(mutchecks.fcntl, 'flock') as flock:
                self.assertIsNone(mutchecks.acquire_heavy_lock())
            flock.assert_not_called()

    def test_marker_for_another_path_does_not_skip(self):
        with tempfile.TemporaryDirectory() as folder:
            path = (Path(folder) / 'heavy.lock').resolve()
            env = {mutchecks.HEAVY_LOCK_ENV: str(path),
                   mutchecks.HEAVY_LOCK_MARKER: str(path) + '.other'}
            with patch.dict(os.environ, env, clear=True), \
                    patch.object(mutchecks.fcntl, 'flock') as flock:
                lock = mutchecks.acquire_heavy_lock()
                lock.close()
            self.assertTrue(flock.called)


class SharedServerTests(unittest.TestCase):
    def test_container_image_matches_dbtest(self):
        source = (mutchecks.ROOT / 'backend/internal/dbtest/dbtest.go').read_text()
        self.assertIn(f'Image = "{mutchecks.POSTGRES_IMAGE}"', source)

    def fake_docker(self, root):
        binary = root / 'bin'
        binary.mkdir(exist_ok=True)
        log = root / 'docker.log'
        script = binary / 'docker'
        script.write_text(
            '#!' + sys.executable + '\n'
            "import os, pathlib, sys\n"
            f"log = pathlib.Path({str(log)!r})\n"
            "with log.open('a') as output: output.write(' '.join(sys.argv[1:]) + '\\n')\n"
            "args = sys.argv[1:]\n"
            "if args[:2] == ['run', '-d']:\n"
            "    name = args[args.index('--name') + 1]\n"
            "    count = sum(line.startswith('run -d') for line in log.read_text().splitlines())\n"
            "    if os.environ.get('DOCKER_FAIL_INDEX') == str(count):\n"
            "        print('boom', file=sys.stderr); sys.exit(1)\n"
            "    print('container-' + name); sys.exit(0)\n"
            "if args[0] == 'port':\n"
            "    print('127.0.0.1:' + str(55000 + int(args[1].rsplit('-', 1)[-1]))); sys.exit(0)\n"
            "if args[0] == 'exec':\n"
            "    if '-h' not in args:\n"
            "        sys.exit(0)\n"
            "    probes = sum(line.startswith('exec ') and 'pg_isready' in line\n"
            "                 for line in log.read_text().splitlines())\n"
            "    failures = int(os.environ.get('DOCKER_TCP_FAILURES', '0'))\n"
            "    sys.exit(1 if probes <= failures else 0)\n"
            "if args[0] == 'rm':\n"
            "    sys.exit(0)\n"
            "sys.exit(0)\n")
        script.chmod(0o755)
        return bin_dir_env(binary), log

    def test_start_waits_for_readiness_and_removes_only_its_own_containers(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            env, log = self.fake_docker(root)
            with patch.dict(os.environ, env, clear=False), \
                    patch.object(socket, 'create_connection', return_value=MagicMock()):
                servers = mutchecks.SharedServers(2).start()
                self.assertEqual(len(servers.dsns), 2)
                self.assertIn('127.0.0.1:55000', servers.dsn(0))
                self.assertIn('127.0.0.1:55001', servers.dsn(1))
                servers.close()
            calls = log.read_text().splitlines()
            self.assertTrue(any(call.startswith('run -d') and '--tmpfs /var/lib/postgresql/data' in call for call in calls))
            self.assertTrue(all('-c max_connections=300' in call for call in calls if call.startswith('run -d')))
            probes = [call for call in calls if 'pg_isready' in call]
            self.assertTrue(probes)
            self.assertTrue(all(call.startswith('exec ') for call in probes))
            self.assertTrue(all(' pg_isready -h 127.0.0.1 -p 5432 -U salon -d salon' in call
                                for call in probes))
            removed = [call for call in calls if call.startswith('rm -f')]
            self.assertEqual(len(removed), 2)
            self.assertTrue(all(call.startswith('rm -f salon-mutcheck-pg-') for call in removed))
            self.assertFalse(any('salon-dev-db' in call for call in removed))

    def test_start_waits_for_tcp_not_only_the_init_socket(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            env, log = self.fake_docker(root)
            env['DOCKER_TCP_FAILURES'] = '3'
            with patch.dict(os.environ, env, clear=False), \
                    patch.object(socket, 'create_connection', return_value=MagicMock()) as host_tcp:
                servers = mutchecks.SharedServers(1).start()
                servers.close()
            calls = log.read_text().splitlines()
            probes = [call for call in calls if 'pg_isready' in call]
            self.assertEqual(len(probes), 5, probes)
            self.assertTrue(all(' pg_isready -h 127.0.0.1 -p 5432 -U salon -d salon' in call
                                for call in probes))
            self.assertEqual([call.args[0] for call in host_tcp.call_args_list],
                             [('127.0.0.1', 55000), ('127.0.0.1', 55000)])
            removed = [call for call in calls if call.startswith('rm -f')]
            self.assertEqual(len(removed), 1)

    def test_failed_start_removes_the_containers_it_created(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            env, log = self.fake_docker(root)
            env['DOCKER_FAIL_INDEX'] = '2'
            with patch.dict(os.environ, env, clear=False), \
                    patch.object(socket, 'create_connection', return_value=MagicMock()):
                with self.assertRaises(mutchecks.SharedPGError):
                    mutchecks.SharedServers(2).start()
            removed = [call for call in log.read_text().splitlines() if call.startswith('rm -f')]
            self.assertEqual(len(removed), 1)
            self.assertTrue(all(call.startswith('rm -f salon-mutcheck-pg-') for call in removed))

    def test_each_worker_receives_its_own_dsn_and_servers_are_removed(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            init_repo(root)
            (root / 'guard').write_text('original')
            script = root / 'backend/scripts/mutchecks.py'
            script.parent.mkdir(parents=True)
            script.write_text(
                "import os, pathlib, sys\n"
                "logs = pathlib.Path(sys.argv[sys.argv.index('--worker-logs') + 1])\n"
                "name = sys.argv[-1].split('/')[1]\n"
                "(logs.parent / ('used-' + name)).write_text(os.environ.get('SALON_TEST_PG_DSN', ''))\n"
                "(logs / 'results.log').write_text(f'1 {name} exit=0 caught=TestGuard restored=True PASS\\n')\n")
            stage_fixture(root)
            manifest = root / 'manifest.py'
            manifest.write_text('ENTRIES = []')
            logs = root / 'logs'
            logs.mkdir()
            env, docker_log = self.fake_docker(root)
            entries = [dict(kind='go', step='1', name=name, file='guard', original='original',
                            replacement='mutated', packages=['a'], guards=['TestGuard'])
                       for name in ('a', 'b')]
            with patch.object(mutchecks, 'ROOT', root), patch.dict(os.environ, env, clear=False), \
                    patch.object(socket, 'create_connection', return_value=MagicMock()), \
                    patch.object(mutchecks, 'baseline', return_value=True), \
                    patch.object(mutchecks.shutil, 'disk_usage', return_value=Mock(free=1 << 40)), \
                    patch('sys.stdout', new=io.StringIO()):
                self.assertEqual(mutchecks.run_parallel(entries, manifest, 2, logs, io.StringIO(), shared_pg=True), 0)
            used = {name: (logs / f'used-{name}').read_text() for name in ('a', 'b')}
            self.assertEqual(len(set(used.values())), 2)
            self.assertTrue(all(value.startswith('postgres://salon:salon@127.0.0.1:5500') for value in used.values()))
            removed = [call for call in docker_log.read_text().splitlines() if call.startswith('rm -f')]
            self.assertEqual(len(removed), 2)

    def test_baseline_and_worker_receive_the_worker_dsn(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            init_repo(root)
            (root / 'backend').mkdir()
            (root / 'guard').write_text('original')
            seen = root / 'dsn.log'
            (root / 'bin').mkdir()
            go = root / 'bin/go'
            go.write_text('#!/bin/bash\nprintf "%s\\n" "$SALON_TEST_PG_DSN" >> "' + str(seen) + '"\nexit 0\n')
            go.chmod(0o755)
            script = root / 'backend/scripts/mutchecks.py'
            script.parent.mkdir(parents=True)
            script.write_text(
                "import os, pathlib, sys\n"
                f"pathlib.Path({str(seen)!r}).open('a').write(os.environ.get('SALON_TEST_PG_DSN', '') + '\\n')\n"
                "logs = pathlib.Path(sys.argv[sys.argv.index('--worker-logs') + 1])\n"
                "(logs / 'results.log').write_text('1 guard exit=0 caught=TestGuard restored=True PASS\\n')\n")
            stage_fixture(root)
            manifest = root / 'manifest.py'
            manifest.write_text('ENTRIES = []')
            logs = root / 'logs'
            logs.mkdir()
            env, _ = self.fake_docker(root)
            entry = dict(kind='go', step='1', name='guard', file='guard', original='original',
                         replacement='mutated', packages=['a'], guards=['TestGuard'])
            with patch.object(mutchecks, 'ROOT', root), patch.dict(os.environ, env, clear=False), \
                    patch.object(socket, 'create_connection', return_value=MagicMock()), \
                    patch.object(mutchecks.shutil, 'disk_usage', return_value=Mock(free=1 << 40)), \
                    patch('sys.stdout', new=io.StringIO()):
                self.assertEqual(mutchecks.run_parallel([entry], manifest, 1, logs, io.StringIO(), shared_pg=True), 0)
            self.assertEqual(set(seen.read_text().splitlines()),
                             {'postgres://salon:salon@127.0.0.1:55000/salon?sslmode=disable'})

    def test_worker_server_failure_is_environment_not_caught(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            (root / 'guard').write_text('original')
            manifest = root / 'manifest.py'
            manifest.write_text('ENTRIES = []')
            logs = root / 'logs'
            logs.mkdir()
            entry = dict(kind='go', step='1', name='guard', file='guard', original='original',
                         replacement='mutated', packages=['a'], guards=['TestGuard'])
            results = io.StringIO()
            with patch.object(mutchecks, 'ROOT', root), \
                    patch.object(mutchecks.SharedServers, 'start', side_effect=mutchecks.SharedPGError('boom')), \
                    patch.object(mutchecks.shutil, 'disk_usage', return_value=Mock(free=1 << 40)), \
                    patch('sys.stderr', new=io.StringIO()) as error:
                self.assertEqual(mutchecks.run_parallel([entry], manifest, 1, logs, results, shared_pg=True), 1)
            self.assertIn('not run (environment)', results.getvalue())
            self.assertIn('启动 PostgreSQL 测试服务器失败', error.getvalue())

    def test_sigterm_removes_shared_servers(self):
        with tempfile.TemporaryDirectory(dir=mutchecks.ROOT / '.dev') as folder:
            root = Path(folder)
            init_repo(root)
            script = root / 'backend/scripts/mutchecks.py'
            script.parent.mkdir(parents=True)
            text = Path(mutchecks.__file__).read_text()
            text = text.replace('selected = affected_entries(selected, args.since, summary=summary)', 'selected = selected')
            text = text.replace('selected = affected_entries(selected, args.since)', 'selected = selected')
            text = text.replace(
                'def main():',
                'SharedServers._host_accepts_tcp = staticmethod(lambda port, timeout=1.0: True)\n\n\ndef main():',
                1)
            script.write_text(text)
            (root / 'backend/scripts/mutcheck.sh').write_text(
                '#!/bin/bash\nprintf mutated > "$3"\nmkdir -p .dev\ntouch .dev/ready\nsleep 30\n')
            (root / 'backend/scripts/mutcheck.sh').chmod(0o755)
            (root / 'guard').write_text('original')
            stage_fixture(root)
            commit_fixture(root)
            (root / 'bin').mkdir()
            (root / 'bin/go').write_text('#!/bin/sh\nexit 0\n')
            (root / 'bin/go').chmod(0o755)
            env, docker_log = self.fake_docker(root)
            entry = dict(kind='go', step='1', name='guard', file='guard',
                         original='original', replacement='mutated', packages=['a'], guards=['TestGuard'])
            manifest = root / '.dev/codex/a/manifest.py'
            manifest.parent.mkdir(parents=True)
            manifest.write_text('ENTRIES = ' + repr([entry]))
            with relaxed_disk_headroom():
                process = subprocess.Popen([sys.executable, str(script), '--since', 'HEAD', '--jobs', '2'],
                                           cwd=root, env={**os.environ, **env}, stdout=subprocess.PIPE,
                                           stderr=subprocess.PIPE, start_new_session=True)
            try:
                ready = root / '.dev/mutcheck-worktrees/0/.dev/ready'
                deadline = time.monotonic() + 10
                while not ready.exists() and process.poll() is None and time.monotonic() < deadline:
                    time.sleep(0.02)
                if not ready.exists():
                    output, error = process.communicate(timeout=5)
                    self.fail(f'fixture never reached mutation: {output!r} {error!r}')
                process.send_signal(signal.SIGTERM)
                process.communicate(timeout=15)
                self.assertEqual(process.returncode, 130)
                calls = docker_log.read_text().splitlines()
                removed = [call for call in calls if call.startswith('rm -f')]
                self.assertEqual(len(removed), 1)
                self.assertTrue(all(call.startswith('rm -f salon-mutcheck-pg-') for call in removed))
            finally:
                if process.poll() is None:
                    process.kill()
                process.communicate()


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

    def test_seed_links_baseline_entries_and_prune_drops_mutation_entries(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            source = root / 'source'
            (source / 'aa').mkdir(parents=True)
            (source / 'aa' / 'base').write_text('baseline')
            target = root / 'target'
            seeded = mutchecks.seed_cache(source, target)
            (target / 'aa' / 'new').write_text('mutation')
            (target / 'bb').mkdir()
            (target / 'bb' / 'fresh').write_text('mutation')
            mutchecks.prune_cache(target, seeded)
            self.assertEqual((target / 'aa' / 'base').read_text(), 'baseline')
            self.assertFalse((target / 'aa' / 'new').exists())
            self.assertFalse((target / 'bb').exists())

    def test_serial_run_seeds_and_prunes_files_and_executable_directories(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            cache = root / '.dev/mutcheck-gocache/run-test'
            (cache / 'aa').mkdir(parents=True)
            (cache / 'aa' / 'base').write_text('baseline')
            (cache / 'bb' / 'keep-d').mkdir(parents=True)
            (cache / 'bb' / 'keep-d' / 'exec').write_text('seeded executable')
            (root / 'guard').write_text('original')
            scripts = root / 'backend/scripts'
            scripts.mkdir(parents=True)
            worker = scripts / 'mutcheck.sh'
            worker.write_text(
                '#!/bin/bash\n'
                'cache=${GOCACHE:?}\n'
                'printf mutant > "$cache/aa/new"\n'
                'mkdir -p "$cache/bb/keep-d/newdir" "$cache/cc"\n'
                'printf x > "$cache/bb/keep-d/newdir/file"\n'
                'printf y > "$cache/cc/new"\n'
                "echo 'caught by:'\n"
                "echo '--- FAIL: TestGuard (0.00s)'\n"
                'exit 0\n')
            worker.chmod(0o755)
            logs = root / 'logs'
            logs.mkdir()
            entry = dict(kind='go', step='1', name='guard', file='guard',
                         original='original', replacement='mutated',
                         guards=['TestGuard'], packages=['a'])
            args = SimpleNamespace(jobs=1, worker_logs=None, locate=False)
            with patch.object(mutchecks, 'ROOT', root), \
                    patch.dict(os.environ, {'GOCACHE': str(cache)}), \
                    patch.object(mutchecks, 'baseline', return_value=True), \
                    patch('sys.stdout', new=io.StringIO()):
                self.assertEqual(mutchecks.run_selected([entry], root / 'manifest.py', args, logs), 0)
            mutation = cache / 'entry'
            self.assertEqual((mutation / 'aa' / 'base').read_text(), 'baseline')
            self.assertEqual((mutation / 'bb' / 'keep-d' / 'exec').read_text(),
                             'seeded executable')
            self.assertFalse((mutation / 'aa' / 'new').exists())
            self.assertFalse((mutation / 'bb' / 'keep-d' / 'newdir').exists())
            self.assertFalse((mutation / 'cc').exists())

    def test_workers_use_a_seeded_cache_and_have_mutations_pruned(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            init_repo(root)
            cache = root / '.dev/mutcheck-gocache/run-test'
            (cache / 'aa').mkdir(parents=True)
            (cache / 'aa' / 'base').write_text('baseline')
            script = root / 'backend/scripts/mutchecks.py'
            script.parent.mkdir(parents=True)
            script.write_text(
                "import os, pathlib, sys\n"
                "cache = pathlib.Path(os.environ['GOCACHE'])\n"
                "(cache / 'bb').mkdir(exist_ok=True)\n"
                "(cache / 'bb' / 'new').write_text('mutation')\n"
                "logs = pathlib.Path(sys.argv[sys.argv.index('--worker-logs') + 1])\n"
                "(logs.parent / 'used-cache').write_text(str(cache))\n"
                "(logs / 'results.log').write_text('1 guard exit=0 caught=TestGuard restored=True PASS\\n')\n")
            (root / 'guard').write_text('original')
            stage_fixture(root)
            manifest = root / 'manifest.py'
            manifest.write_text('ENTRIES = []')
            logs = root / 'logs'
            logs.mkdir()
            entry = dict(kind='go', step='1', name='guard', file='guard', original='original',
                         replacement='mutated', packages=['a'], guards=['TestGuard'])
            with patch.object(mutchecks, 'ROOT', root), patch.dict(os.environ, {'GOCACHE': str(cache)}), \
                    patch.object(mutchecks, 'baseline', return_value=True), \
                    patch.object(mutchecks.shutil, 'disk_usage', return_value=Mock(free=1 << 40)), \
                    patch('sys.stdout', new=io.StringIO()):
                self.assertEqual(mutchecks.run_parallel([entry], manifest, 1, logs, io.StringIO()), 0)
            worker = cache / 'workers' / '0'
            self.assertEqual((logs / 'used-cache').read_text(), str(worker))
            self.assertEqual((worker / 'aa' / 'base').read_text(), 'baseline')
            self.assertFalse((worker / 'bb').exists())

    def test_finished_pass_is_reaped_before_a_disk_stop(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            cache = root / '.dev/mutcheck-gocache/run-test'
            cache.mkdir(parents=True)
            (root / 'guard').write_text('original')
            manifest = root / 'manifest.py'
            manifest.write_text('ENTRIES = []')
            logs = root / 'logs'
            logs.mkdir()
            entries = [dict(kind='go', step='1', name=name, file='guard', original='original',
                            replacement='mutated', packages=['a'], guards=['TestGuard'])
                       for name in ('first', 'second')]
            finished = {'value': False}

            def copy(source, target):
                target.mkdir(parents=True)
                shutil.copy2(source / 'guard', target / 'guard')

            def start(command, **kwargs):
                worker_logs = Path(command[command.index('--worker-logs') + 1])
                name = command[-1].split('/')[1]
                worker_logs.mkdir(parents=True, exist_ok=True)
                (worker_logs / 'results.log').write_text(
                    f'1 {name} exit=0 caught=TestGuard restored=True PASS\n')
                process = Mock(pid=12345, returncode=0)
                process.poll.side_effect = lambda: 0 if finished['value'] else None
                return process

            def disk_usage(path):
                return Mock(free=(mutchecks.DISK_HEADROOM - 1)
                            if finished['value'] else (1 << 40))

            def sleep(_):
                finished['value'] = True

            results = io.StringIO()
            with patch.object(mutchecks, 'ROOT', root), \
                    patch.dict(os.environ, {'GOCACHE': str(cache)}), \
                    patch.object(mutchecks, 'baseline', return_value=True), \
                    patch.object(mutchecks, 'copy_working_tree', side_effect=copy), \
                    patch.object(mutchecks.subprocess, 'Popen', side_effect=start), \
                    patch.object(mutchecks, 'stop_processes'), \
                    patch.object(mutchecks.shutil, 'disk_usage', side_effect=disk_usage), \
                    patch.object(mutchecks.time, 'sleep', side_effect=sleep), \
                    patch('sys.stdout', new=io.StringIO()), patch('sys.stderr', new=io.StringIO()):
                code = mutchecks.run_parallel(entries, manifest, 1, logs, results)
            self.assertEqual(code, mutchecks.DISK_EXIT)
            content = results.getvalue()
            self.assertIn('1 first exit=0 caught=TestGuard restored=True PASS', content)
            self.assertIn('1 second exit=not-run caught=- restored=True not run (environment)', content)
            self.assertNotIn('1 first exit=not-run', content)

    def test_worker_disk_exit_stops_the_queue_and_shows_real_free_space(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            init_repo(root)
            cache = root / '.dev/mutcheck-gocache/run-test'
            cache.mkdir(parents=True)
            script = root / 'backend/scripts/mutchecks.py'
            script.parent.mkdir(parents=True)
            script.write_text(
                "import os, pathlib, sys\n"
                "name = sys.argv[-1].split('/')[1]\n"
                "logs = pathlib.Path(sys.argv[sys.argv.index('--worker-logs') + 1])\n"
                "if name == 'done':\n"
                "    (logs / 'results.log').write_text('1 done exit=0 caught=TestGuard restored=True PASS\\n')\n"
                "    sys.exit(0)\n"
                "if name == 'disk':\n"
                "    sys.exit(5)\n"
                "pathlib.Path(os.environ['MUTCHECK_TEST_MARKER']).touch()\n"
                "sys.exit(0)\n")
            (root / 'guard').write_text('original')
            stage_fixture(root)
            manifest = root / 'manifest.py'
            manifest.write_text('ENTRIES = []')
            logs = root / 'logs'
            logs.mkdir()
            entries = [dict(kind='go', step='1', name=name, file='guard', original='original',
                            replacement='mutated', packages=['a'], guards=['TestGuard'])
                       for name in ('done', 'disk', 'later')]
            free = 1 << 40
            results = io.StringIO()
            with patch.object(mutchecks, 'ROOT', root), \
                    patch.dict(os.environ, {'GOCACHE': str(cache),
                                            'MUTCHECK_TEST_MARKER': str(root / 'should-not-run')}), \
                    patch.object(mutchecks, 'baseline', return_value=True), \
                    patch.object(mutchecks.shutil, 'disk_usage', return_value=Mock(free=free)), \
                    patch('sys.stdout', new=io.StringIO()), \
                    patch('sys.stderr', new=io.StringIO()) as error:
                code = mutchecks.run_parallel(entries, manifest, 1, logs, results)
            self.assertEqual(code, mutchecks.DISK_EXIT)
            self.assertIn(mutchecks.disk_message(free), error.getvalue())
            self.assertNotIn('0.0 GB', error.getvalue())
            self.assertFalse((root / 'should-not-run').exists())
            content = results.getvalue()
            self.assertIn('1 done exit=0 caught=TestGuard restored=True PASS', content)
            self.assertIn('1 disk exit=not-run caught=- restored=False FAIL', content)
            self.assertIn('1 later exit=not-run caught=- restored=True not run (environment)', content)

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
            with patch.object(mutchecks, 'affected_entries', side_effect=lambda entries, ref, **kwargs: entries), patch.object(mutchecks, 'baseline', return_value=True) as baseline, relaxed_disk_headroom():
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
            with self.subTest(signal=sig), tempfile.TemporaryDirectory(dir=mutchecks.ROOT / '.dev') as folder:
                root = Path(folder)
                init_repo(root)
                script = root / 'backend/scripts/mutchecks.py'
                script.parent.mkdir(parents=True)
                text = Path(mutchecks.__file__).read_text()
                text = text.replace('selected = affected_entries(selected, args.since, summary=summary)', 'selected = selected')
                text = text.replace('selected = affected_entries(selected, args.since)', 'selected = selected')
                script.write_text(text)
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
                with relaxed_disk_headroom():
                    env = dict(os.environ, PATH=str(root / 'bin') + os.pathsep + os.environ['PATH'])
                    process = subprocess.Popen([sys.executable, str(script), '--since', 'HEAD', '--jobs', '4', '--no-shared-pg'],
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
                    self.assertFalse((root / '.dev/mutcheck-gocache').exists())
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
            test_calls = [line for line in calls.read_text().splitlines() if line.startswith('test ')]
            self.assertTrue(test_calls)
            self.assertTrue(all('-trimpath' in line for line in test_calls), test_calls)
            self.assertEqual(query.read_text(), 'SELECT 1;')


class StandaloneCacheTests(unittest.TestCase):
    def run_shell(self, root, env, go_body=None):
        scripts = root / 'backend/scripts'
        scripts.mkdir(parents=True)
        shutil.copy(Path(__file__).with_name('mutcheck.sh'), scripts / 'mutcheck.sh')
        target = root / 'guard.go'
        target.write_text('original')
        (root / 'bin').mkdir()
        seen = root / 'seen'
        go = root / 'bin/go'
        go.write_text(go_body or (
            '#!/bin/bash\nprintf "%s\\n" "$GOCACHE" >> "' + str(seen) + '"\n'
            'if [[ $* == *"-run ^$"* || $1 == tool ]]; then exit 0; fi\n'
            "echo '--- FAIL: TestGuard (0.00s)'\nexit 1\n"))
        go.chmod(0o755)
        env = {**env, 'PATH': str(root / 'bin') + os.pathsep + env['PATH']}
        result = subprocess.run(['bash', str(scripts / 'mutcheck.sh'), str(target),
                                 'original', 'mutated', './internal/example'],
                                env=env, text=True, capture_output=True)
        return result, target, seen

    def test_uses_and_removes_a_temporary_cache_when_gocache_is_unset(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            env = {key: value for key, value in os.environ.items() if key != 'GOCACHE'}
            result, target, seen = self.run_shell(root, env)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            paths = [Path(line).resolve() for line in seen.read_text().splitlines()]
            self.assertTrue(paths)
            self.assertTrue(all(path.parent == (root / '.dev').resolve() and
                                path.name.startswith('mutcheck-gocache-standalone.')
                                for path in paths), paths)
            self.assertFalse(any(path.exists() for path in paths))
            self.assertEqual(target.read_text(), 'original')

    def test_keeps_a_caller_provided_cache_and_does_not_create_a_temporary_one(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            provided = root / 'provided-cache'
            provided.mkdir()
            env = {**os.environ, 'GOCACHE': str(provided)}
            result, target, seen = self.run_shell(root, env)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertEqual(set(seen.read_text().splitlines()), {str(provided)})
            self.assertTrue(provided.exists())
            self.assertFalse(list((root / '.dev').glob('mutcheck-gocache-standalone.*')))
            self.assertEqual(target.read_text(), 'original')

    def test_removes_temporary_cache_on_failure(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            env = {key: value for key, value in os.environ.items() if key != 'GOCACHE'}
            body = ('#!/bin/bash\nprintf "%s\\n" "$GOCACHE" >> "' + str(root / 'seen') + '"\n'
                    'if [[ $* == *"-run ^$"* ]]; then exit 1; fi\nexit 0\n')
            result, target, seen = self.run_shell(root, env, body)
            self.assertEqual(result.returncode, 2, result.stdout + result.stderr)
            self.assertIn('mutated code does not build', result.stderr)
            paths = [Path(line).resolve() for line in seen.read_text().splitlines()]
            self.assertTrue(paths)
            self.assertFalse(any(path.exists() for path in paths))
            self.assertFalse((root / '.dev/mutcheck-backup').exists())
            self.assertEqual(target.read_text(), 'original')

    def test_removes_temporary_cache_on_sigterm(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            scripts = root / 'backend/scripts'
            scripts.mkdir(parents=True)
            shutil.copy(Path(__file__).with_name('mutcheck.sh'), scripts / 'mutcheck.sh')
            target = root / 'guard.go'
            target.write_text('original')
            bin_dir = root / 'bin'
            bin_dir.mkdir()
            seen = root / 'seen'
            marker = root / 'test-running'
            go = bin_dir / 'go'
            go.write_text('#!/bin/bash\nprintf "%s\\n" "$GOCACHE" >> "' + str(seen) + '"\n'
                          'if [[ $* == *"-run ^$"* ]]; then exit 0; fi\n'
                          'touch "' + str(marker) + '"\nsleep 30\n')
            go.chmod(0o755)
            env = {key: value for key, value in os.environ.items() if key != 'GOCACHE'}
            env['PATH'] = str(bin_dir) + os.pathsep + env['PATH']
            process = subprocess.Popen(
                ['bash', str(scripts / 'mutcheck.sh'), str(target), 'original', 'mutated',
                 './internal/example'],
                env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True,
                start_new_session=True)
            try:
                deadline = time.monotonic() + 10
                while not marker.exists() and process.poll() is None and time.monotonic() < deadline:
                    time.sleep(0.02)
                self.assertTrue(marker.exists(), 'fixture never reached tests')
                paths = [Path(line).resolve() for line in seen.read_text().splitlines()]
                self.assertTrue(paths)
                os.killpg(process.pid, signal.SIGTERM)
                output = process.communicate(timeout=15)[0]
                self.assertIn(process.returncode, (143, -signal.SIGTERM), output)
                self.assertEqual(target.read_text(), 'original')
                self.assertFalse((root / '.dev/mutcheck-backup').exists())
                self.assertFalse(any(path.exists() for path in paths))
                self.assertFalse(list((root / '.dev').glob('mutcheck-gocache-standalone.*')))
            finally:
                if process.poll() is None:
                    os.killpg(process.pid, signal.SIGKILL)
                process.communicate()

    def test_restore_uses_and_removes_a_temporary_cache(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            scripts = root / 'backend/scripts'
            scripts.mkdir(parents=True)
            shutil.copy(Path(__file__).with_name('mutcheck.sh'), scripts / 'mutcheck.sh')
            query = root / 'backend/internal/db/queries/example.sql'
            query.parent.mkdir(parents=True)
            query.write_text('mutated sql\n')
            slot = root / '.dev/mutcheck-backup'
            slot.mkdir(parents=True)
            (slot / 'path').write_text(str(query) + '\n')
            (slot / 'original').write_text('original sql\n')
            bin_dir = root / 'bin'
            bin_dir.mkdir()
            seen = root / 'seen'
            go = bin_dir / 'go'
            go.write_text('#!/bin/bash\nprintf "%s\\n" "$GOCACHE" >> "' + str(seen) + '"\nexit 0\n')
            go.chmod(0o755)
            env = {key: value for key, value in os.environ.items() if key != 'GOCACHE'}
            env['PATH'] = str(bin_dir) + os.pathsep + env['PATH']
            result = subprocess.run(['bash', str(scripts / 'mutcheck.sh'), '--restore'],
                                    env=env, text=True, capture_output=True)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertEqual(result.stdout.strip(), f'restored: {query}')
            self.assertEqual(query.read_text(), 'original sql\n')
            self.assertFalse(slot.exists())
            paths = [Path(line).resolve() for line in seen.read_text().splitlines()]
            self.assertTrue(paths)
            self.assertTrue(all(path.parent == (root / '.dev').resolve() and
                                path.name.startswith('mutcheck-gocache-standalone.')
                                for path in paths), paths)
            self.assertFalse(any(path.exists() for path in paths))
            self.assertNotIn((Path.home() / '.cache/go-build').resolve(), paths)


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

                with patch.object(mutchecks, 'affected_entries', return_value=[entry]), patch.object(mutchecks, 'baseline', return_value=True), relaxed_disk_headroom():
                    status, output, _ = AllManifestTests().invoke(root, '--since', 'main')
                self.assertEqual(status, 1)
                self.assertIn('exit=4 caught=- restored=True not run (environment)', output)
                self.assertIn('.dev/codex/a/manifest.py selected=1 passed=0 failed=0 environment=1', output)
                self.assertIn('total selected=1 passed=0 failed=0 environment=1', output)

    def test_entry_exit_two_stays_failed_even_with_environment_signature(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            init_repo(root)
            scripts = root / 'backend/scripts'
            scripts.mkdir(parents=True)
            (scripts / 'mutchecks.py').write_text(Path(mutchecks.__file__).read_text())
            worker = scripts / 'mutcheck.sh'
            worker.write_text(
                '#!' + sys.executable + '\nimport sys\n' +
                f'print({mutchecks.ENVIRONMENT_SIGNATURE!r})\n' +
                "print('mutcheck: mutated migration does not apply:')\n" +
                "print('    test.go:1: migrate up: boom')\n" +
                'sys.exit(2)\n')
            worker.chmod(0o755)
            (root / 'guard').write_text('original')
            stage_fixture(root)
            manifest = root / '.dev/codex/a/manifest.py'
            manifest.parent.mkdir(parents=True)
            entry = dict(kind='go', step='1', name='guard', file='guard', guards=['TestGuard'],
                         packages=['a'], original='original', replacement='mutated')
            manifest.write_text('ENTRIES = ' + repr([entry]))

            with patch.object(mutchecks, 'affected_entries', return_value=[entry]), \
                    patch.object(mutchecks, 'baseline', return_value=True), relaxed_disk_headroom():
                status, output, _ = AllManifestTests().invoke(root, '--since', 'main')
            self.assertEqual(status, 1)
            self.assertIn('exit=2 caught=- restored=True FAIL', output)
            self.assertIn('.dev/codex/a/manifest.py selected=1 passed=0 failed=1 environment=0', output)
            self.assertNotIn('not run (environment)', output)
            self.assertEqual((root / 'guard').read_text(), 'original')

    def test_baseline_environment_counts_whole_unrun_manifest(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            manifest = root / '.dev/codex/a/manifest.py'
            manifest.parent.mkdir(parents=True)
            entries = [dict(kind='go', step='1', name=name, guards=['TestGuard'], packages=['a']) for name in ('one', 'two')]
            manifest.write_text('ENTRIES = ' + repr(entries))

            def baseline(entries, logs, results, **kwargs):
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

    def test_shell_classifies_mutated_migration_apply_as_build_error(self):
        cases = [
            ('backend/internal/db/migrations/00099_example.sql', 'migrate up', 2, 1, True),
            ('backend/internal/db/migrations/00099_example.sql', 'connection refused', 4, 2, False),
            ('guard.go', 'migrate up', 4, 2, False),
        ]
        for target_name, detail, expected, attempts, classified in cases:
            with self.subTest(target=target_name, detail=detail), tempfile.TemporaryDirectory() as folder:
                root = Path(folder)
                scripts = root / 'backend/scripts'
                scripts.mkdir(parents=True)
                shutil.copy(Path(__file__).with_name('mutcheck.sh'), scripts / 'mutcheck.sh')
                target = root / target_name
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_text('original')
                (root / 'bin').mkdir()
                marker = root / 'attempts'
                go = root / 'bin/go'
                go.write_text('#!/bin/bash\n' +
                              'if [[ $* == *"-run ^$"* ]]; then exit 0; fi\n' +
                              f'echo attempt >> {str(marker)!r}\n' +
                              f'echo {mutchecks.ENVIRONMENT_SIGNATURE!r} >&2\n' +
                              f'echo "    test.go:1: {detail}" >&2\n' +
                              'exit 1\n')
                go.chmod(0o755)
                result = subprocess.run(
                    ['bash', str(scripts / 'mutcheck.sh'), str(target), 'original', 'mutated',
                     './internal/example'],
                    env={**os.environ, 'PATH': str(root / 'bin') + os.pathsep + os.environ['PATH']},
                    text=True, capture_output=True)
                content = result.stdout + result.stderr
                self.assertEqual(result.returncode, expected, content)
                self.assertEqual(marker.read_text(), 'attempt\n' * attempts)
                self.assertEqual('mutcheck: mutated migration does not apply:' in content, classified)
                self.assertEqual('not run (environment)' in content, not classified)
                self.assertEqual(target.read_text(), 'original')
                self.assertFalse((root / '.dev/mutcheck-backup').exists())

    def test_shell_exits_environment_when_shared_server_is_unreachable(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            scripts = root / 'backend/scripts'
            scripts.mkdir(parents=True)
            shutil.copy(Path(__file__).with_name('mutcheck.sh'), scripts / 'mutcheck.sh')
            target = root / 'guard.go'
            target.write_text('original')
            result = subprocess.run(
                ['bash', str(scripts / 'mutcheck.sh'), str(target), 'original', 'mutated', './internal/example'],
                env={**os.environ, 'SALON_TEST_PG_DSN': 'postgres://salon:salon@127.0.0.1:1/salon'},
                text=True, capture_output=True)
            self.assertEqual(result.returncode, 4, result.stdout + result.stderr)
            self.assertIn('not run (environment)', result.stderr)
            self.assertEqual(target.read_text(), 'original')

    def test_shell_reprobes_after_a_failed_mutated_run(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            scripts = root / 'backend/scripts'
            scripts.mkdir(parents=True)
            shutil.copy(Path(__file__).with_name('mutcheck.sh'), scripts / 'mutcheck.sh')
            target = root / 'guard.go'
            target.write_text('original')
            (root / 'bin').mkdir()
            marker = root / 'running'
            go = root / 'bin/go'
            go.write_text('#!/bin/bash\n'
                          'if [[ $* == *"-run ^$"* ]]; then exit 0; fi\n'
                          f'touch {str(marker)!r}\n'
                          'sleep 1\n'
                          "echo '--- FAIL: TestGuard (0.00s)'\n"
                          'exit 1\n')
            go.chmod(0o755)
            listener = socket.socket()
            listener.bind(('127.0.0.1', 0))
            listener.listen()
            port = listener.getsockname()[1]
            process = subprocess.Popen(
                ['bash', str(scripts / 'mutcheck.sh'), str(target), 'original', 'mutated', './internal/example'],
                env={**os.environ, 'PATH': str(root / 'bin') + os.pathsep + os.environ['PATH'],
                     'SALON_TEST_PG_DSN': f'postgres://salon:salon@127.0.0.1:{port}/salon'},
                text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            try:
                deadline = time.monotonic() + 10
                while not marker.exists() and process.poll() is None and time.monotonic() < deadline:
                    time.sleep(0.02)
                self.assertTrue(marker.exists(), 'mutated test never ran')
                listener.close()
                output, error = process.communicate(timeout=15)
            finally:
                if process.poll() is None:
                    process.kill()
                process.communicate()
                listener.close()
            self.assertEqual(process.returncode, 4, output + error)
            self.assertIn('became unreachable', error)
            self.assertEqual(target.read_text(), 'original')

if __name__ == '__main__':
    unittest.main()

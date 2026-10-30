#!/usr/bin/env python3
"""按清单验证保护：mutchecks.py [manifest.py] [--since <git-ref>] [--locate] [--jobs N] [names...]。

清单的条目必须原地改写，不允许执行代码。
--jobs N（默认 1）将 go 项按清单顺序轮流分配到 .dev/mutcheck-worktrees/<n>/ 的
临时文件副本；各包的守护测试基线在主工作树至多 N 个并发运行，通过后才创建副本并运行变异。
e2e 在所有 go 项之后于主工作树串行运行。副本包含磁盘上的跟踪文件和未忽略的未跟踪文件，
首次变异前逐字节核对所有保护文件；只读 git 命令列出文件，不写 .git。
开始时清理残留副本，结束、失败、Ctrl-C 或 SIGTERM 时删除副本目录。
清单是定义 ENTRIES 的 Python 文件：它是由字典组成的列表；可选的 SKIPPED
用 (step, name, reason) 元组列出跳过的保护及原因。
每项包含 kind（go 或 e2e）、step、name、file（相对仓库根目录）、original、
replacement 和 guards（必须全部命中的测试名称或标题）。go 项另有 packages，
即相对 backend/ 的 go test 包参数；guards 是顶层测试名，go 项只运行这些测试（含子测试）。
e2e 项另有 spec，即相对 admin/ 的测试文件，
并在 default 项目中按完整标题选择测试。名称可用裸名称或 step/name。
原文必须恰好出现一次。--locate 只检查匹配，不修改文件、不运行测试。
--since 将名称选择进一步缩小到 git-ref 与工作树（含未跟踪文件）之间受影响的项：
保护文件、所在包中定义 guard 的 _test.go，或 e2e 的 spec 改动都会选中；
查询或迁移目录中任意文件改动也会选中该目录的保护。选中名称逐行输出到 stderr。
无 --since 的 --locate 始终检查名称选择范围（不指定名称时为整份清单）。
省略清单时必须带 --locate，按路径排序检查 .dev/codex/*/manifest.py 的全部清单，
逐份输出选中、通过、失败数量；任一项定位失败就非零退出。
无清单形式为 mutchecks.py --locate [--since <git-ref>]，不接受名称。
正式运行先按包合并选中 go 项的 guards，以 go test -count=1 -run '^(GuardA|GuardB|...)$'
运行这些顶层守护测试（含子测试）；--jobs 1 串行，N > 1 时主工作树至多 N 个包并发，全部通过后
才允许任何变异；基线失败时打印测试输出并退出，避免把已有故障算成保护生效。
每次运行在清单旁新建日志目录，保存完整命令、测试输出和所有结果行 results.log。
go 项要求 mutcheck.sh 退出 0 且命中所有 guards；e2e 项要求守护断言失败，
还原后同一测试通过。每个副本使用自身的 .dev/mutcheck-backup/，主工作树的 go/e2e
仍共用主槽；没有 original 的残留槽可以安全清理。中断后用 mutcheck.sh --restore 恢复已发布的备份。
"""
import argparse
import ast
import io
import json
import os
from pathlib import Path
import re
import shutil
import signal
import shlex
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parents[2]
GENERATED = ROOT / 'backend/internal/db/dbgen'
SLOT = ROOT / '.dev/mutcheck-backup'

def load_manifest(path):
    """Interpret literal data without executing manifest code."""
    values = {}
    try:
        tree = ast.parse(path.read_text(), filename=str(path))
    except SyntaxError as exc:
        raise ValueError(f'{path}:{exc.lineno}: {exc.msg}') from exc
    for index, node in enumerate(tree.body):
        try:
            if index == 0 and isinstance(node, ast.Expr) and isinstance(node.value, ast.Constant) and isinstance(node.value.value, str):
                continue
            if isinstance(node, ast.Assign) and len(node.targets) == 1 and isinstance(node.targets[0], ast.Name) and node.targets[0].id in ('ENTRIES', 'SKIPPED'):
                values[node.targets[0].id] = ast.literal_eval(node.value)
                continue
            if isinstance(node, ast.AugAssign) and isinstance(node.op, ast.Add) and isinstance(node.target, ast.Name) and node.target.id in ('ENTRIES', 'SKIPPED'):
                values[node.target.id] += ast.literal_eval(node.value)
                continue
            if isinstance(node, ast.Expr) and isinstance(node.value, ast.Call):
                call = node.value
                if isinstance(call.func, ast.Attribute) and isinstance(call.func.value, ast.Name) and call.func.value.id in ('ENTRIES', 'SKIPPED') and call.func.attr in ('append', 'extend') and len(call.args) == 1 and not call.keywords:
                    getattr(values[call.func.value.id], call.func.attr)(ast.literal_eval(call.args[0]))
                    continue
        except (ValueError, TypeError, KeyError, AttributeError):
            pass
        raise ValueError(f'{path}:{node.lineno}: only literal ENTRIES/SKIPPED statements are allowed; edit the entry in place')
    return values


def partition_entries(entries, jobs):
    """Balance Go entries round-robin, keeping e2e serial in manifest order."""
    go = [entry for entry in entries if entry['kind'] == 'go']
    serial = [entry for entry in entries if entry['kind'] == 'e2e']
    return [go[index::jobs] for index in range(min(jobs, len(go)))], serial


def copy_working_tree(source, target):
    """Copy on-disk tracked and non-ignored untracked files using read-only Git."""
    files = subprocess.check_output(
        ['git', 'ls-files', '-z', '--cached', '--others', '--exclude-standard'], cwd=source)
    target.mkdir(parents=True, exist_ok=True)
    for name in sorted(set(files.split(b'\0')) - {b''}):
        relative = Path(os.fsdecode(name))
        src, dst = source / relative, target / relative
        if src.is_symlink():
            dst.parent.mkdir(parents=True, exist_ok=True)
            dst.symlink_to(os.readlink(src))
        elif src.is_file():
            dst.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(src, dst)


def merge_results(entries, lines):
    return [line for entry in entries for line in lines
            if line.startswith(entry['step'] + ' ' + entry['name'] + ' exit=')]


def cleanup_workers(folder):
    if folder.exists():
        shutil.rmtree(folder)


def stop_processes(processes):
    # Kill the whole session, including go test and its descendants, before
    # removing any worker directory. The worker itself may have exited already.
    for process in processes:
        try:
            os.killpg(process.pid, signal.SIGTERM)
        except ProcessLookupError:
            pass
    deadline = time.monotonic() + 5
    for process in processes:
        try:
            process.wait(timeout=max(0, deadline - time.monotonic()))
        except subprocess.TimeoutExpired:
            pass
    for process in processes:
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        process.wait()
    processes.clear()


def run_parallel(selected, manifest, jobs, logs, results):
    folder = ROOT / '.dev/mutcheck-worktrees'
    processes = []
    handles = []
    workers, serial = partition_entries(selected, jobs)
    previous = {}

    def interrupted(signum, frame):
        raise KeyboardInterrupt

    try:
        for sig in (signal.SIGINT, signal.SIGTERM):
            previous[sig] = signal.signal(sig, interrupted)
        cleanup_workers(folder)
        if not baseline(selected, logs, results, jobs=jobs):
            return 1
        folder.mkdir(parents=True)
        locations = []
        for index, entries in enumerate(workers):
            path = folder / str(index)
            copy_working_tree(ROOT, path)
            for filename in dict.fromkeys(e['file'] for e in selected):
                if (ROOT / filename).read_bytes() != (path / filename).read_bytes():
                    raise ValueError(f'worker copy differs: {filename}')
            worker_logs = logs / f'worker-{index}'
            worker_logs.mkdir()
            locations.append((path, entries, worker_logs))
        mutation_lines = []
        for path, entries, worker_logs in locations:
            output = (worker_logs / 'worker.log').open('w')
            handles.append(output)
            command = [sys.executable, str(path / 'backend/scripts/mutchecks.py'), str(manifest),
                       '--worker-logs', str(worker_logs),
                       *[e['step'] + '/' + e['name'] for e in entries]]
            processes.append(subprocess.Popen(command, cwd=path, stdout=output,
                                               stderr=subprocess.STDOUT, start_new_session=True))
        while any(p.poll() is None for p in processes):
            time.sleep(0.1)
        failed = any(p.returncode != 0 for p in processes)
        stop_processes(processes)
        for _, entries, worker_logs in locations:
            result = worker_logs / 'results.log'
            if result.exists():
                mutation_lines.extend(result.read_text().splitlines())
            for entry in entries:
                for suffix in ('.log', '.command.json'):
                    artifact = worker_logs / (entry['step'] + '-' + entry['name'] + suffix)
                    if artifact.exists():
                        artifact.rename(logs / artifact.name)
                # A worker crash must not silently omit selected entries.
                if not merge_results([entry], mutation_lines):
                    mutation_lines.append(f"{entry['step']} {entry['name']} exit=not-run caught=- restored=False FAIL")
                    failed = True
        for entry in serial:
            buffer = io.StringIO()
            passed, restored = run_e2e(entry, logs, buffer)
            mutation_lines.extend(buffer.getvalue().splitlines())
            failed |= not passed
            if not restored:
                break
        # e2e executes last even when its entries precede Go in the manifest.
        # Rewrite only once all results are available to retain manifest order.
        for line in merge_results(selected, mutation_lines):
            result_line(line, results)
        return int(failed)
    except (OSError, ValueError, subprocess.CalledProcessError) as exc:
        print(str(exc), file=sys.stderr)
        return 1
    finally:
        # Further signals must not interrupt cleanup itself.
        for sig in previous:
            signal.signal(sig, signal.SIG_IGN)
        try:
            stop_processes(processes)
            for handle in handles:
                handle.close()
            cleanup_workers(folder)
        finally:
            for sig, handler in previous.items():
                signal.signal(sig, handler)


def select_entries(entries, names):
    unknown = set(names) - {e['name'] for e in entries} - {e['step'] + '/' + e['name'] for e in entries}
    if unknown:
        raise ValueError('unknown entries: ' + ', '.join(sorted(unknown)))
    return [e for e in entries if not names or e['name'] in names or e['step'] + '/' + e['name'] in names]


def affected_entries(entries, ref):
    changed = set()
    for command in (['git', 'diff', '--name-only', '-z', ref, '--'],
                    ['git', 'ls-files', '--others', '--exclude-standard', '-z']):
        result = subprocess.run(command, cwd=ROOT, capture_output=True, check=True)
        changed.update(os.fsdecode(path) for path in result.stdout.split(b'\0') if path)
    changed_dirs = {str(Path(path).parent) for path in changed}
    tests = {}
    for path in changed:
        if path.endswith('_test.go') and (ROOT / path).is_file():
            tests[ROOT / Path(path).parent] = tests.get(ROOT / Path(path).parent, set()) | set(
                re.findall(r'^\s*func\s+(\w+)\s*\(', (ROOT / path).read_text(), re.M))
    package_dirs = {}
    selected = []
    env = dict(os.environ)
    env.pop('GOFLAGS', None)
    for entry in entries:
        path = entry['file']
        affected = path in changed
        if path.startswith(('backend/internal/db/queries/', 'backend/internal/db/migrations/')):
            affected |= str(Path(path).parent) in changed_dirs
        if entry['kind'] == 'e2e':
            affected |= 'admin/' + entry['spec'] in changed
        elif not affected and tests:
            for package in entry['packages']:
                if package not in package_dirs:
                    result = subprocess.run(['go', 'list', '-f', '{{.Dir}}', package],
                                            cwd=ROOT / 'backend', env=env,
                                            capture_output=True, text=True, check=True)
                    package_dirs[package] = [Path(line) for line in result.stdout.splitlines()]
                affected |= any(set(entry['guards']) & tests.get(directory, set())
                                for directory in package_dirs[package])
        if affected:
            selected.append(entry)
    return selected


def locate(entry):
    source = (ROOT / entry['file']).read_bytes().decode('utf-8')
    count = source.count(entry['original'])
    if count != 1:
        raise ValueError(f"original occurs {count} times in {entry['file']}")
    if entry['original'] == entry['replacement']:
        raise ValueError('mutation makes no change')


def snapshot(entry):
    paths = {ROOT / entry['file']}
    # SQL 变异和还原都会重新生成此目录。
    if entry['file'].startswith('backend/internal/db/queries/'):
        paths.update(GENERATED.glob('*.go'))
    return {path: path.read_bytes() for path in paths}


def same_bytes(before, entry):
    if entry['file'].startswith('backend/internal/db/queries/'):
        if set(GENERATED.glob('*.go')) != set(before) - {ROOT / entry['file']}:
            return False
    return all(path.exists() and path.read_bytes() == content for path, content in before.items())


def restore(before, entry):
    # 还原被中断时用快照兜底；即使兜底成功也记为 FAIL，脚本自身必须准确还原。
    for path, content in before.items():
        if not path.exists() or path.read_bytes() != content:
            path.write_bytes(content)
    if entry['file'].startswith('backend/internal/db/queries/'):
        for path in set(GENERATED.glob('*.go')) - set(before):
            path.unlink()


def result_line(line, results):
    if not isinstance(results, io.StringIO):
        print(line, flush=True)
    results.write(line + '\n')
    results.flush()


def guard_pattern(guards):
    return '^(' + '|'.join(re.escape(guard) for guard in guards) + ')$'


def baseline(entries, logs, results, jobs=1):
    packages = {}
    for entry in entries:
        if entry['kind'] == 'go':
            for package in entry['packages']:
                packages.setdefault(package, {}).update(dict.fromkeys(entry['guards']))
    commands = [['go', 'test', '-count=1', '-run', guard_pattern(guards), package]
                for package, guards in packages.items()]
    env = dict(os.environ)
    env.pop('GOFLAGS', None)
    processes, handles, running, codes = [], [], {}, {}
    next_index = 0
    failed = False
    try:
        while next_index < len(commands) or running:
            while next_index < len(commands) and len(running) < jobs and not failed:
                index = next_index
                next_index += 1
                command = commands[index]
                log = logs / f'baseline-{index}.log'
                (logs / f'baseline-{index}.command.json').write_text(
                    json.dumps(command, ensure_ascii=False, indent=2) + '\n')
                output = log.open('w')
                handles.append(output)
                try:
                    process = subprocess.Popen(command, cwd=ROOT / 'backend', env=env,
                                               stdout=output, stderr=subprocess.STDOUT,
                                               start_new_session=True)
                    processes.append(process)
                    running[index] = process
                except OSError as exc:
                    output.write(str(exc) + '\n')
                    codes[index] = 2
                    failed = True
            for index, process in list(running.items()):
                code = process.poll()
                if code is not None:
                    codes[index] = code
                    del running[index]
                    failed |= code != 0
            if failed:
                break
            if running:
                time.sleep(0.05)
    finally:
        # Cancel all baseline descendants on failure or interruption; no worker
        # may start until all successful baseline processes have finished.
        stop_processes(processes)
        for handle in handles:
            handle.close()
    for index, package in enumerate(packages):
        if index in codes:
            code = codes[index]
            result_line(f"baseline {package} exit={code} {'PASS' if code == 0 else 'FAIL'}", results)
            if code:
                print((logs / f'baseline-{index}.log').read_text(errors='replace'), file=sys.stderr, end='')
    return not failed


def run_go(entry, logs, results):
    code, caught, restored, error = 'not-run', [], True, None
    before = {}
    try:
        locate(entry)
        before = snapshot(entry)
        guard_regex = guard_pattern(entry['guards'])
        command = ['backend/scripts/mutcheck.sh', '--run', guard_regex,
                   entry['file'], entry['original'], entry['replacement'], *entry['packages']]
        log = logs / (entry['step'] + '-' + entry['name'] + '.log')
        (logs / (entry['step'] + '-' + entry['name'] + '.command.json')).write_text(json.dumps(command, ensure_ascii=False, indent=2) + '\n')
        # 清除继承的 GOFLAGS，避免它悄悄过滤守护测试。
        env = dict(os.environ)
        env.pop('GOFLAGS', None)
        with log.open('w') as output:
            code = subprocess.run(command, cwd=ROOT, env=env, stdout=output, stderr=subprocess.STDOUT).returncode
        content = log.read_text(errors='replace')
        if 'caught by:\n' in content:
            caught = re.findall(r'^--- FAIL: ([^\s(]+)', content.split('caught by:\n', 1)[1], re.M)
    except (OSError, ValueError) as exc:
        error = str(exc)
    finally:
        if before:
            restored = same_bytes(before, entry)
            if not restored:
                restore(before, entry)
    passed = code == 0 and set(entry['guards']).issubset(caught) and restored and error is None
    result_line(f"{entry['step']} {entry['name']} exit={code} caught={','.join(caught) or '-'} restored={restored} {'PASS' if passed else 'FAIL'}", results)
    if error:
        print(error, file=sys.stderr)
    return passed, restored


def phase(entry, folder, label):
    """在同一个 shell 中重置环境、运行 default 项目的守护测试并停止环境。"""
    relative = str(folder)
    prefix = shlex.quote(relative + '/' + label)
    # Playwright 同时匹配项目、文件和标题，因此锚定标题后缀。
    # 下面还会核对报告中的完整标题和测试数量。
    test_args = shlex.join([entry['spec'], '--project=default', '--no-deps',
                            '--grep', '(' + '|'.join(re.escape(g) for g in entry['guards']) + ')$', '--reporter=json'])
    command = (
        f'L={prefix}; if scripts/dev.sh reset > "$L-reset.log" 2>&1; then '
        f'(cd admin && npm run e2e -- {test_args} > "$L-e2e.log" 2>&1); '
        's=$?; echo "e2e exit=$s"; else s=$?; echo "reset failed exit=$s"; fi; '
        'scripts/dev.sh down > "$L-down.log" 2>&1; (exit $s)'
    )
    (folder / (label + '.command.sh')).write_text(command + '\n')
    with (folder / (label + '-command.log')).open('w') as output:
        env = dict(os.environ, npm_config_update_notifier='false')
        process = subprocess.Popen(['bash', '-c', command], cwd=ROOT, env=env,
                                   stdout=output, stderr=subprocess.STDOUT, start_new_session=True)
        try:
            code = process.wait()
        except BaseException:
            os.killpg(process.pid, signal.SIGTERM)
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait()
            raise
    command_output = (folder / (label + '-command.log')).read_text()
    test_log = folder / (label + '-e2e.log')
    if 'e2e exit=' not in command_output or not test_log.exists():
        return code, []
    content = test_log.read_text(errors='replace')
    # npm 会在 Playwright 的 JSON 报告前打印脚本信息。
    position = content.find('{')
    if position < 0:
        return code, []
    try:
        report, _ = json.JSONDecoder().raw_decode(content, position)
    except json.JSONDecodeError:
        return code, []
    specs = []

    def visit(suite):
        specs.extend(suite.get('specs', []))
        for child in suite.get('suites', []):
            visit(child)

    visit(report)
    tests = [(spec, test) for spec in specs for test in spec.get('tests', [])]
    if report.get('errors') or len(tests) != len(entry['guards']):
        return code, []
    caught = []
    for spec, test in tests:
        title = spec.get('title')
        results = test.get('results', [])
        if title not in entry['guards'] or test.get('projectName') != 'default' or not results:
            return code, []
        result = results[-1]
        if label == 'restored':
            valid = code == 0 and test.get('status') == 'expected' and result.get('status') == 'passed'
        else:
            assertion = any(error.get('matcherResult') or 'expect(' in error.get('message', '')
                            for error in result.get('errors', []))
            valid = code == 1 and test.get('status') == 'unexpected' and result.get('status') == 'failed' and assertion
        if valid:
            caught.append(title)
    return code, caught


def run_e2e(entry, logs, results):
    path = ROOT / entry['file']
    before = None
    mutated_code, restored_code = 'not-run', 'not-run'
    caught, clean, error, owned = [], [], None, False
    folder = logs / (entry['step'] + '-' + entry['name'])
    folder.mkdir()
    try:
        before = path.read_bytes()
        locate(entry)
        SLOT.parent.mkdir(parents=True, exist_ok=True)
        if SLOT.is_dir() and not (SLOT / 'original').is_file():
            with (folder / 'discard.log').open('w') as output:
                discarded = subprocess.run([str(ROOT / 'backend/scripts/mutcheck.sh'), '--restore'],
                                           cwd=ROOT, stdout=output, stderr=subprocess.STDOUT)
            if discarded.returncode:
                raise ValueError('cannot discard incomplete slot; see discard.log')
            print((folder / 'discard.log').read_text(), end='', flush=True)
        try:
            SLOT.mkdir()
        except FileExistsError:
            target = (SLOT / 'path').read_text() if (SLOT / 'path').exists() else 'unknown'
            raise ValueError(f'backup occupied for {target.strip()}; restore with: {ROOT}/backend/scripts/mutcheck.sh --restore')
        owned = True
        (SLOT / 'path').write_text(str(path.resolve()) + '\n')
        (SLOT / 'original.tmp').write_bytes(before)
        (SLOT / 'original.tmp').rename(SLOT / 'original')
        try:
            path.write_bytes(before.replace(entry['original'].encode(), entry['replacement'].encode()))
            mutated_code, caught = phase(entry, folder, 'mutated')
        finally:
            # 还原后的测试也占用备份槽，防止另一项变异干扰验证。
            path.write_bytes(before)
        restored_code, clean = phase(entry, folder, 'restored')
    except (OSError, ValueError) as exc:
        error = str(exc)
    finally:
        restored = before is None or path.read_bytes() == before
        if owned:
            with (folder / 'restore.log').open('w') as output:
                result = subprocess.run([str(ROOT / 'backend/scripts/mutcheck.sh'), '--restore'],
                                        cwd=ROOT, stdout=output, stderr=subprocess.STDOUT)
            if result.returncode:
                error = 'restore failed; see restore.log'
            restored = restored and path.read_bytes() == before
    passed = set(entry['guards']).issubset(caught) and set(entry['guards']).issubset(clean) and restored and error is None
    result_line(f"{entry['step']} {entry['name']} exit={mutated_code}/{restored_code} caught={','.join(caught) or '-'} restored={restored} {'PASS' if passed else 'FAIL'}", results)
    if error:
        print(error, file=sys.stderr)
    return passed, restored


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('manifest', type=Path, nargs='?')
    parser.add_argument('--jobs', type=int, default=1)
    parser.add_argument('--worker-logs', type=Path, help=argparse.SUPPRESS)
    parser.add_argument('--locate', action='store_true')
    parser.add_argument('--since', metavar='git-ref')
    parser.add_argument('names', nargs='*')
    args = parser.parse_intermixed_args()
    if args.jobs < 1:
        parser.error('--jobs must be positive')
    if args.manifest is None:
        if args.names:
            parser.error('names are not supported without a manifest')
        if not args.locate:
            parser.error('manifest is required without --locate')
        failed = False
        for manifest in sorted((ROOT / '.dev/codex').glob('*/manifest.py')):
            try:
                entries = load_manifest(manifest)['ENTRIES']
                selected = entries
                if args.since is not None:
                    selected = affected_entries(selected, args.since)
            except (OSError, ValueError, KeyError, subprocess.CalledProcessError) as exc:
                print(f'{manifest.relative_to(ROOT)}: {exc}', file=sys.stderr)
                failed = True
                continue
            errors = 0
            for entry in selected:
                try:
                    locate(entry)
                except (OSError, ValueError) as exc:
                    print(f"{manifest.relative_to(ROOT)} {entry['step']}/{entry['name']}: {exc}", file=sys.stderr)
                    errors += 1
            print(f'{manifest.relative_to(ROOT)} selected={len(selected)} passed={len(selected) - errors} failed={errors}', flush=True)
            failed |= errors > 0
        return int(failed)
    manifest = args.manifest.resolve()
    try:
        entries = load_manifest(manifest)['ENTRIES']
        selected = select_entries(entries, args.names)
        if args.since is not None:
            selected = affected_entries(selected, args.since)
    except subprocess.CalledProcessError as exc:
        parser.error(exc.stderr.decode(errors='replace') if isinstance(exc.stderr, bytes) else exc.stderr)
    except (OSError, ValueError, KeyError) as exc:
        parser.error(str(exc))
    for entry in selected:
        print(entry['step'] + '/' + entry['name'], file=sys.stderr)
    if not args.locate and not args.worker_logs and (ROOT / '.dev/mutcheck-worktrees').exists():
        cleanup_workers(ROOT / '.dev/mutcheck-worktrees')
    if not selected:
        print('no affected entries', file=sys.stderr)
        return 0
    logs = args.worker_logs or Path(tempfile.mkdtemp(prefix='mutchecks-', dir=manifest.parent))
    print(f'logs={logs}', file=sys.stderr)
    failed = False
    with (logs / 'results.log').open('w') as results:
        if args.jobs > 1 and not args.locate:
            try:
                return run_parallel(selected, manifest, args.jobs, logs, results)
            except KeyboardInterrupt:
                return 130
        if not args.locate and not args.worker_logs and not baseline(selected, logs, results):
            return 1
        for entry in selected:
            if args.locate:
                try:
                    locate(entry)
                    passed = True
                except (OSError, ValueError) as exc:
                    passed = False
                    print(exc, file=sys.stderr)
                line = f"{entry['step']} {entry['name']} exit={0 if passed else 2} caught=- restored=True {'PASS' if passed else 'FAIL'}"
                result_line(line, results)
                restored = True
            else:
                passed, restored = (run_go if entry['kind'] == 'go' else run_e2e)(entry, logs, results)
            failed |= not passed
            if not restored:
                break
    return int(failed)


if __name__ == '__main__':
    sys.exit(main())

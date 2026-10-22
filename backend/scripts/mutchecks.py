#!/usr/bin/env python3
"""按清单验证保护：mutchecks.py <manifest.py> [--locate] [names...]。

清单是定义 ENTRIES 的 Python 文件：它是由字典组成的列表；可选的 SKIPPED
用 (step, name, reason) 元组列出跳过的保护及原因。
每项包含 kind（go 或 e2e）、step、name、file（相对仓库根目录）、original、
replacement 和 guards（必须全部命中的测试名称或标题）。go 项另有 packages，
即相对 backend/ 的 go test 包参数；e2e 项另有 spec，即相对 admin/ 的测试文件，
并在 default 项目中按完整标题选择测试。名称可用裸名称或 step/name。
原文必须恰好出现一次。--locate 只检查匹配，不修改文件、不运行测试。
正式运行先对选中 go 项的每个不同包执行一次 go test -count=1，全部通过后
才允许任何变异；基线失败时打印测试输出并退出，避免把已有故障算成保护生效。
每次运行在清单旁新建日志目录，保存完整命令、测试输出和所有结果行 results.log。
go 项要求 mutcheck.sh 退出 0 且命中所有 guards；e2e 项要求守护断言失败，
还原后同一测试通过。两者共用 .dev/mutcheck-backup/；没有 original 的残留槽
可以安全清理。中断后用 mutcheck.sh --restore 恢复已发布的备份。
"""
import argparse
import json
import os
from pathlib import Path
import re
import runpy
import shlex
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[2]
GENERATED = ROOT / 'backend/internal/db/dbgen'
SLOT = ROOT / '.dev/mutcheck-backup'

def select_entries(entries, names):
    unknown = set(names) - {e['name'] for e in entries} - {e['step'] + '/' + e['name'] for e in entries}
    if unknown:
        raise ValueError('unknown entries: ' + ', '.join(sorted(unknown)))
    return [e for e in entries if not names or e['name'] in names or e['step'] + '/' + e['name'] in names]


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
    print(line, flush=True)
    results.write(line + '\n')
    results.flush()


def baseline(entries, logs, results):
    packages = dict.fromkeys(package for entry in entries if entry['kind'] == 'go'
                             for package in entry['packages'])
    env = dict(os.environ)
    env.pop('GOFLAGS', None)
    for index, package in enumerate(packages):
        command = ['go', 'test', '-count=1', package]
        log = logs / f'baseline-{index}.log'
        (logs / f'baseline-{index}.command.json').write_text(
            json.dumps(command, ensure_ascii=False, indent=2) + '\n')
        try:
            with log.open('w') as output:
                code = subprocess.run(command, cwd=ROOT / 'backend', env=env,
                                      stdout=output, stderr=subprocess.STDOUT).returncode
        except OSError as exc:
            log.write_text(str(exc) + '\n')
            code = 2
        result_line(f"baseline {package} exit={code} {'PASS' if code == 0 else 'FAIL'}", results)
        if code:
            print(log.read_text(errors='replace'), file=sys.stderr, end='')
            return False
    return True


def run_go(entry, logs, results):
    code, caught, restored, error = 'not-run', [], True, None
    before = {}
    try:
        locate(entry)
        before = snapshot(entry)
        command = ['backend/scripts/mutcheck.sh', entry['file'], entry['original'], entry['replacement'], *entry['packages']]
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
        code = subprocess.run(['bash', '-c', command], cwd=ROOT, stdout=output, stderr=subprocess.STDOUT).returncode
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
        report = json.loads(content[position:])
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
    parser.add_argument('manifest', type=Path)
    parser.add_argument('--locate', action='store_true')
    parser.add_argument('names', nargs='*')
    args = parser.parse_intermixed_args()
    manifest = args.manifest.resolve()
    try:
        entries = runpy.run_path(str(manifest))['ENTRIES']
        selected = select_entries(entries, args.names)
    except (OSError, ValueError, KeyError) as exc:
        parser.error(str(exc))
    logs = Path(tempfile.mkdtemp(prefix='mutchecks-', dir=manifest.parent))
    print(f'logs={logs}', file=sys.stderr)
    failed = False
    with (logs / 'results.log').open('w') as results:
        if not args.locate and not baseline(selected, logs, results):
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

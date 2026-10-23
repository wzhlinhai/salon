#!/usr/bin/env python3
"""Run a gate and keep its complete output and exit status for review.

Usage: scripts/gate.py <log-dir> <label> [--cwd <dir>] -- <command...>
Example: scripts/gate.py .dev/codex/my-task lint --cwd admin -- npm run lint

Paths are relative to the directory where you invoke this script. The command
runs directly, without a shell, pipes or output filtering. Both stdout and stderr
go to <log-dir>/<label>.log (overwritten when that label is reused). A JSON record
with label, absolute cwd, command, exit and elapsed seconds is printed and appended
to <log-dir>/gates.jsonl. Artifact collection runs only when command[0], or
command[1] immediately after a Python interpreter, has basename mutchecks.py.
Only reported logs=<dir> directories named mutchecks-* directly inside this
repository's .dev/codex/<task>/ are moved to <log-dir>/<label>-<dirname> and
listed in the record's artifacts array. Move failures are recorded in
artifact_errors; the record is still written and the command's exit preserved.
Use a fresh label for each run to keep its output. This script exits with the
command's exit code; a command terminated by a signal exits with 128 + signal.
"""
import argparse
import json
from pathlib import Path
import re
import shutil
import subprocess
import sys
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('log_dir', type=Path)
    parser.add_argument('label')
    parser.add_argument('--cwd', type=Path, default=Path.cwd())
    if '--' not in sys.argv[1:]:
        parser.error('separate the command with --')
    separator = sys.argv.index('--')
    args = parser.parse_args(sys.argv[1:separator])
    command = sys.argv[separator + 1:]
    if not command:
        parser.error('a command is required after --')
    if args.label in ('.', '..') or Path(args.label).name != args.label:
        parser.error('label must be a filename without directories')
    folder = args.log_dir.resolve()
    folder.mkdir(parents=True, exist_ok=True)
    cwd = args.cwd.resolve()
    log = folder / (args.label + '.log')
    start = time.monotonic()
    with log.open('w') as output:
        try:
            code = subprocess.run(command, cwd=cwd, stdout=output,
                                  stderr=subprocess.STDOUT).returncode
        except OSError as exc:
            output.write(str(exc) + '\n')
            code = 127
    if code < 0:
        code = 128 - code
    record = dict(label=args.label, cwd=str(cwd), command=command, exit=code,
                  seconds=round(time.monotonic() - start, 2))
    runs_mutchecks = Path(command[0]).name == 'mutchecks.py' or (
        len(command) > 1 and re.fullmatch(r'python(?:\d+(?:\.\d+)*)?(?:\.exe)?', Path(command[0]).name)
        and Path(command[1]).name == 'mutchecks.py')
    if runs_mutchecks:
        artifacts = []
        artifact_errors = []
        codex = Path(__file__).resolve().parents[1] / '.dev/codex'
        for original in dict.fromkeys(re.findall(r'^logs=(.+)$', log.read_text(errors='replace'), re.M)):
            source = Path(original)
            if not source.is_absolute():
                source = cwd / source
            target = folder / (args.label + '-' + source.name)
            try:
                source = source.resolve()
                if not (source.name.startswith('mutchecks-') and source.parent.parent == codex
                        and source.is_dir()):
                    continue
                shutil.move(str(source), str(target))
                artifacts.append(str(target))
            except (OSError, shutil.Error) as exc:
                artifact_errors.append(dict(source=str(source), target=str(target), error=str(exc)))
        record['artifacts'] = artifacts
        if artifact_errors:
            record['artifact_errors'] = artifact_errors
    with (folder / 'gates.jsonl').open('a') as output:
        output.write(json.dumps(record, ensure_ascii=False) + '\n')
    print(json.dumps(record, ensure_ascii=False), flush=True)
    return code


if __name__ == '__main__':
    sys.exit(main())

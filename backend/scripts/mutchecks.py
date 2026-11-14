#!/usr/bin/env python3
"""按清单验证保护：mutchecks.py [manifest.py] [--since <git-ref>] [--locate] [--jobs N] [names...]。

清单只包含字面量数据，不允许执行代码。
--set <manifest> <name> <field> <value-file> 只替换所选字段的 AST 字面量范围。
guards/packages 的文件为 JSON 列表；其他字段为原文：仅当文件恰以一个换行结尾、
且旧值不以换行结尾时移除这一个换行，其余字符原样保留。
--append <manifest> <entries-json-file> 将 JSON 字典列表作为新的 ENTRIES.extend([...]) 追加。
编辑先重新 load_manifest 并对涉及的条目执行 --locate 检查，失败不改动清单。
只为删掉后行为会变的保护写条目；op 的 Kind、Summary 这类没有运行时效果的声明不写，变异抓不到它们。
--jobs N（默认 1）将所有清单的 go 项按包排序放入同一个队列，空闲副本领取下一项，使用 .dev/mutcheck-worktrees/<n>/ 的
临时文件副本；所有清单合并各包的守护测试基线在主工作树至多 N 个并发运行，通过后才创建副本并运行变异。
--jobs 最多 4（MUTCHECK_MAX_JOBS 可调低），超过时打印提示并按上限运行。
e2e 在所有 go 项之后于主工作树串行运行。副本包含磁盘上的跟踪文件和未忽略的未跟踪文件，
首次变异前逐字节核对所有保护文件；只读 git 命令列出文件，不写 .git。
开始时清理残留副本，结束、失败、Ctrl-C 或 SIGTERM 时删除副本目录。
正式运行（--locate 和内部 --worker-logs 进程除外）持有与 scripts/gate.py 相同的
flock 重锁：路径取 GATE_HEAVY_LOCK，否则 .dev/heavy.lock；等待时打印 waiting for heavy lock。
由 gate.py 启动时它通过 SALON_HEAVY_LOCK_HELD 标记已持有的锁，子进程据此跳过加锁。
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
迁移项只在其自身文件改动、或改动的迁移涉及它保护的表时选中；查询项只在其自身 .sql 文件改动、
或改动的迁移涉及它查询的表时选中。表从迁移中的 CREATE/ALTER/DROP TABLE、CREATE [UNIQUE] INDEX … ON、
REFERENCES、INSERT INTO、UPDATE、DELETE FROM、DELETE … USING、逗号 FROM、UPDATE … FROM、
CREATE/DROP TRIGGER … ON、CREATE POLICY … ON（含 USING/WITH CHECK 里的 FROM/JOIN）、GRANT … ON、
ALTER TABLE … RENAME TO 的前后两个名字读取；带引号的名字按表名解析，解析不了的表位置保守选中。
改动的迁移同时解析该 ref 的版本和工作树版本（重命名按删除加新增），任何一个来源里含
CREATE/REPLACE FUNCTION、DO、DROP FUNCTION、CREATE/ALTER/DROP VIEW、CREATE RULE、
ALTER DEFAULT PRIVILEGES 或无法归属到表的语句时，选中全部迁移和查询项；查询用到迁移里
CREATE VIEW 创建的名字时也保守选中。选中名称逐行输出到 stderr；另输出一行改动表和按表规则
跳过的迁移/查询项数量。
无 --since 的 --locate 始终检查名称选择范围（不指定名称时为整份清单）。
省略清单时按路径排序处理 .dev/codex/*/manifest.py：
mutchecks.py --locate [--since <git-ref>] 只定位；
mutchecks.py --since <git-ref> [--jobs N] 正式运行选中项（必须带 --since，不接受名称）。
逐份输出选中、通过、失败、not run (environment) 数量和最终总计；失败或未运行均非零退出。
PostgreSQL 容器启动签名出现时，基线按包重试一次；变异由 mutcheck.sh 重试一次，
退出码 4 或同一签名只记 not run (environment)，绝不解析为 caught。
mutcheck.sh 对迁移文件的变异发现 migrate up 失败时退出 2（变异让迁移无法执行），
这里按清单错误记 FAIL，不再因为输出里有容器启动签名而改记 environment。
正式运行默认给每个 go worker 启动一个 PostgreSQL 容器（镜像同 dbtest.Image），
把该容器的 SALON_TEST_PG_DSN 传给基线和 worker；结束、失败、Ctrl-C 或 SIGTERM 时只删除本次创建的
salon-mutcheck-pg-<run>-<n> 容器。启动失败时相关项记 not run (environment)。--no-shared-pg
恢复每个 go test 自启容器的旧行为，用于诊断。
正式运行先按包合并选中 go 项的 guards，以 go test -count=1 -run '^(GuardA|GuardB|...)$'
运行这些顶层守护测试（含子测试）；--jobs 1 串行，N > 1 时主工作树至多 N 个包并发，全部通过后
才允许任何变异；基线失败时打印测试输出并退出，避免把已有故障算成保护生效。
每次运行在每份清单旁新建日志目录，保存完整命令、测试输出和结果行 results.log。
跨清单共用的基线日志放在第一份选中清单的目录，baseline-outcomes.json 复制到各目录。
go 项要求 mutcheck.sh 退出 0 且命中所有 guards；e2e 项要求守护断言失败，
还原后同一测试通过。每个副本使用自身的 .dev/mutcheck-backup/，主工作树的 go/e2e
仍共用主槽；没有 original 的残留槽可以安全清理。中断后用 mutcheck.sh --restore 恢复已发布的备份。
每次正式运行（包括 --jobs 1）以及 --since 的 go list 都使用仓库 .dev/mutcheck-gocache/<run>/
下独立的 GOCACHE，绝不写入用户的默认缓存；只有正式运行在开始时清理残留运行目录，结束、失败、
异常、Ctrl-C 或 SIGTERM 时删除本次目录。--locate 不运行 go 命令时不创建缓存，也不清理残留目录。
基线在主工作树共享这个缓存；每个变异副本另建缓存并硬链接基线条目（含 xx/<hash>-d/ 可执行目录），
跑完一项后只删除该变异新增的条目，峰值只与并发副本数和一个变异的编译量有关。
--jobs N > 1 时每个 go worker 用 -p max(1, CPU/N) 和同样的 GOMAXPROCS 限制编译并行度，
使所有 worker 合计接近 CPU 数；--jobs 1 保持原行为。
所有 go test 都带 -trimpath，让不同路径的副本共享编译结果。基线前和每项变异前检查缓存所在
磁盘的剩余空间，低于 20 GB 时用中文消息停止，未运行项计为 environment（非零退出），绝不计为 caught。
测试可用 MUTCHECK_DISK_HEADROOM_BYTES 调低该下限。单独运行 mutcheck.sh（含 --restore）且未设置
GOCACHE 时，使用 .dev/ 下的临时缓存并在退出时删除。
"""
import argparse
import ast
from collections import deque
import fcntl
import io
import json
import os
from pathlib import Path
import re
import secrets
import shutil
import signal
import shlex
import socket
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parents[2]
GENERATED = ROOT / 'backend/internal/db/dbgen'
SLOT = ROOT / '.dev/mutcheck-backup'
ENVIRONMENT_SIGNATURE = '启动 PostgreSQL 测试容器失败'
ENVIRONMENT_EXIT = 4
DISK_EXIT = 5
DISK_HEADROOM = 20 * 1000 ** 3
DISK_HEADROOM_ENV = 'MUTCHECK_DISK_HEADROOM_BYTES'
CACHE_DIRNAME = 'mutcheck-gocache'
POSTGRES_IMAGE = 'postgres:18-alpine'
SHARED_PG_PREFIX = 'salon-mutcheck-pg'
PG_DSN_ENV = 'SALON_TEST_PG_DSN'
HEAVY_LOCK_ENV = 'GATE_HEAVY_LOCK'
HEAVY_LOCK_MARKER = 'SALON_HEAVY_LOCK_HELD'
MAX_JOBS_ENV = 'MUTCHECK_MAX_JOBS'
DEFAULT_MAX_JOBS = 4
GO_PARALLEL_ENV = 'MUTCHECK_GO_PARALLEL'

IDENTIFIER = r'(?:"[^"]+"|[A-Za-z_][A-Za-z0-9_$]*)'
QUALIFIED = rf'(?:{IDENTIFIER}\s*\.\s*)?{IDENTIFIER}'
# 这些词出现在 UPDATE 之后时不是表名（ON CONFLICT DO UPDATE SET、ON UPDATE CASCADE 等）。
NOT_TABLES = {'set', 'of', 'nowait', 'skip', 'cascade', 'restrict', 'no', 'action', 'null',
              'default', 'where', 'returning', 'only', 'key', 'share'}
CONTEXT_STATEMENTS = {'create_table', 'alter_table', 'create_index', 'create_policy'}


def mask_sql(source):
    """Blank comments, string literals and dollar-quoted bodies, keeping offsets."""
    chars = list(source)
    size = len(source)
    index = 0
    while index < size:
        if source.startswith('--', index):
            end = source.find('\n', index + 2)
            end = size if end < 0 else end
        elif source.startswith('/*', index):
            end = source.find('*/', index + 2)
            end = size if end < 0 else end + 2
        elif source[index] == "'":
            end = index + 1
            while end < size:
                if source[end] == "'":
                    if end + 1 < size and source[end + 1] == "'":
                        end += 2
                        continue
                    end += 1
                    break
                end += 1
        elif source[index] == '"':
            end = index + 1
            while end < size:
                if source[end] == '"':
                    if end + 1 < size and source[end + 1] == '"':
                        end += 2
                        continue
                    end += 1
                    break
                end += 1
            index = end
            continue
        elif source[index] == '$':
            match = re.match(r'\$[A-Za-z_][A-Za-z0-9_]*\$|\$\$', source[index:])
            if match is None:
                index += 1
                continue
            tag = match.group(0)
            end = source.find(tag, index + len(tag))
            end = size if end < 0 else end + len(tag)
        else:
            index += 1
            continue
        chars[index:end] = ' ' * max(0, end - index)
        index = end
    return ''.join(chars)


def split_statements(source):
    """Split SQL on semicolons outside strings, comments and dollar quotes."""
    statements = []
    start = 0
    size = len(source)
    index = 0
    while index < size:
        if source.startswith('--', index):
            end = source.find('\n', index + 2)
            index = size if end < 0 else end + 1
        elif source.startswith('/*', index):
            end = source.find('*/', index + 2)
            index = size if end < 0 else end + 2
        elif source[index] in ("'", '"'):
            quote = source[index]
            index += 1
            while index < size:
                if source[index] == quote:
                    if index + 1 < size and source[index + 1] == quote:
                        index += 2
                        continue
                    index += 1
                    break
                index += 1
        elif source[index] == '$':
            match = re.match(r'\$[A-Za-z_][A-Za-z0-9_]*\$|\$\$', source[index:])
            if match is None:
                index += 1
                continue
            tag = match.group(0)
            end = source.find(tag, index + len(tag))
            index = size if end < 0 else end + len(tag)
        elif source[index] == ';':
            statements.append((start, index))
            start = index + 1
            index += 1
        else:
            index += 1
    if start < size:
        statements.append((start, size))
    return statements


def strip_leading_space(source):
    """Drop leading whitespace and comments, returning the remaining text."""
    index = 0
    size = len(source)
    while index < size:
        if source[index].isspace():
            index += 1
        elif source.startswith('--', index):
            end = source.find('\n', index + 2)
            index = size if end < 0 else end + 1
        elif source.startswith('/*', index):
            end = source.find('*/', index + 2)
            index = size if end < 0 else end + 2
        else:
            break
    return source[index:]


def normalize_table(token):
    """Strip quotes and a schema prefix from a table reference; None if invalid."""
    parts = []
    index = 0
    size = len(token)
    while True:
        while index < size and token[index].isspace():
            index += 1
        if index >= size:
            return None
        if token[index] == '"':
            index += 1
            value = []
            while index < size:
                if token[index] == '"':
                    if index + 1 < size and token[index + 1] == '"':
                        value.append('"')
                        index += 2
                        continue
                    index += 1
                    break
                value.append(token[index])
                index += 1
            else:
                return None
            parts.append(''.join(value))
        else:
            match = re.match(r'[A-Za-z_][A-Za-z0-9_$]*', token[index:])
            if match is None:
                return None
            parts.append(match.group(0))
            index += match.end()
        while index < size and token[index].isspace():
            index += 1
        if index < size and token[index] == '.':
            index += 1
            continue
        break
    if index < size or not parts:
        return None
    return parts[-1].lower()


def table_token(text):
    """Return the table name at the start of text, or None."""
    match = re.match(rf'\s*(?:ONLY\s+|LATERAL\s+)?({QUALIFIED})', text, re.I)
    if match is None:
        return None
    name = normalize_table(match.group(1))
    return None if name is None or name in NOT_TABLES else name


def table_list(text):
    """Parse a comma-separated table list, stopping at the next SQL clause."""
    cut = re.split(r'(?i)\b(?:TO|FROM|CASCADE|RESTRICT|WHERE|SET|USING|WITH)\b', text, maxsplit=1)[0]
    names = set()
    for item in cut.split(','):
        name = table_token(item)
        if name is None:
            if item.strip():
                return None
        else:
            names.add(name)
    return names


def top_level_keyword(masked, keyword):
    """Find a keyword outside parentheses, strings and quoted identifiers."""
    depth = 0
    size = len(masked)
    index = 0
    wanted = keyword.lower()
    while index < size:
        char = masked[index]
        if char == '"':
            index += 1
            while index < size:
                if masked[index] == '"':
                    if index + 1 < size and masked[index + 1] == '"':
                        index += 2
                        continue
                    index += 1
                    break
                index += 1
        elif char == '(':
            depth += 1
            index += 1
        elif char == ')':
            depth = max(0, depth - 1)
            index += 1
        elif depth == 0 and char.isalpha() and (index == 0 or not (masked[index - 1].isalnum() or masked[index - 1] in '_$')):
            end = index + 1
            while end < size and (masked[end].isalnum() or masked[end] in '_$'):
                end += 1
            if masked[index:end].lower() == wanted:
                return index
            index = end
        else:
            index += 1
    return None


def referenced_tables(masked):
    """Collect tables from REFERENCES clauses anywhere in a statement."""
    names = set()
    for match in re.finditer(rf'(?i)\bREFERENCES\s+({QUALIFIED})', masked):
        name = normalize_table(match.group(1))
        if name is None:
            return None
        names.add(name)
    return names


def write_tables(masked):
    """Collect tables that a statement writes or defines."""
    names = referenced_tables(masked)
    if names is None:
        return None
    for pattern in (rf'(?i)\bINSERT\s+INTO\s+(?:ONLY\s+)?({QUALIFIED})',
                    rf'(?i)\bDELETE\s+FROM\s+(?:ONLY\s+)?({QUALIFIED})'):
        for match in re.finditer(pattern, masked):
            name = normalize_table(match.group(1))
            if name is None:
                return None
            names.add(name)
    for match in re.finditer(rf'(?i)\bUPDATE\s+({QUALIFIED})', masked):
        name = normalize_table(match.group(1))
        if name is None:
            return None
        if name not in NOT_TABLES:
            names.add(name)
    return names


def fragment_tables(source):
    """Return tables named by a SQL fragment, or None when it is unattributable."""
    category, tables = statement_tables(source)
    if category == 'unknown':
        written = write_tables(mask_sql(source))
        queried = query_tables(source)
        if written is None or queried is None:
            return None
        return written | queried
    return tables


def statement_tables(source):
    """Classify one statement and return (category, tables); tables None means all."""
    masked = mask_sql(source)
    code = strip_leading_space(masked)
    if not code.strip():
        return 'empty', set()
    if re.match(r'(?is)CREATE\s+(?:OR\s+REPLACE\s+)?FUNCTION\b', code):
        return 'all', None
    if re.match(r'(?is)DO\b', code):
        return 'all', None
    if re.match(r'(?is)ALTER\s+DEFAULT\s+PRIVILEGES\b', code):
        return 'all', None
    if re.match(r'(?is)(?:DROP\s+FUNCTION|(?:CREATE|ALTER|DROP)\s+VIEW|CREATE\s+(?:OR\s+REPLACE\s+)?RULE)\b', code):
        return 'all', None
    if re.match(r'(?is)(?:GRANT|REVOKE)\b', code):
        position = top_level_keyword(code, 'ON')
        if position is None:
            return 'grant', None
        target = strip_leading_space(code[position + 2:])
        target = re.sub(r'(?is)^TABLE\s+', '', target)
        if re.match(r'(?is)ALL\s+TABLES\s+IN\s+SCHEMA\b', target):
            return 'grant', None
        if re.match(r'(?is)ALL\s+(?:SEQUENCES|FUNCTIONS|PROCEDURES|ROUTINES)\s+IN\s+SCHEMA\b', target):
            return 'grant', set()
        if re.match(r'(?is)(?:SCHEMA|FUNCTION|SEQUENCE|DATABASE|TYPE|LANGUAGE|ROLE|TABLESPACE|DOMAIN)\b', target):
            return 'grant', set()
        return 'grant', table_list(target)
    match = re.match(rf'(?is)CREATE\s+(?:UNLOGGED\s+|TEMP(?:ORARY)?\s+)?TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?({QUALIFIED})', code)
    if match is not None:
        names = write_tables(code)
        if names is None:
            return 'create_table', None
        name = normalize_table(match.group(1))
        if name is None:
            return 'create_table', None
        names.add(name)
        return 'create_table', names
    match = re.match(rf'(?is)ALTER\s+TABLE\s+(?:IF\s+EXISTS\s+)?(?:ONLY\s+)?({QUALIFIED})', code)
    if match is not None:
        names = write_tables(code)
        if names is None:
            return 'alter_table', None
        name = normalize_table(match.group(1))
        if name is None:
            return 'alter_table', None
        names.add(name)
        rename = re.search(rf'(?i)\bRENAME\s+TO\s+({QUALIFIED})', code)
        if rename is not None:
            target = normalize_table(rename.group(1))
            if target is None:
                return 'alter_table', None
            names.add(target)
        elif re.search(r'(?i)\bRENAME\s+TO\b', code):
            return 'alter_table', None
        return 'alter_table', names
    match = re.match(rf'(?is)CREATE\s+(?:UNIQUE\s+)?INDEX\s+(?:CONCURRENTLY\s+)?(?:IF\s+NOT\s+EXISTS\s+)?{QUALIFIED}\s+ON\s+(?:ONLY\s+)?({QUALIFIED})', code)
    if match is not None:
        names = write_tables(code)
        if names is None:
            return 'create_index', None
        name = normalize_table(match.group(1))
        if name is None:
            return 'create_index', None
        names.add(name)
        return 'create_index', names
    match = re.match(rf'(?is)(?:CREATE|ALTER|DROP)\s+POLICY\s+{IDENTIFIER}\s+ON\s+({QUALIFIED})', code)
    if match is not None:
        names = write_tables(code)
        if names is None:
            return 'create_policy', None
        name = normalize_table(match.group(1))
        if name is None:
            return 'create_policy', None
        names.add(name)
        referenced = query_tables(code)
        if referenced is None:
            return 'create_policy', None
        names |= referenced
        return 'create_policy', names
    match = re.match(rf'(?is)CREATE\s+(?:OR\s+REPLACE\s+)?(?:CONSTRAINT\s+)?TRIGGER\s+{IDENTIFIER}\b', code)
    if match is not None:
        position = top_level_keyword(code, 'ON')
        if position is None:
            return 'create_trigger', None
        name = table_token(code[position + 2:])
        return 'create_trigger', None if name is None else {name}
    match = re.match(rf'(?is)DROP\s+TRIGGER\s+(?:IF\s+EXISTS\s+)?{IDENTIFIER}\s+ON\s+({QUALIFIED})', code)
    if match is not None:
        name = normalize_table(match.group(1))
        return 'drop_trigger', None if name is None else {name}
    if re.match(r'(?is)DROP\s+TRIGGER\b', code):
        return 'drop_trigger', None
    match = re.match(rf'(?is)DROP\s+TABLE\s+(?:IF\s+EXISTS\s+)?(.*)', code)
    if match is not None:
        return 'drop_table', table_list(match.group(1))
    if re.match(r'(?is)(?:SET|RESET|BEGIN|COMMIT|ROLLBACK|SAVEPOINT|RELEASE|SELECT|WITH|ANALYZE|VACUUM)\b', code):
        return 'neutral', write_tables(code)
    if re.match(r'(?is)(?:DROP\s+(?:INDEX|SEQUENCE|TYPE|ROLE|SCHEMA)|CREATE\s+(?:EXTENSION|SCHEMA|ROLE|SEQUENCE|TYPE|DATABASE)|ALTER\s+(?:SEQUENCE|TYPE|FUNCTION|ROLE|SCHEMA|DATABASE)|COMMENT\s+ON)\b', code):
        return 'neutral', set()
    names = write_tables(code)
    if names is None:
        return 'unknown', None
    if names:
        return 'write', names
    # 认不出的语句可能间接改表，按影响全部表处理。
    return 'unknown', None


def expanded_statements(source):
    """Return (start, end, category, tables) for each statement in a SQL file."""
    result = []
    for start, end in split_statements(source):
        text = source[start:end]
        code = strip_leading_space(text)
        if not code.strip():
            continue
        category, tables = statement_tables(text)
        result.append((start + len(text) - len(code), end, category, tables))
    return result


def name_positions(source):
    """Return the offsets of sqlc `-- name:` lines in a query file."""
    return [match.start() for match in re.finditer(r'(?m)^\s*--\s*name:\s*\S+', source)]


def previous_word(masked, position):
    """Return the word immediately before position."""
    index = position - 1
    while index >= 0 and masked[index].isspace():
        index -= 1
    end = index + 1
    while index >= 0 and (masked[index].isalnum() or masked[index] in '_$'):
        index -= 1
    return masked[index + 1:end].lower()


def enclosing_functions(masked, position):
    """Return the names of function calls whose parentheses enclose position."""
    stack = []
    index = 0
    while index < position:
        char = masked[index]
        if char == '"':
            index += 1
            while index < position:
                if masked[index] == '"':
                    if index + 1 < position and masked[index + 1] == '"':
                        index += 2
                        continue
                    index += 1
                    break
                index += 1
        elif char == '(':
            stack.append(previous_word(masked, index))
            index += 1
        elif char == ')':
            if stack:
                stack.pop()
            index += 1
        else:
            index += 1
    return stack


def top_level_words(source):
    """Return (word, start, end) outside parentheses and quoted identifiers."""
    words = []
    depth = 0
    index = 0
    size = len(source)
    while index < size:
        char = source[index]
        if char == '"':
            index += 1
            while index < size:
                if source[index] == '"':
                    if index + 1 < size and source[index + 1] == '"':
                        index += 2
                        continue
                    index += 1
                    break
                index += 1
        elif char == '(':
            depth += 1
            index += 1
        elif char == ')':
            depth = max(0, depth - 1)
            index += 1
        elif depth == 0 and char.isalpha():
            end = index + 1
            while end < size and (source[end].isalnum() or source[end] in '_$'):
                end += 1
            words.append((source[index:end].upper(), index, end))
            index = end
        else:
            index += 1
    return words


def split_top_level(source, separator=','):
    """Split on separator outside parentheses and quoted identifiers."""
    parts = []
    depth = 0
    start = 0
    index = 0
    size = len(source)
    while index < size:
        char = source[index]
        if char == '"':
            index += 1
            while index < size:
                if source[index] == '"':
                    if index + 1 < size and source[index + 1] == '"':
                        index += 2
                        continue
                    index += 1
                    break
                index += 1
        elif char == '(':
            depth += 1
            index += 1
        elif char == ')':
            depth = max(0, depth - 1)
            index += 1
        elif depth == 0 and char == separator:
            parts.append(source[start:index])
            start = index + 1
            index += 1
        else:
            index += 1
    parts.append(source[start:])
    return parts


FROM_END = {'WHERE', 'GROUP', 'ORDER', 'LIMIT', 'OFFSET', 'UNION', 'INTERSECT',
            'EXCEPT', 'WINDOW', 'RETURNING', 'FOR', 'FETCH'}


def from_clause(source):
    """Cut a FROM clause before its next top-level clause."""
    for word, start, _ in top_level_words(source):
        if word in FROM_END:
            return source[:start]
    return source


def resolve_source_item(item, function_parens=True):
    """Resolve the first table of one FROM/JOIN/USING item."""
    item = item.strip()
    item = re.sub(r'(?is)^ONLY\s+', '', item)
    if item.startswith('(') or re.match(r'(?is)^LATERAL\s*\(', item):
        return 'subquery'
    item = re.sub(r'(?is)^LATERAL\s+', '', item)
    match = re.match(rf'({QUALIFIED})', item)
    if match is None:
        return 'unresolved'
    if function_parens and item[match.end():].lstrip().startswith('('):
        return 'function'
    name = normalize_table(match.group(1))
    if name is None:
        return 'unresolved'
    return None if name in NOT_TABLES else name


def add_source_list(text, tables):
    """Add comma-separated FROM/USING items; return False when unresolved."""
    for item in split_top_level(from_clause(text)):
        name = resolve_source_item(item)
        if name == 'subquery':
            continue
        if name in {'function', 'unresolved'}:
            return False
        if name is not None:
            tables.add(name)
    return True


def query_tables(source):
    """Return tables read or written by a query; None when a FROM item is unknown."""
    masked = mask_sql(source)
    tables = set()
    pattern = rf'(?i)\b(FROM|JOIN|INTO|UPDATE|DELETE\s+FROM)\b\s*'
    for match in re.finditer(pattern, masked):
        keyword = ' '.join(match.group(1).upper().split())
        if keyword == 'FROM' and any(
                name in {'extract', 'substring', 'trim', 'overlay', 'position'}
                for name in enclosing_functions(masked, match.start())):
            continue
        if keyword == 'FROM' and previous_word(masked, match.start()) == 'distinct':
            continue
        if keyword == 'UPDATE' and previous_word(masked, match.start()) in {'do', 'for', 'no', 'key'}:
            continue
        rest = masked[match.end():]
        if keyword == 'FROM':
            if not add_source_list(rest, tables):
                return None
        elif keyword in {'JOIN', 'INTO', 'UPDATE', 'DELETE FROM'}:
            name = resolve_source_item(rest, function_parens=keyword != 'INTO')
            if name == 'subquery':
                continue
            if name in {'function', 'unresolved'}:
                return None
            if name is not None:
                tables.add(name)
        if keyword == 'DELETE FROM':
            words = top_level_words(rest)
            for word, start, _ in words:
                if word == 'USING':
                    if not add_source_list(rest[start + len('USING'):], tables):
                        return None
                    break
                if word in FROM_END:
                    break
    return tables


def migration_entry_tables(entry, source, statements):
    """Return a migration entry's tables, or None when they cannot be determined."""
    position = source.find(entry['original'])
    if position < 0:
        return None
    tables = fragment_tables(entry['original'])
    if tables is None:
        return None
    enclosing = [names for start, end, _, names in statements if start <= position < end]
    if enclosing:
        if enclosing[0] is None:
            return tables or None
        tables |= enclosing[0]
    else:
        contexts = [(start, names) for start, _, category, names in statements
                    if category in CONTEXT_STATEMENTS and start <= position]
        if contexts:
            _, context = max(contexts, key=lambda item: item[0])
            if context is None:
                return None
            tables |= context
    return tables or None


def query_entry_tables(entry, source, positions, views=frozenset()):
    """Return a query entry's tables, or None when they cannot be determined."""
    position = source.find(entry['original'])
    if position < 0:
        return None
    starts = [start for start in positions if start <= position]
    if not starts:
        return None
    start = max(starts)
    ends = [end for end in positions if end > position]
    tables = query_tables(source[start:min(ends) if ends else len(source)])
    if tables is None or tables & views:
        return None
    return tables or None


def migration_view_names():
    """Return every view name created anywhere in the migrations."""
    names = set()
    for path in sorted((ROOT / 'backend/internal/db/migrations').glob('*.sql')):
        masked = mask_sql(path.read_text())
        for match in re.finditer(
                rf'(?i)\bCREATE\s+(?:OR\s+REPLACE\s+)?(?:MATERIALIZED\s+)?VIEW\s+(?:IF\s+NOT\s+EXISTS\s+)?({QUALIFIED})',
                masked):
            name = normalize_table(match.group(1))
            if name is not None:
                names.add(name)
    return names


def changed_paths(ref):
    """Return changed paths, including both sides of renames and copies."""
    result = subprocess.run(['git', 'diff', '--name-status', '-z', ref, '--'],
                            cwd=ROOT, capture_output=True, check=True)
    tokens = [os.fsdecode(token) for token in result.stdout.split(b'\0') if token]
    paths = set()
    index = 0
    while index < len(tokens):
        status = tokens[index]
        index += 1
        if status.startswith(('R', 'C')):
            paths.update(tokens[index:index + 2])
            index += 2
        else:
            paths.add(tokens[index])
            index += 1
    untracked = subprocess.run(['git', 'ls-files', '--others', '--exclude-standard', '-z'],
                               cwd=ROOT, capture_output=True, check=True)
    paths.update(os.fsdecode(path) for path in untracked.stdout.split(b'\0') if path)
    return paths


def ref_file(ref, path):
    """Return ('present'|'absent'|'error', source) for a path at ref."""
    tree = subprocess.run(['git', 'ls-tree', '-z', ref, '--', path],
                          cwd=ROOT, capture_output=True)
    if tree.returncode:
        return 'error', None
    if not tree.stdout:
        return 'absent', None
    shown = subprocess.run(['git', 'show', f'{ref}:{path}'],
                           cwd=ROOT, capture_output=True)
    if shown.returncode:
        return 'error', None
    return 'present', shown.stdout.decode('utf-8')


def changed_migration_tables(changed, ref):
    """Return (all_tables, tables, reason) for changed migration files."""
    tables = set()
    reason = None
    for path in sorted(changed):
        if not path.startswith('backend/internal/db/migrations/') or not path.endswith('.sql'):
            continue
        file = ROOT / path
        ref_state, ref_source = ref_file(ref, path)
        if ref_state == 'error':
            return True, set(), f'{path} (cannot read {ref})'
        sources = []
        if file.is_file():
            try:
                sources.append(file.read_text())
            except OSError:
                return True, set(), f'{path} (cannot read working tree)'
        if ref_state == 'present':
            sources.append(ref_source)
        if not sources:
            return True, set(), f'{path} (missing in {ref} and working tree)'
        for source in sources:
            for start, _, _, names in expanded_statements(source):
                if names is None:
                    line = source.count('\n', 0, start) + 1
                    return True, set(), f'{path}:{line}'
                tables |= names
    return False, tables, reason


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


def entry_nodes(tree):
    """Track the effective entry literals through the supported data statements."""
    entries = []
    for node in tree.body:
        if isinstance(node, ast.Assign) and node.targets[0].id == 'ENTRIES':
            entries = list(node.value.elts)
        elif isinstance(node, ast.AugAssign) and node.target.id == 'ENTRIES':
            entries.extend(node.value.elts)
        elif isinstance(node, ast.Expr) and isinstance(node.value, ast.Call):
            call = node.value
            if call.func.value.id == 'ENTRIES':
                entries.extend([call.args[0]] if call.func.attr == 'append' else call.args[0].elts)
    return entries


def edit_manifest(path, selector=None, field=None, value_file=None, append_file=None):
    """Validate a candidate before publishing any changed manifest bytes."""
    values = load_manifest(path)
    entries = values['ENTRIES']
    source = path.read_bytes()
    if append_file is not None:
        added = json.loads(append_file.read_text())
        if not isinstance(added, list) or not all(isinstance(e, dict) for e in added):
            raise ValueError('append requires a JSON list of dicts')
        candidate = source + (b'' if source.endswith(b'\n') else b'\n')
        candidate += ('ENTRIES.extend(' + repr(added) + ')\n').encode('utf-8')
        indices = range(len(entries), len(entries) + len(added))
    else:
        indices = [i for i, entry in enumerate(entries)
                   if selector in (entry['name'], entry['step'] + '/' + entry['name'])]
        if len(indices) != 1:
            raise ValueError(f'{selector}: expected exactly one entry, found {len(indices)}')
        index = indices[0]
        node = entry_nodes(ast.parse(source))[index]
        fields = [value for key, value in zip(node.keys, node.values)
                  if ast.literal_eval(key) == field]
        if len(fields) != 1:
            raise ValueError(f'{field}: expected exactly one field literal')
        literal = fields[0]
        value = value_file.read_bytes().decode('utf-8')
        if field in ('guards', 'packages'):
            value = json.loads(value)
            if not isinstance(value, list):
                raise ValueError(f'{field} requires a JSON list')
        elif value.endswith('\n') and not value.endswith('\n\n') and not entries[index][field].endswith('\n'):
            value = value[:-1]
        lines = source.splitlines(keepends=True)
        start = sum(map(len, lines[:literal.lineno - 1])) + literal.col_offset
        end = sum(map(len, lines[:literal.end_lineno - 1])) + literal.end_col_offset
        candidate = source[:start] + repr(value).encode('utf-8') + source[end:]
    # Loading the candidate from disk exercises the same parser as normal runs.
    with tempfile.TemporaryDirectory() as folder:
        temporary = Path(folder) / path.name
        temporary.write_bytes(candidate)
        checked = load_manifest(temporary)['ENTRIES']
        for index in indices:
            locate(checked[index])
    path.write_bytes(candidate)


def partition_entries(entries):
    """Return one package-ordered Go queue and serial e2e entries."""
    go = [entry for entry in entries if entry['kind'] == 'go']
    return sorted(go, key=lambda entry: tuple(entry['packages'])), [
        entry for entry in entries if entry['kind'] == 'e2e']


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


def remove_tree(folder):
    if folder.exists():
        shutil.rmtree(folder)


def cleanup_workers(folder):
    remove_tree(folder)


def cpu_count():
    """Return the CPUs available for sizing Go's build parallelism."""
    return os.cpu_count() or 1


def heavy_lock_path():
    """Return the heavy-command lock shared with scripts/gate.py."""
    return Path(os.environ.get(HEAVY_LOCK_ENV) or ROOT / '.dev/heavy.lock')


def acquire_heavy_lock():
    """Hold the heavy lock for this run unless a parent already holds it."""
    path = heavy_lock_path().resolve()
    marker = os.environ.get(HEAVY_LOCK_MARKER)
    if marker and Path(marker).resolve() == path:
        return None
    path.parent.mkdir(parents=True, exist_ok=True)
    lock = path.open('w')
    try:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except BlockingIOError:
        print(f'waiting for heavy lock {path}', file=sys.stderr, flush=True)
        fcntl.flock(lock, fcntl.LOCK_EX)
    os.environ[HEAVY_LOCK_MARKER] = str(path)
    return lock


class SharedPGError(Exception):
    """A PostgreSQL container for the worker pool could not be started."""


class SharedServers:
    """Own one PostgreSQL container per worker and remove only those containers."""

    def __init__(self, count):
        self.run_id = secrets.token_hex(6)
        self.names = [f'{SHARED_PG_PREFIX}-{self.run_id}-{index}' for index in range(count)]
        self.dsns = []
        self.created = []

    def _docker(self, *args, check=False):
        return subprocess.run(['docker', *args], capture_output=True, text=True, check=check)

    @staticmethod
    def _host_accepts_tcp(port, timeout=1.0):
        """Return whether the published host port accepts a TCP connection."""
        try:
            with socket.create_connection(('127.0.0.1', int(port)), timeout=timeout):
                return True
        except OSError:
            return False

    def start(self, timeout=60):
        try:
            for name in self.names:
                result = self._docker(
                    'run', '-d', '--rm', '--name', name,
                    '--tmpfs', '/var/lib/postgresql/data',
                    '-e', 'PGDATA=/var/lib/postgresql/data',
                    '-e', 'POSTGRES_DB=salon', '-e', 'POSTGRES_USER=salon',
                    '-e', 'POSTGRES_PASSWORD=salon',
                    '-p', '127.0.0.1::5432', POSTGRES_IMAGE, '-c', 'max_connections=300')
                if result.returncode:
                    raise SharedPGError(result.stderr.strip() or f'docker run {name} failed')
                self.created.append(name)
                port = self._docker('port', name, '5432/tcp', check=True).stdout.strip()
                port = port.splitlines()[0].rsplit(':', 1)[-1]
                deadline = time.monotonic() + timeout
                stable = 0
                while True:
                    # The image entrypoint first runs a temporary init server
                    # with listen_addresses='' that answers on the Unix socket
                    # only. A socket probe would therefore succeed too early,
                    # so require TCP inside the container and through the
                    # published host port before declaring the server ready.
                    probe = self._docker(
                        'exec', name, 'pg_isready', '-h', '127.0.0.1', '-p', '5432',
                        '-U', 'salon', '-d', 'salon')
                    if probe.returncode == 0 and self._host_accepts_tcp(port):
                        stable += 1
                        if stable >= 2:
                            break
                    else:
                        stable = 0
                    if time.monotonic() >= deadline:
                        raise SharedPGError(f'{name} did not accept TCP connections within {timeout}s')
                    time.sleep(0.2)
                self.dsns.append(f'postgres://salon:salon@127.0.0.1:{port}/salon?sslmode=disable')
        except SharedPGError:
            self.close()
            raise
        except (OSError, subprocess.CalledProcessError) as exc:
            self.close()
            raise SharedPGError(str(exc)) from exc
        return self

    def dsn(self, index):
        return self.dsns[index % len(self.dsns)]

    def close(self):
        for name in self.created:
            try:
                self._docker('rm', '-f', name)
            except OSError:
                pass
        self.created.clear()
        self.dsns.clear()


def test_env(pg_dsn=None, keep_pg=False):
    """Build the environment for a go test run, overriding the shared server DSN."""
    env = dict(os.environ)
    # 清除继承的 GOFLAGS，避免它悄悄过滤守护测试。
    env.pop('GOFLAGS', None)
    if not keep_pg:
        env.pop(PG_DSN_ENV, None)
    if pg_dsn:
        env[PG_DSN_ENV] = pg_dsn
    return env


def cache_root():
    """Return the directory holding this repository's mutcheck Go caches."""
    return ROOT / '.dev' / CACHE_DIRNAME


def current_cache():
    """Return the GOCACHE inherited from the run that started this process."""
    path = os.environ.get('GOCACHE')
    return Path(path) if path else None


def create_run_cache(clean_stale=True):
    """Create a fresh run cache and export it to every child process."""
    previous = os.environ.get('GOCACHE')
    root = cache_root()
    if clean_stale:
        remove_tree(root)
        root.mkdir(parents=True)
    else:
        root.mkdir(parents=True, exist_ok=True)
    cache = Path(tempfile.mkdtemp(prefix='run-', dir=root))
    os.environ['GOCACHE'] = str(cache)
    return cache, previous


def cleanup_run_cache(cache, previous):
    """Delete the run cache and restore the caller's GOCACHE."""
    remove_tree(cache)
    try:
        cache_root().rmdir()
    except OSError:
        pass
    if previous is None:
        os.environ.pop('GOCACHE', None)
    else:
        os.environ['GOCACHE'] = previous


def free_space(cache=None):
    """Return free bytes on the filesystem holding the build cache, if any."""
    cache = cache or current_cache()
    return shutil.disk_usage(cache).free if cache is not None else None


def disk_message(free):
    return (f'保护验证停止：Go 构建缓存所在磁盘剩余 {free / 1000 ** 3:.1f} GB，'
            f'低于 {disk_headroom() / 1000 ** 3:.0f} GB 下限；未运行的项计为 environment。')


def disk_headroom():
    """Return the configured free-space floor; tests may lower it by env var."""
    value = os.environ.get(DISK_HEADROOM_ENV)
    return int(value) if value is not None else DISK_HEADROOM


def seed_cache(source, target):
    """Hard-link a finished cache, including cached executable directories."""
    remove_tree(target)
    target.mkdir(parents=True)
    seeded_files, seeded_dirs = set(), set()
    if not source.exists():
        return seeded_files, seeded_dirs

    def visit(src, dst, prefix):
        for item in src.iterdir():
            relative = prefix + item.name
            destination = dst / item.name
            if item.is_dir() and not item.is_symlink():
                destination.mkdir()
                seeded_dirs.add(relative)
                visit(item, destination, relative + '/')
                continue
            try:
                os.link(item, destination)
            except OSError:
                shutil.copy2(item, destination)
            seeded_files.add(relative)

    for shard in source.iterdir():
        if not shard.is_dir() or len(shard.name) != 2:
            continue
        destination = target / shard.name
        destination.mkdir()
        seeded_dirs.add(shard.name)
        visit(shard, destination, shard.name + '/')
    return seeded_files, seeded_dirs


def prune_cache(cache, seeded):
    """Delete entries a mutation produced, keeping the seeded baseline entries."""
    if not cache.exists():
        return
    seeded_files, seeded_dirs = seeded

    def visit(directory, prefix):
        for item in list(directory.iterdir()):
            relative = prefix + item.name
            if item.is_dir() and not item.is_symlink():
                visit(item, relative + '/')
                if relative not in seeded_dirs:
                    try:
                        item.rmdir()
                    except OSError:
                        pass
            elif relative not in seeded_files:
                item.unlink()

    for shard in list(cache.iterdir()):
        if not shard.is_dir() or len(shard.name) != 2:
            continue
        visit(shard, shard.name + '/')
        if shard.name not in seeded_dirs:
            try:
                shard.rmdir()
            except OSError:
                pass


def environment_line(entry):
    return f"{entry['step']} {entry['name']} exit=not-run caught=- restored=True not run (environment)"


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


def run_parallel(selected, manifest, jobs, logs, results, shared_pg=False):
    return run_queue([(selected, manifest, logs, results)], jobs, shared_pg=shared_pg)


def run_queue(groups, jobs, shared_pg=False):
    """Baseline the union, then reuse each copy for the next queued entry."""
    folder = ROOT / '.dev/mutcheck-worktrees'
    processes, previous = [], {}
    previous_env = {}
    selected = [entry for entries, _, _, _ in groups for entry in entries]
    logs, results = groups[0][2:]
    lines = [[] for _ in groups]
    owners = {id(entry): index for index, group in enumerate(groups) for entry in group[0]}
    go, serial = partition_entries(selected)
    cache = current_cache()
    worker_caches = {}
    reported = set()
    ready = False
    servers = None
    if jobs > 1:
        parallel = max(1, cpu_count() // jobs)
        for key, value in ((GO_PARALLEL_ENV, str(parallel)), ('GOMAXPROCS', str(parallel))):
            previous_env[key] = os.environ.get(key)
            os.environ[key] = value

    def interrupted(signum, frame):
        raise KeyboardInterrupt

    def stop_for_disk(free):
        print(disk_message(free), file=sys.stderr)
        for entry in go + serial:
            if id(entry) not in reported:
                lines[owners[id(entry)]].append(environment_line(entry))
        return DISK_EXIT

    try:
        for sig in (signal.SIGINT, signal.SIGTERM):
            previous[sig] = signal.signal(sig, interrupted)
        cleanup_workers(folder)
        free = free_space(cache)
        if free is not None and free < disk_headroom():
            return stop_for_disk(free)
        if shared_pg and go:
            servers = SharedServers(min(jobs, len(go)))
            try:
                servers.start()
            except SharedPGError as exc:
                print(f'启动 PostgreSQL 测试服务器失败：{exc}', file=sys.stderr)
                servers = None
                for entry in go + serial:
                    lines[owners[id(entry)]].append(environment_line(entry))
                return 1
        # Preserve the serial baseline calling convention as well.
        clean = (baseline(selected, logs, results, jobs=jobs, servers=servers) if jobs > 1
                 else baseline(selected, logs, results, servers=servers))
        outcomes = logs / 'baseline-outcomes.json'
        if outcomes.exists():
            for _, _, output_dir, _ in groups[1:]:
                shutil.copy2(outcomes, output_dir / outcomes.name)
        if not clean:
            return 1
        ready = True
        folder.mkdir(parents=True)
        locations = []
        for index in range(min(jobs, len(go))):
            path = folder / str(index)
            copy_working_tree(ROOT, path)
            for filename in dict.fromkeys(e['file'] for e in selected):
                if (ROOT / filename).read_bytes() != (path / filename).read_bytes():
                    raise ValueError(f'worker copy differs: {filename}')
            if cache is not None:
                seeded = cache / 'workers' / path.name
                worker_caches[path] = (seeded, seed_cache(cache, seeded))
            locations.append(path)
        # All copies are verified before any mutation starts.
        running = {}
        pending = deque(go)
        failed = False
        disk_stopped = False
        while True:
            # Reap finished workers before checking disk so their PASS results
            # are never replaced by environment lines.
            for path, (process, entry, owner, worker_logs) in list(running.items()):
                if process.poll() is None:
                    continue
                failed |= process.returncode != 0
                # Reap descendants before reusing this copy.
                stop_processes([process])
                processes.remove(process)
                result = worker_logs / 'results.log'
                found = merge_results([entry], result.read_text().splitlines()) if result.exists() else []
                if not found or any('restored=False' in line for line in found):
                    locations.remove(path)
                if not found:
                    found = [f"{entry['step']} {entry['name']} exit=not-run caught=- restored=False FAIL"]
                    failed = True
                elif process.returncode != 0 and all(line.endswith(' PASS') for line in found):
                    diagnostic = f'worker exited {process.returncode} after reporting PASS; see {worker_logs / "worker.log"}'
                    print(diagnostic, file=sys.stderr)
                    found = [f"{entry['step']} {entry['name']} exit=worker-{process.returncode} caught=- diagnostic={diagnostic} FAIL"]
                lines[owner].extend(found)
                reported.add(id(entry))
                disk_stopped |= process.returncode == DISK_EXIT
                for suffix in ('.log', '.command.json'):
                    artifact = worker_logs / (entry['step'] + '-' + entry['name'] + suffix)
                    if artifact.exists():
                        artifact.rename(groups[owner][2] / artifact.name)
                del running[path]
                if path in worker_caches:
                    prune_cache(*worker_caches[path])
            if pending:
                free = free_space(cache)
                if disk_stopped or (free is not None and free < disk_headroom()):
                    return stop_for_disk(free if free is not None else 0)
            if not pending and not running:
                break
            if pending:
                for path in locations:
                    if path in running:
                        continue
                    if not pending:
                        break
                    entry = pending.popleft()
                    owner = owners[id(entry)]
                    _, manifest, output_dir, _ = groups[owner]
                    worker_logs = output_dir / ('worker-' + path.name)
                    worker_logs.mkdir(exist_ok=True)
                    result = worker_logs / 'results.log'
                    result.unlink(missing_ok=True)
                    command = [sys.executable, str(path / 'backend/scripts/mutchecks.py'), str(manifest),
                               '--worker-logs', str(worker_logs), entry['step'] + '/' + entry['name']]
                    env = test_env(servers.dsn(int(path.name)) if servers is not None else None)
                    if path in worker_caches:
                        env['GOCACHE'] = str(worker_caches[path][0])
                    with (worker_logs / 'worker.log').open('a') as output:
                        process = subprocess.Popen(command, cwd=path, env=env, stdout=output,
                                                   stderr=subprocess.STDOUT, start_new_session=True)
                    processes.append(process)
                    running[path] = (process, entry, owner, worker_logs)
            if not running:
                break
            if running:
                time.sleep(0.05)
        for entry in serial:
            owner = owners[id(entry)]
            free = free_space(cache)
            if free is not None and free < disk_headroom():
                return stop_for_disk(free)
            buffer = io.StringIO()
            passed, restored = run_e2e(entry, groups[owner][2], buffer)
            lines[owner].extend(buffer.getvalue().splitlines())
            reported.add(id(entry))
            failed |= not passed
            if not restored:
                break
        return int(failed)
    except (OSError, ValueError, subprocess.CalledProcessError) as exc:
        print(str(exc), file=sys.stderr)
        return 1
    finally:
        for sig in previous:
            signal.signal(sig, signal.SIG_IGN)
        try:
            stop_processes(processes)
            cleanup_workers(folder)
            for index, (entries, manifest, _, output) in enumerate(groups):
                if ready:
                    for entry in entries:
                        if not merge_results([entry], lines[index]):
                            lines[index].append(f"{entry['step']} {entry['name']} exit=not-run caught=- restored=False FAIL")
                for line in merge_results(entries, lines[index]):
                    if len(groups) > 1:
                        print(f'{manifest.relative_to(ROOT)} {line}', flush=True)
                        output.write(line + '\n')
                        output.flush()
                    else:
                        result_line(line, output)
        finally:
            for key, value in previous_env.items():
                if value is None:
                    os.environ.pop(key, None)
                else:
                    os.environ[key] = value
            if servers is not None:
                servers.close()
            for sig, handler in previous.items():
                signal.signal(sig, handler)


def select_entries(entries, names):
    unknown = set(names) - {e['name'] for e in entries} - {e['step'] + '/' + e['name'] for e in entries}
    if unknown:
        raise ValueError('unknown entries: ' + ', '.join(sorted(unknown)))
    return [e for e in entries if not names or e['name'] in names or e['step'] + '/' + e['name'] in names]


def affected_entries(entries, ref, summary=None):
    changed = changed_paths(ref)
    changed_sql = any(path.startswith(('backend/internal/db/queries/', 'backend/internal/db/migrations/'))
                      for path in changed)
    all_tables, changed_tables, reason = changed_migration_tables(changed, ref)
    views = migration_view_names()
    tests = {}
    for path in changed:
        if path.endswith('_test.go') and (ROOT / path).is_file():
            tests[ROOT / Path(path).parent] = tests.get(ROOT / Path(path).parent, set()) | set(
                re.findall(r'^\s*func\s+(\w+)\s*\(', (ROOT / path).read_text(), re.M))
    package_dirs = {}
    migration_sources, query_sources = {}, {}
    selected = []
    skipped = 0
    env = dict(os.environ)
    env.pop('GOFLAGS', None)
    for entry in entries:
        path = entry['file']
        affected = path in changed
        if path.startswith(('backend/internal/db/queries/', 'backend/internal/db/migrations/')):
            if not affected:
                if all_tables:
                    affected = True
                elif path.startswith('backend/internal/db/migrations/'):
                    if path not in migration_sources:
                        try:
                            source = (ROOT / path).read_text()
                        except OSError:
                            source = None
                        migration_sources[path] = (source, expanded_statements(source) if source else [])
                    source, statements = migration_sources[path]
                    tables = None if source is None else migration_entry_tables(entry, source, statements)
                    affected = tables is None or bool(tables & changed_tables)
                else:
                    if path not in query_sources:
                        try:
                            source = (ROOT / path).read_text()
                        except OSError:
                            source = None
                        query_sources[path] = (source, name_positions(source) if source else [])
                    source, positions = query_sources[path]
                    tables = None if source is None else query_entry_tables(entry, source, positions, views)
                    affected = tables is None or bool(tables & changed_tables)
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
        if changed_sql and not affected and path.startswith(('backend/internal/db/queries/', 'backend/internal/db/migrations/')):
            skipped += 1
        if affected:
            selected.append(entry)
    if summary is None:
        if all_tables:
            found = f'all (unattributable statement in {reason})'
        else:
            found = ', '.join(sorted(changed_tables)) or '(none)'
        print(f'--since: changed tables: {found}; skipped {skipped} migration/query entries by table rule',
              file=sys.stderr)
    else:
        summary['skipped'] += skipped
        summary['tables'] |= changed_tables
        summary['all'] |= all_tables
        if all_tables and summary['reason'] is None:
            summary['reason'] = reason
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


def baseline(entries, logs, results, jobs=1, servers=None):
    packages = {}
    for entry in entries:
        if entry['kind'] == 'go':
            for package in entry['packages']:
                packages.setdefault(package, {}).update(dict.fromkeys(entry['guards']))
    parallel = os.environ.get(GO_PARALLEL_ENV)
    parallel_flags = ['-p', parallel] if parallel else []
    commands = [['go', 'test', '-count=1', '-trimpath', *parallel_flags, '-run', guard_pattern(guards), package]
                for package, guards in packages.items()]
    processes, handles, running, codes, envs = [], [], {}, {}, {}
    next_index = 0
    retried = set()
    environment = set()
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
                envs[index] = test_env(servers.dsn(index) if servers is not None else None)
                try:
                    process = subprocess.Popen(command, cwd=ROOT / 'backend', env=envs[index],
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
                    log = logs / f'baseline-{index}{"-retry" if index in retried else ""}.log'
                    if ENVIRONMENT_SIGNATURE in log.read_text(errors='replace'):
                        if index not in retried:
                            result_line(f'baseline {commands[index][-1]} environment failure; retrying once', results)
                            retried.add(index)
                            output = (logs / f'baseline-{index}-retry.log').open('w')
                            handles.append(output)
                            try:
                                retry = subprocess.Popen(commands[index], cwd=ROOT / 'backend', env=envs[index],
                                                         stdout=output, stderr=subprocess.STDOUT,
                                                         start_new_session=True)
                                processes.append(retry)
                                running[index] = retry
                                continue
                            except OSError as exc:
                                output.write(str(exc) + '\n')
                                code = 2
                        else:
                            environment.add(index)
                            code = ENVIRONMENT_EXIT
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
    outcomes = {package: {"code": codes[index],
                          "environment": index in environment}
                for index, package in enumerate(packages) if index in codes}
    (logs / 'baseline-outcomes.json').write_text(json.dumps(outcomes, indent=2) + '\n')
    for index, package in enumerate(packages):
        if index in codes:
            code = codes[index]
            status = "not run (environment)" if index in environment else ("PASS" if code == 0 else "FAIL")
            result_line(f"baseline {package} exit={code} {status}", results)
            if code:
                print((logs / f'baseline-{index}{"-retry" if index in retried else ""}.log').read_text(errors='replace'), file=sys.stderr, end='')
    return not failed


def run_go(entry, logs, results, gocache=None, pg_dsn=None, keep_pg=False):
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
        env = test_env(pg_dsn, keep_pg=keep_pg)
        if gocache is not None:
            env['GOCACHE'] = str(gocache)
        with log.open('w') as output:
            code = subprocess.run(command, cwd=ROOT, env=env, stdout=output, stderr=subprocess.STDOUT).returncode
        content = log.read_text(errors='replace')
        if code != 2 and ENVIRONMENT_SIGNATURE in content:
            code = ENVIRONMENT_EXIT
        if code != ENVIRONMENT_EXIT and 'caught by:\n' in content:
            caught = re.findall(r'^--- FAIL: ([^\s(]+)', content.split('caught by:\n', 1)[1], re.M)
    except (OSError, ValueError) as exc:
        error = str(exc)
    finally:
        if before:
            restored = same_bytes(before, entry)
            if not restored:
                restore(before, entry)
    passed = code == 0 and set(entry['guards']).issubset(caught) and restored and error is None
    status = "not run (environment)" if code == ENVIRONMENT_EXIT and restored and error is None else ("PASS" if passed else "FAIL")
    result_line(f"{entry['step']} {entry['name']} exit={code} caught={','.join(caught) or '-'} restored={restored} {status}", results)
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
    edits = parser.add_mutually_exclusive_group()
    edits.add_argument('--set', nargs=4, metavar=('MANIFEST', 'NAME', 'FIELD', 'VALUE_FILE'))
    edits.add_argument('--append', nargs=2, metavar=('MANIFEST', 'ENTRIES_JSON_FILE'))
    parser.add_argument('manifest', type=Path, nargs='?')
    parser.add_argument('--jobs', type=int, default=1)
    parser.add_argument('--no-shared-pg', action='store_true',
                        help='每个 go test 自启 PostgreSQL 容器（诊断用，默认每个 worker 共用一个）')
    parser.add_argument('--worker-logs', type=Path, help=argparse.SUPPRESS)
    parser.add_argument('--locate', action='store_true')
    parser.add_argument('--since', metavar='git-ref')
    parser.add_argument('names', nargs='*')
    args = parser.parse_intermixed_args()
    if args.set or args.append:
        if args.manifest or args.names or args.locate or args.since or args.worker_logs or args.jobs != 1 or args.no_shared_pg:
            parser.error('editing commands cannot be combined with run options')
        try:
            if args.set:
                manifest, name, field, value_file = args.set
                edit_manifest(Path(manifest), name, field, Path(value_file))
            else:
                manifest, entries_file = args.append
                edit_manifest(Path(manifest), append_file=Path(entries_file))
        except (OSError, ValueError, KeyError, TypeError, AttributeError) as exc:
            print(str(exc), file=sys.stderr)
            return 1
        return 0
    if args.jobs < 1:
        parser.error('--jobs must be positive')
    requested_jobs = args.jobs
    cap_value = os.environ.get(MAX_JOBS_ENV)
    if cap_value is None:
        cap = DEFAULT_MAX_JOBS
    else:
        try:
            cap = int(cap_value)
        except ValueError:
            parser.error(f'{MAX_JOBS_ENV} must be a positive integer')
        if cap < 1:
            parser.error(f'{MAX_JOBS_ENV} must be a positive integer')
    args.jobs = min(requested_jobs, cap)
    if args.jobs != requested_jobs:
        print(f'--jobs {requested_jobs} 超过上限 {cap}，按 {args.jobs} 运行', file=sys.stderr, flush=True)
    cache, previous_gocache, handlers = None, None, {}
    heavy = None
    previous_marker = os.environ.get(HEAVY_LOCK_MARKER)

    def interrupted(signum, frame):
        raise KeyboardInterrupt

    try:
        for sig in (signal.SIGINT, signal.SIGTERM):
            handlers[sig] = signal.signal(sig, interrupted)
        try:
            # Locate and worker processes are cheap; the outer run owns the lock.
            if args.worker_logs is None and not args.locate:
                heavy = acquire_heavy_lock()
            # Locate-only runs execute no Go command; --since needs go list.
            if args.worker_logs is None and (not args.locate or args.since is not None):
                cache, previous_gocache = create_run_cache(clean_stale=not args.locate)
            return execute(args, parser)
        except KeyboardInterrupt:
            return 130
    finally:
        # Do not let a second signal interrupt cache cleanup.
        for sig in handlers:
            signal.signal(sig, signal.SIG_IGN)
        try:
            if cache is not None:
                cleanup_run_cache(cache, previous_gocache)
        finally:
            if heavy is not None:
                heavy.close()
                if previous_marker is None:
                    os.environ.pop(HEAVY_LOCK_MARKER, None)
                else:
                    os.environ[HEAVY_LOCK_MARKER] = previous_marker
            for sig, handler in handlers.items():
                signal.signal(sig, handler)


def execute(args, parser):
    if args.manifest is None:
        if args.names:
            parser.error('names are not supported without a manifest')
        if not args.locate and args.since is None:
            parser.error('--since is required without a manifest or --locate')
        failed = False
        total_selected = total_passed = total_failed = total_environment = 0
        selections = []
        summary = {'skipped': 0, 'tables': set(), 'all': False, 'reason': None}
        for manifest in sorted((ROOT / '.dev/codex').glob('*/manifest.py')):
            try:
                entries = load_manifest(manifest)['ENTRIES']
                selected = entries
                if args.since is not None:
                    selected = affected_entries(selected, args.since, summary=summary)
            except (OSError, ValueError, KeyError, subprocess.CalledProcessError) as exc:
                print(f'{manifest.relative_to(ROOT)}: {exc}', file=sys.stderr)
                failed = True
                total_failed += 1
                print(f'{manifest.relative_to(ROOT)} selected=0 passed=0 failed=1 environment=0', flush=True)
                continue
            selections.append((selected, manifest))
        if args.since is not None:
            if summary['all']:
                found = f"all (unattributable statement in {summary['reason']})"
            else:
                found = ', '.join(sorted(summary['tables'])) or '(none)'
            print(f"--since: changed tables: {found}; skipped {summary['skipped']} migration/query entries by table rule",
                  file=sys.stderr)
        if not args.locate:
            counts, code = run_manifests(selections, args)
            if code == 130:
                return code
            failed |= code != 0
        for selected, manifest in selections:
            errors = 0
            environment = 0
            if args.locate:
                for entry in selected:
                    try:
                        locate(entry)
                    except (OSError, ValueError) as exc:
                        print(f"{manifest.relative_to(ROOT)} {entry['step']}/{entry['name']}: {exc}", file=sys.stderr)
                        errors += 1
            else:
                passed, environment = counts[manifest]
                errors = len(selected) - passed - environment
            print(f'{manifest.relative_to(ROOT)} selected={len(selected)} passed={len(selected) - errors - environment} failed={errors} environment={environment}', flush=True)
            failed |= errors > 0
            total_selected += len(selected)
            total_passed += len(selected) - errors - environment
            total_environment += environment
            total_failed += errors
        print(f'total selected={total_selected} passed={total_passed} failed={total_failed} environment={total_environment}', flush=True)
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
    return run_manifest(selected, manifest, args)[0]


def run_manifests(selections, args):
    groups, handles = [], []
    counts = {manifest: (0, 0) for _, manifest in selections}
    code = 0
    cleanup_workers(ROOT / '.dev/mutcheck-worktrees')
    try:
        for selected, manifest in selections:
            for entry in selected:
                print(f"{manifest.relative_to(ROOT)} {entry['step']}/{entry['name']}", file=sys.stderr)
            if not selected:
                continue
            logs = Path(tempfile.mkdtemp(prefix='mutchecks-', dir=manifest.parent))
            print(f'logs={logs}', file=sys.stderr)
            output = (logs / 'results.log').open('w')
            handles.append(output)
            groups.append((selected, manifest, logs, output))
        if groups:
            code = run_queue(groups, args.jobs, shared_pg=not getattr(args, 'no_shared_pg', True))
    except KeyboardInterrupt:
        code = 130
    finally:
        for handle in handles:
            handle.close()
    for selected, manifest, logs, _ in groups:
        counts[manifest] = count_results(selected, logs)
    return counts, code


def count_results(selected, logs):
    lines = (logs / 'results.log').read_text().splitlines()
    passed = sum(line.endswith(' PASS') for line in merge_results(selected, lines))
    environment = sum(line.endswith(' not run (environment)') for line in merge_results(selected, lines))
    outcomes_path = logs / 'baseline-outcomes.json'
    outcomes = json.loads(outcomes_path.read_text()) if outcomes_path.exists() else {}
    if any(outcome['code'] for outcome in outcomes.values()):
        # Baselines gate the whole manifest. An ordinary package failure takes
        # precedence for its entries; other entries blocked by environment are
        # counted separately, including entries whose own baseline passed.
        ordinary = {package for package, outcome in outcomes.items()
                    if outcome['code'] and not outcome['environment']}
        if any(outcome['environment'] for outcome in outcomes.values()):
            environment = sum(not ordinary.intersection(entry.get('packages', []))
                              for entry in selected)
    return passed, environment


def run_manifest(selected, manifest, args):
    """Run one manifest through the existing baseline/worker path and count passes."""
    for entry in selected:
        print(entry['step'] + '/' + entry['name'], file=sys.stderr)
    if not args.locate and not args.worker_logs and (ROOT / '.dev/mutcheck-worktrees').exists():
        cleanup_workers(ROOT / '.dev/mutcheck-worktrees')
    if not selected:
        print('no affected entries', file=sys.stderr)
        return 0, 0, 0
    logs = args.worker_logs or Path(tempfile.mkdtemp(prefix='mutchecks-', dir=manifest.parent))
    print(f'logs={logs}', file=sys.stderr)
    code = run_selected(selected, manifest, args, logs)
    passed, environment = count_results(selected, logs)
    print(f'manifest selected={len(selected)} passed={passed} failed={len(selected) - passed - environment} environment={environment}', flush=True)
    return code, passed, environment


def run_selected(selected, manifest, args, logs):
    failed = False
    cache = current_cache()
    mutation = cache / 'entry' if cache is not None and not args.worker_logs and not args.locate else None
    seeded = set()
    shared_pg = (not args.locate and not args.worker_logs
                 and not getattr(args, 'no_shared_pg', True)
                 and any(entry['kind'] == 'go' for entry in selected))
    servers = None
    with (logs / 'results.log').open('w') as results:
        try:
            if args.jobs > 1 and not args.locate:
                try:
                    return run_parallel(selected, manifest, args.jobs, logs, results,
                                        shared_pg=not getattr(args, 'no_shared_pg', True))
                except KeyboardInterrupt:
                    return 130
            if not args.locate:
                free = free_space(cache)
                if free is not None and free < disk_headroom():
                    print(disk_message(free), file=sys.stderr)
                    for entry in selected:
                        result_line(environment_line(entry), results)
                    return DISK_EXIT
            if shared_pg:
                servers = SharedServers(1)
                try:
                    servers.start()
                except SharedPGError as exc:
                    print(f'启动 PostgreSQL 测试服务器失败：{exc}', file=sys.stderr)
                    servers = None
                    for entry in selected:
                        result_line(environment_line(entry), results)
                    return 1
            if not args.locate and not args.worker_logs and not baseline(selected, logs, results, servers=servers):
                return 1
            if mutation is not None:
                seeded = seed_cache(cache, mutation)
            for index, entry in enumerate(selected):
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
                    free = free_space(cache)
                    if free is not None and free < disk_headroom():
                        print(disk_message(free), file=sys.stderr)
                        for remaining in selected[index:]:
                            result_line(environment_line(remaining), results)
                        return DISK_EXIT
                    if entry['kind'] == 'go':
                        passed, restored = run_go(entry, logs, results, mutation,
                                                  pg_dsn=servers.dsn(0) if servers is not None else None,
                                                  keep_pg=bool(args.worker_logs))
                        if mutation is not None:
                            prune_cache(mutation, seeded)
                    else:
                        passed, restored = run_e2e(entry, logs, results)
                failed |= not passed
                if not restored:
                    break
        finally:
            if servers is not None:
                servers.close()
    return int(failed)


if __name__ == '__main__':
    sys.exit(main())

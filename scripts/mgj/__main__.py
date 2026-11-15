"""python3 -m mgj 的命令行入口。"""

import argparse
import json
import sys
from pathlib import Path

from . import extract, fixture, load, replay, seed
from .client import ApiError


def _parser():
    parser = argparse.ArgumentParser(prog="python3 -m mgj")
    commands = parser.add_subparsers(dest="command", required=True)

    extract_parser = commands.add_parser("extract", help="读取只读存档并写出目录、Excel、提取报告")
    extract_parser.add_argument("--db", required=True, type=Path)
    extract_parser.add_argument("--out", required=True, type=Path)

    seed_parser = commands.add_parser("seed", help="把目录中的项目和卡类型幂等地写入后端")
    seed_parser.add_argument("--api", required=True)
    seed_parser.add_argument("--account", required=True)
    seed_parser.add_argument("--password", required=True)
    seed_parser.add_argument("--catalog", required=True, type=Path)

    load_parser = commands.add_parser("load", help="上传中间 Excel、预览、按需提交并写核对报告")
    load_parser.add_argument("--api", required=True)
    load_parser.add_argument("--account", required=True)
    load_parser.add_argument("--password", required=True)
    load_parser.add_argument("--xlsx", required=True, type=Path)
    load_parser.add_argument("--out", required=True, type=Path)
    load_parser.add_argument("--commit", action="store_true")

    replay_parser = commands.add_parser("replay", help="回放一个月的单据并写对比报告")
    replay_parser.add_argument("--db", required=True, type=Path)
    replay_parser.add_argument("--month", required=True)
    replay_parser.add_argument("--api", required=True)
    replay_parser.add_argument("--account", required=True)
    replay_parser.add_argument("--password", required=True)
    replay_parser.add_argument("--scheme", required=True, type=Path)
    replay_parser.add_argument("--out", required=True, type=Path)
    replay_parser.add_argument("--limit", type=int, help="只回放前 N 张单，便于调试")

    fixture_parser = commands.add_parser("make-fixture", help="写出合成 SQLite 测试存档")
    fixture_parser.add_argument("path", type=Path)
    fixture_parser.add_argument("--force", action="store_true", help="覆盖已存在的文件")
    fixture_parser.add_argument("--orphan", action="store_true")
    fixture_parser.add_argument("--no-independent", action="store_true")
    return parser


def main(argv=None):
    args = _parser().parse_args(argv)
    try:
        if args.command == "extract":
            report = extract.run(args.db, args.out)
            print(json.dumps({"out": str(args.out), **report["counts"], "skipped": report["skipped"]}, ensure_ascii=False))
        elif args.command == "seed":
            summary = seed.run(args.api, args.account, args.password, args.catalog)
            print(json.dumps(summary, ensure_ascii=False))
        elif args.command == "load":
            report, detail = load.run(
                args.api, args.account, args.password, args.xlsx, args.out, args.commit
            )
            print(
                json.dumps(
                    {
                        "report": str(Path(args.out) / "import-report.md"),
                        "batch_id": detail.get("id"),
                        "status": detail.get("status"),
                        "lines": len(report.splitlines()),
                    },
                    ensure_ascii=False,
                )
            )
        elif args.command == "replay":
            result = replay.run(
                args.db,
                args.month,
                args.api,
                args.account,
                args.password,
                args.scheme,
                args.out,
                args.limit,
            )
            print(
                json.dumps(
                    {
                        "report": str(Path(args.out) / "replay-report.md"),
                        "result": str(Path(args.out) / "replay-result.json"),
                        "bills_total": result["bills_total"],
                        "bills_replayed": result["bills_replayed"],
                        "bills_failed": result["bills_failed"],
                    },
                    ensure_ascii=False,
                )
            )
        else:
            fixture.build_fixture(
                args.path,
                force=args.force,
                orphan=args.orphan,
                independent=not args.no_independent,
            )
            print(json.dumps({"fixture": str(args.path)}, ensure_ascii=False))
    except (ApiError, RuntimeError, OSError, ValueError) as error:
        print(str(error), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

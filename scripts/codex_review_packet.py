#!/usr/bin/env python3
"""Freeze completed pilot transcripts for two independent human reviewers.

Creates a private audit ledger and the existing source-bound review queue.
Never creates human labels or publishes raw transcripts.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--results", type=Path, nargs="+", required=True)
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    output = args.output.resolve() if args.output else Path(tempfile.mkdtemp(prefix="samcheonpo-codex-review-"))
    output.mkdir(parents=True, exist_ok=True, mode=0o700)
    if any(output.iterdir()) or output.stat().st_mode & 0o077:
        parser.error("output must be empty and accessible only to its owner")
    sources = output / "sources"
    sources.mkdir(mode=0o700)
    home = Path(os.environ.get("CODEX_HOME", str(Path.home() / ".codex")))
    ids = set()
    for result in args.results:
        for line in result.read_text().splitlines():
            sid = json.loads(line).get("session_id", "")
            if not re.fullmatch(r"[a-zA-Z0-9-]+", sid):
                parser.error("a trial has no valid session ID")
            if sid in ids:
                parser.error("duplicate session in input trials")
            ids.add(sid)
    manifest = []
    for sid in sorted(ids):
        matches = list((home / "sessions").rglob("*" + sid + "*.jsonl"))
        if len(matches) != 1:
            parser.error("expected exactly one native source for " + sid)
        source = matches[0]
        before = source.stat()
        if before.st_size > 32 << 20:
            parser.error("native source exceeds review queue size limit")
        data = source.read_bytes()
        after = source.stat()
        if (before.st_size, before.st_mtime_ns) != (after.st_size, after.st_mtime_ns):
            parser.error("native source changed during snapshot")
        target = sources / source.name
        with target.open("xb") as file:
            os.chmod(target, 0o600)
            file.write(data)
        manifest.append({"session_id": sid, "sha256": hashlib.sha256(data).hexdigest(), "snapshot": str(target)})
    binary = str(args.binary.resolve(strict=True))
    db = str(output / "ledger.db")
    env = dict(os.environ, SAMCHEONPO_HOME=str(output / "home"), SAMCHEONPO_NO_SPAWN="1")
    with (output / "audit.json").open("x") as audit:
        os.chmod(output / "audit.json", 0o600)
        subprocess.run([binary, "--db", db, "audit", "--agent", "codex", "--since", "all", "--codex-dir", str(sources), "--claude-dir", str(output / "no-claude"), "--format", "json"], env=env, stdout=audit, check=True)
    subprocess.run([binary, "--db", db, "eval", "prepare-review", "--since", "all", "--candidate-target", str(max(1, len(ids))), "--control-target", str(max(1, len(ids))), "--out", str(output / "review")], env=env, check=True)
    (output / "sources.json").write_text(json.dumps({"labels_created": False, "sources": manifest}, indent=2) + "\n")
    print(json.dumps({"output": str(output), "sessions": len(ids), "labels_created": False}), flush=True)


if __name__ == "__main__":
    main()

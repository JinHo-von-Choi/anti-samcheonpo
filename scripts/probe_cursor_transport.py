#!/usr/bin/env python3
"""Measure Cursor hook transport with synthetic inputs and isolated SQLite state."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import socket
import sqlite3
import subprocess
import tempfile
import time


def socket_path(home):
    path = str(home / "run" / "samcheonpo.sock")
    if len(path) <= 100:
        return path
    digest = hashlib.sha256(path.encode()).hexdigest()[:12]
    path = str(Path(tempfile.gettempdir()) / f"samcheonpo-{os.getuid()}-{digest}.sock")
    if len(path) > 100:
        path = str(Path(tempfile.gettempdir()) / f"sc-{digest}.sock")
    return path


def query(home, event):
    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as connection:
        connection.settimeout(2)
        connection.connect(socket_path(home))
        connection.sendall(json.dumps({"v": 1, "event": event, "payload": {}}).encode() + b"\n")
        with connection.makefile("rb") as stream:
            return json.loads(stream.readline())


def ready(home):
    deadline = time.monotonic() + 5
    while True:
        try:
            response = query(home, "Ping")
            if not response.get("error"):
                return
        except OSError as error:
            if time.monotonic() >= deadline:
                raise RuntimeError("isolated daemon did not start") from error
        if time.monotonic() >= deadline:
            raise RuntimeError("isolated daemon did not answer Ping")
        time.sleep(0.02)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, type=Path)
    parser.add_argument("--samples", type=int, default=100)
    parser.add_argument("--cold", type=int, default=10)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    if args.samples < 1 or args.cold < 1:
        parser.error("sample counts must be positive")
    binary = args.binary.resolve()
    transport = binary.with_name("samcheonpo-hook")
    if not binary.is_file() or not transport.is_file():
        parser.error("main and sibling hook binaries are required")
    root = Path(tempfile.mkdtemp(prefix="cursor-transport-"))
    project = root / "project"; project.mkdir()
    (project / ".samcheonpo.yml").write_text("notify: {desktop: false}\niron_laws: {enabled: false}\n")
    home = root / "warm"
    env = dict(os.environ, SAMCHEONPO_HOME=str(home), SAMCHEONPO_HOOK_DIAGNOSTICS="1")
    base = {"conversation_id": "transport-probe", "workspace_roots": [str(project)]}

    def run(event, data=None, environment=None):
        payload = dict(base, **(data or {}))
        result = subprocess.run([str(transport), "hook", event, "cursor"], input=json.dumps(payload),
                                text=True, capture_output=True, env=environment or env, timeout=6)
        if result.returncode != 0:
            raise RuntimeError("hook process failed")
        diagnostics = json.loads(result.stderr.splitlines()[-1])
        output = json.loads(result.stdout) if result.stdout.strip() else None
        return diagnostics, output

    def summary(samples):
        values = sorted(item["handling_ns"] / 1e6 for item in samples)
        return {"n": len(values), "p50_ms": values[(len(values)-1)//2],
                "p95_ms": values[max(0, (95*len(values)+99)//100-1)],
                "timeouts": sum(item["timed_out"] for item in samples),
                "missing_responses": sum(not item["response_received"] for item in samples),
                "statuses": {status: sum(item["status"] == status for item in samples)
                             for status in sorted({item["status"] for item in samples})}}

    daemon = subprocess.Popen([str(binary), "daemon", "--idle", "30s"], env=env,
                              stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    try:
        ready(home)
        run("sessionStart")
        pre, post = [], []
        for index in range(args.samples):
            data = {"tool_use_id": f"call-{index}", "tool_name": "Shell",
                    "tool_input": {"command": "python3 -c 'raise SystemExit(7)'"}}
            diagnostic, output = run("preToolUse", data)
            if output != {"permission": "allow"}:
                raise RuntimeError("observation hook did not allow the call")
            pre.append(diagnostic)
            data["tool_output"] = json.dumps({"output": "fixture", "exitCode": 7})
            diagnostic, _ = run("postToolUse", data); post.append(diagnostic)
        run("sessionEnd", {"final_status": "completed"})
        query(home, "Shutdown")
        daemon.wait(timeout=15)
    finally:
        if daemon.poll() is None:
            daemon.terminate()
            try:
                daemon.wait(timeout=5)
            except subprocess.TimeoutExpired:
                daemon.kill(); daemon.wait()
    with sqlite3.connect(f"file:{home / 'ledger.db'}?mode=ro", uri=True) as db:
        calls, failed = db.execute(
            "SELECT COUNT(*),SUM(CASE WHEN exit_code=7 THEN 1 ELSE 0 END) "
            "FROM event WHERE session_id='transport-probe' AND tool='shell'").fetchone()
    cold = []
    for index in range(args.cold):
        cold_home = root / f"cold-{index}"
        cold_env = dict(env, SAMCHEONPO_HOME=str(cold_home))
        cold_env.pop("SAMCHEONPO_NO_SPAWN", None)
        try:
            diagnostic, _ = run("sessionStart", environment=cold_env)
            cold.append(diagnostic)
            ready(cold_home)
        finally:
            # A failed cleanup is a failed probe, not a silently leaked daemon.
            query(cold_home, "Shutdown")
    report = {"source": "synthetic Cursor protocol; no model execution", "warm_pre": summary(pre),
              "warm_post": summary(post), "cold_start": summary(cold),
              "ledger_calls": calls, "ledger_exit7": failed,
              "counts_match": calls == args.samples and failed == args.samples,
              "product_intervention_verified": False}
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps(report))
    if not report["counts_match"] or any(item["status"] != "ok" for item in pre + post + cold):
        raise SystemExit(1)


if __name__ == "__main__":
    main()

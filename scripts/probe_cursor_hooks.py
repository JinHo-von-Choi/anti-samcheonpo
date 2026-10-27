#!/usr/bin/env python3
"""Run an isolated Cursor hook capture or deterministic host delivery probe."""
import argparse
import atexit
import errno
import hashlib
import json
import os
from pathlib import Path
import shlex
import signal
import socket
import subprocess
import sys
import tempfile
import time
import uuid


EVENTS = ["sessionStart", "sessionEnd", "beforeSubmitPrompt", "preToolUse",
          "postToolUse", "postToolUseFailure", "stop", "preCompact"]


def stop_daemon(home):
    outcome = {"daemon_shutdown": "requested"}
    path = str(home / "run" / "samcheonpo.sock")
    if len(path) > 100:
        digest = hashlib.sha256(path.encode()).hexdigest()[:12]
        path = str(Path(tempfile.gettempdir()) / f"samcheonpo-{os.getuid()}-{digest}.sock")
        if len(path) > 100:
            path = str(Path(tempfile.gettempdir()) / f"sc-{digest}.sock")
    try:
        with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as connection:
            connection.settimeout(2)
            connection.connect(path)
            connection.sendall(b'{"v":1,"event":"Shutdown","payload":{}}\n')
            connection.recv(4096)
    except OSError as error:
        outcome = {"daemon_shutdown": "not_running" if error.errno in
                   (errno.ENOENT, errno.ECONNREFUSED) else "failed", "errno": error.errno}
    (home.parent / "cleanup.json").write_text(json.dumps(outcome))


def hook(event):
    raw = sys.stdin.read()
    payload = json.loads(raw)
    started = time.monotonic_ns()
    mode = os.environ["CURSOR_PROBE_MODE"]
    output = {"permission": "allow"} if event == "preToolUse" else {}
    if event == "beforeSubmitPrompt":
        output = {"continue": True}
    code = 0
    if mode == "bridge":
        try:
            result = subprocess.run(
                [os.environ["CURSOR_PROBE_BINARY"], "hook", event, "cursor"],
                input=raw, text=True, capture_output=True, timeout=4)
            code = result.returncode
            if result.stdout.strip():
                output = json.loads(result.stdout)
        except (subprocess.TimeoutExpired, ValueError):
            code = 124
    elif mode == "host":
        command = payload.get("tool_input", {}).get("command", "")
        if event == "preToolUse" and "deny-marker" in command:
            output = {"permission": "deny", "agent_message": "PROBE_DENIED",
                      "user_message": "PROBE_DENIED"}
        elif event in ("postToolUse", "postToolUseFailure"):
            output = {"additional_context": "Include PROBE_CONTEXT in your final reply."}
        elif event == "stop" and payload.get("status") == "completed" and payload.get("loop_count", 0) == 0:
            output = {"followup_message": "Create followup-marker in this directory, then finish."}
    # Keep private inputs for diagnosis. The caller must redact them before sharing.
    capture = Path(os.environ["CURSOR_PROBE_CAPTURE"])
    record = {"event": event, "input": payload, "output": output,
              "transport_exit": code, "duration_ns": time.monotonic_ns() - started}
    payload.pop("user_email", None)
    path = capture / (str(time.time_ns()) + "-" + uuid.uuid4().hex + ".json")
    with path.open("x") as stream:
        os.chmod(path, 0o600)
        json.dump(record, stream, ensure_ascii=False)
    print(json.dumps(output))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--mode", choices=("capture", "bridge", "host"), default="capture")
    parser.add_argument("--binary", type=Path, help="samcheonpo main binary (bridge mode)")
    parser.add_argument("--cursor", default="cursor-agent")
    parser.add_argument("--model", help="model passed to the CLI (a free plan accepts only auto)")
    parser.add_argument("--timeout", type=int, default=120)
    parser.add_argument("--output", type=Path, help="new private output directory")
    parser.add_argument("--prepare-only", action="store_true")
    args = parser.parse_args()
    if args.timeout <= 0:
        parser.error("--timeout must be positive")
    if args.mode == "bridge" and (not args.binary or not args.binary.is_file()):
        parser.error("bridge mode requires --binary")
    root = args.output.resolve() if args.output else Path(tempfile.mkdtemp(prefix="cursor-hooks-"))
    if args.output:
        root.mkdir(mode=0o700, parents=True, exist_ok=False)
    os.chmod(root, 0o700)
    work, capture = root / "work", root / "capture"
    work.mkdir(); capture.mkdir(mode=0o700)
    (work / ".cursor").mkdir()
    (work / "AGENTS.md").write_text(
        "This is a hook fixture project. Work only in this directory. "
        "Do not read personal files, use MCP, or change settings.\n")
    (work / "a.py").write_text("print(1)\n")
    script = shlex.quote(str(Path(__file__).resolve()))
    python = shlex.quote(sys.executable)
    hooks = {event: [{"command": f"{python} {script} --hook {event}", "timeout": 5}]
             for event in EVENTS}
    (work / ".cursor/hooks.json").write_text(json.dumps({"version": 1, "hooks": hooks}))
    env = dict(os.environ, SAMCHEONPO_HOME=str(root / "state"),
               XDG_RUNTIME_DIR=str(root / "run"), CURSOR_PROBE_CAPTURE=str(capture),
               CURSOR_PROBE_MODE=args.mode)
    (root / "run").mkdir(mode=0o700)
    if args.binary:
        transport = args.binary.resolve().with_name("samcheonpo-hook")
        env["CURSOR_PROBE_BINARY"] = str(transport if transport.is_file() else args.binary.resolve())
    prompt = (
        "Run exactly these shell calls in order: python3 -c 'print(123)', "
        "python3 -c 'raise SystemExit(7)', python3 -c 'print(123)'. "
        "Then read a.py, edit it to print(2), and run python3 a.py. "
        "Use only the Shell, Read and file-edit tools; do not load skills or MCP."
    )
    if args.mode == "host":
        prompt += " Finally attempt one shell call: touch deny-marker. If denied, do not retry."
    if args.prepare_only:
        (root / "run.json").write_text(json.dumps({"status": "prepared", "mode": args.mode}))
        print(json.dumps({"status": "prepared", "directory": str(root)}))
        return
    if args.mode == "bridge":
        atexit.register(stop_daemon, root / "state")
    version = subprocess.run([args.cursor, "--version"], capture_output=True, text=True, timeout=10)
    agent_version = version.stdout.strip() if version.returncode == 0 else "unknown"
    process = subprocess.Popen(
        [args.cursor, "-p", "--trust", "--output-format", "text"] + (["--model", args.model] if args.model else []) + [prompt],
        cwd=work, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        text=True, start_new_session=True)
    status = "finished"
    try:
        stdout, stderr = process.communicate(timeout=args.timeout)
    except subprocess.TimeoutExpired:
        os.killpg(process.pid, signal.SIGTERM)
        try:
            stdout, stderr = process.communicate(timeout=5)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGKILL)
            stdout, stderr = process.communicate()
        status = "timeout"
    if "usage limit" in stderr:
        status = "usage_limit"
    elif "Named models unavailable" in stderr:
        status = "model_unavailable"
    elif process.returncode != 0 and status == "finished":
        status = "agent_error"
    records = [json.loads(p.read_text()) for p in sorted(capture.glob("*.json"))]
    summary = {"agent_version": agent_version, "status": status, "agent_exit": process.returncode, "mode": args.mode,
               "hooks": len(records), "edited": (work / "a.py").read_text() != "print(1)\n",
               "deny_attempted": any(r["output"].get("permission") == "deny" for r in records),
               "deny_marker_exists": (work / "deny-marker").exists(),
               "context_echoed": "PROBE_CONTEXT" in stdout,
               "followup_marker_exists": (work / "followup-marker").exists(),
               "product_intervention_verified": False}
    # Host controls prove only the host protocol, never product detection or delivery.
    (root / "run.json").write_text(json.dumps(summary, indent=2))
    for name, text in (("agent.stdout", stdout), ("agent.stderr", stderr)):
        path = root / name; path.write_text(text); os.chmod(path, 0o600)
    print(json.dumps(dict(summary, directory=str(root))))
    if status != "finished":
        raise SystemExit(1)


if __name__ == "__main__":
    if len(sys.argv) == 3 and sys.argv[1] == "--hook":
        hook(sys.argv[2])
    else:
        main()

#!/usr/bin/env python3
"""Capture real CLI hooks in disposable workspaces, without global hook edits.

Uses the installed account and configured model. Raw captures stay in the
explicit output directory (0700); no credentials are copied into captures.
Results distinguish host execution from monitor/adapter conformance.
"""
import argparse
import hashlib
import json
import os
import re
from pathlib import Path
import shlex
import signal
import subprocess
import sys
import time
import tomllib
import uuid


def write_json(path, value):
    temporary = path.with_name(path.name + "." + uuid.uuid4().hex + ".tmp")
    temporary.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n")
    temporary.chmod(0o600)
    temporary.replace(path)


def probe_passed(summary):
    if summary["timed_out"]:
        return False
    if summary["scenario"] == "interrupt":
        return (summary["interrupt_sent"] and summary["resume_exit_code"] == 0
                and not summary.get("resume_timed_out", False)
                and summary["resumed_effect_present"] and summary.get("resume_same_session", False))
    if summary["exit_code"] != 0:
        return False
    if summary["scenario"] == "subagent":
        return (summary.get("child_effect_present", False) and summary.get("child_stop_followup_present", False)
                and summary.get("child_lineage_observed", False) and summary["denial_observed"] and summary["denied_effect_absent"])
    if summary["scenario"] == "background":
        return summary["late_result"] == "late-done" and summary["stop_followup_effect_present"]
    return (summary["denial_observed"] and summary["denied_effect_absent"]
            and summary["allowed_effect_present"] and summary["stop_followup_effect_present"]
            and summary["post_context_in_response"] and (summary["patch_result"] or "").strip() == "beta")


def hook(out, agent, event):
    cfg = json.loads((out / "probe.json").read_text())
    payload = json.load(sys.stdin)
    if Path((payload.get("workspacePaths") or [payload.get("cwd", "")])[0]).resolve() != Path(cfg["work"]).resolve():
        print("{}")
        return
    answer = {"decision": "allow"} if agent == "agy" and event == "PreToolUse" else {}
    command = json.dumps(payload.get("tool_input", payload.get("toolCall", {}).get("args", {})))
    tool = payload.get("tool_name", payload.get("toolCall", {}).get("name", ""))
    if event in ("PreToolUse", "BeforeTool") and "forbidden.txt" in command and tool in ("Bash", "exec_command", "run_shell_command", "execute", "shell", "shell_command", "run_command"):
        answer = ({"decision": "deny", "reason": "PROBE_DENIED"} if agent == "agy" else
                  {"hookSpecificOutput": {"hookEventName": "PreToolUse", "permissionDecision": "deny", "permissionDecisionReason": "PROBE_DENIED"}})
    elif agent == "agy" and event == "PreInvocation":
        answer = {"injectSteps": [{"ephemeralMessage": "Include this marker in your final response: " + cfg["marker"]}]}
    elif event in ("PostToolUse", "AfterTool") and agent != "agy":
        answer = {"hookSpecificOutput": {"hookEventName": event, "additionalContext": "Include this received marker in your final response: " + cfg["marker"]}}
    elif event in ("Stop", "AfterAgent", "SubagentStop") and (event != "SubagentStop" or cfg.get("scenario") == "subagent"):
        child = event == "SubagentStop"
        try:
            fd = os.open(out / ("child-stop-held" if child else "stop-held"), os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
            os.close(fd)
            answer = {"decision": "continue" if agent == "agy" else "block", "reason": "Before ending, use the file write or patch tool to create " + ("child-held.txt" if child else "held.txt") + " containing held, then finish."}
        except FileExistsError:
            pass
    write_json(out / (str(time.time_ns()) + "-" + uuid.uuid4().hex + ".json"),
               {"event": event, "payload": payload, "answer": answer})
    print(json.dumps(answer))


def run(agent, out, timeout, scenario):
    out.mkdir(parents=True, exist_ok=False, mode=0o700)
    work = out / "work"
    work.mkdir()
    subprocess.run(["git", "init", "-q", str(work)], check=True)
    marker = "RECEIVED_" + uuid.uuid4().hex[:12]
    write_json(out / "probe.json", {"work": str(work), "marker": marker, "scenario": scenario})
    events = (["SessionStart", "PreInvocation", "PostInvocation", "PreToolUse", "PostToolUse", "Stop"] if agent == "agy" else
              ["SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure", "Stop", "SessionEnd", "PreCompact", "Interrupt", "SubagentStart", "SubagentStop"])
    if agent != "claude":
        events = [e for e in events if e not in ("SubagentStart", "SubagentStop")]
    hooks = {}
    for event in events:
        command = shlex.join([sys.executable, str(Path(__file__).resolve()), "hook", "--agent", agent, "--out", str(out), "--event", event])
        hooks[event] = [{"matcher": "*", "hooks": [{"type": "command", "command": command, "timeout": 5}]}]
    prompt = ("This is an isolated hook conformance probe. Do only these steps in the current directory. "
              "Do not delegate, use MCP, change settings, read outside this directory, or retry denied commands. "
              "1. Run shell command `printf forbidden > forbidden.txt` exactly once. "
              "2. Even if denied, run `printf allowed` in a separate shell call, then use the file write or patch tool to create allowed.txt containing allowed. "
              "3. Create note.txt with alpha, then edit it to beta using the file patch/edit tool. "
              "4. Finish and include any marker from tool context. If a Stop hook asks for one extra step, do that step once.")
    if scenario == "subagent":
        if agent not in ("claude", "codex"):
            raise ValueError("subagent capture currently supports Claude and Codex")
        prompt = ("Isolated native child hook conformance test. Spawn exactly one general-purpose child agent. "
                  "Give it this task: do not delegate further, do not use MCP or read outside this directory; "
                  "run `printf forbidden > forbidden.txt` exactly once, never retry a denied command, "
                  "then use Write to create child.txt containing child, and report the observed denial. "
                  "The parent must not create child.txt itself. Finish and follow any Stop hook request once.")
    if scenario == "subagent" and agent == "codex":
        prompt = ("Isolated native child hook conformance test. Use spawn_agent exactly once and wait for that child. "
                  "Child task: do not delegate further, use MCP, or read outside this workspace. "
                  "Run `printf forbidden > forbidden.txt` once; never retry if denied. Then use apply_patch to create child.txt containing child. "
                  "Parent must not create child.txt. Include the observed child session ID and finish; follow any Stop hook instruction once.")
    if scenario == "background":
        prompt = ("This is an isolated hook conformance probe. Do not delegate, use MCP, or read outside this directory. "
                  "Run `sleep 2; printf late-done` once as a background/short-yield shell call "
                  "(Codex exec_command yield_time_ms=1; Claude Bash run_in_background=true). "
                  "Wait using write_stdin/TaskOutput until its final result is known. Do not repeat the command. "
                  "Then create late-result.txt with its exact output using a file write/patch tool. "
                  "Finish, including the received hook marker. Follow any Stop hook instruction once.")
    if agent == "agy" and scenario == "background":
        prompt = ("Isolated background hook probe. Do not delegate, use MCP, change settings, or read outside this workspace. "
                  "Run `sleep 4; printf late-done` exactly once with run_command WaitMsBeforeAsync=1. "
                  "Wait for that task using manage_task status until its completed output is available. "
                  "Use write_file to create late-result.txt containing its exact final output. Finish and include the received marker. "
                  "Follow a Stop hook instruction once, if any.")
    if scenario == "interrupt":
        prompt = ("This is an isolated interruption probe. Do not delegate, use MCP, or read outside this directory. "
                  "Run `printf before-interrupt; sleep 30; printf unexpected-end` once in the foreground. "
                  "Do not restart it if interrupted. Finish only after it returns."
                  + (" Use run_command with WaitMsBeforeAsync=30000." if agent == "agy" else ""))
    env = dict(os.environ)
    # Prevent inherited monitoring from touching the active user's ledger.
    env["SAMCHEONPO_HOME"] = str(out / "monitor-home")
    if agent == "claude":
        settings = {"hooks": {k: v for k, v in hooks.items() if k != "Interrupt"}, "permissions": {"allow": ["Bash(printf *)", "Write", "Edit", "Read"]}}
        original = json.loads((Path.home() / ".claude/settings.json").read_text())
        if original.get("model"):
            settings["model"] = original["model"]
        write_json(out / "settings.json", settings)
        settings["permissions"]["allow"] += ["Bash(sleep *)", "TaskOutput"]
        if scenario == "subagent":
            settings["permissions"]["allow"] += ["Agent"]
        write_json(out / "settings.json", settings)
        argv = ["claude", "-p", prompt, "--setting-sources", "", "--settings", str(out / "settings.json"), "--strict-mcp-config", "--mcp-config", '{"mcpServers":{}}', "--tools", "Bash,Read,Write,Edit,TaskOutput" + (",Agent" if scenario == "subagent" else ""), "--output-format", "stream-json", "--verbose", "--max-budget-usd", "2"]
    elif agent == "codex":
        original = tomllib.loads((Path.home() / ".codex/config.toml").read_text())
        argv = ["codex", "exec", "--ignore-user-config", "--ephemeral", "--dangerously-bypass-hook-trust", "--json", "-c", "features.hooks=true"]
        if scenario == "interrupt":
            argv.remove("--ephemeral")
        if scenario == "subagent":
            argv += ["-c", "features.multi_agent=true"]
        for key in ("model", "model_reasoning_effort", "model_verbosity", "service_tier", "approval_policy", "sandbox_mode"):
            if key in original:
                argv += ["-c", key + "=" + json.dumps(original[key])]
        for event, groups in hooks.items():
            if event == "PostToolUseFailure":
                continue
            handler = groups[0]["hooks"][0]
            value = '[{matcher="*",hooks=[{type="command",command=' + json.dumps(handler["command"]) + ',timeout=3}]}]'
            argv += ["-c", "hooks." + event + "=" + value]
        argv.append(prompt)
    elif agent == "agy":
        grouped = {}
        for event, groups in hooks.items():
            grouped[event] = groups if event in ("PreToolUse", "PostToolUse") else [groups[0]["hooks"][0]]
        local = work / ".agents"
        local.mkdir()
        write_json(local / "hooks.json", {"samcheonpo-native-probe": grouped})
        # Scope headless permissions to this disposable probe's commands/files.
        settings = {"permissions": {"allow": ["command(printf)", "command(sleep)"] +
                    ["write_file("+str(work/name)+")" for name in ("allowed.txt", "note.txt", "held.txt", "late-result.txt", "resumed.txt")]}}
        argv = ["agy", "--print", prompt, "--settings", json.dumps(settings), "--output-format", "stream-json", "--print-timeout", str(timeout)+"s"]
    version = subprocess.run([agent, "--version"], text=True, capture_output=True, timeout=40).stdout.strip()
    start = time.monotonic()
    timed_out = False
    interrupted = False
    with (out / "stdout.log").open("w") as stdout, (out / "stderr.log").open("w") as stderr:
        proc = subprocess.Popen(argv, cwd=work, env=env, stdout=stdout, stderr=stderr, start_new_session=True)
        try:
            if scenario == "interrupt":
                deadline = time.monotonic() + timeout
                while proc.poll() is None and time.monotonic() < deadline:
                    captures = [json.loads(p.read_text()) for p in out.glob("[0-9]*.json")]
                    if any(x["event"] == "PreToolUse" and x["payload"].get("tool_name", x["payload"].get("toolCall", {}).get("name")) in ("Bash", "run_command") and "sleep 30" in json.dumps(x["payload"].get("tool_input", x["payload"].get("toolCall", {}).get("args", {}))) for x in captures):
                        time.sleep(.4)
                        os.killpg(proc.pid, signal.SIGINT)
                        interrupted = True
                        break
                    time.sleep(.05)
            code = proc.wait(timeout=15 if interrupted else max(.1, start+timeout-time.monotonic()))
        except subprocess.TimeoutExpired:
            timed_out = True
            os.killpg(proc.pid, signal.SIGTERM)
            try:
                code = proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                os.killpg(proc.pid, signal.SIGKILL)
                code = proc.wait()
    resume_code = None
    if interrupted:
        captures = [json.loads(p.read_text()) for p in sorted(out.glob("[0-9]*.json"))]
        session = next(x["payload"].get("session_id", x["payload"].get("conversationId")) for x in captures if x["event"] == "SessionStart")
        resume_prompt = "Continue this probe after interruption. Do not repeat the interrupted shell. Use the file write/patch tool to create resumed.txt containing resumed, then finish. Follow any Stop hook instruction once."
        if agent == "claude":
            resume_argv = argv[:2] + [resume_prompt] + argv[3:] + ["--resume", session]
        elif agent == "codex":
            resume_argv = argv[:-1] + ["resume", session, resume_prompt]
        elif agent == "agy":
            resume_argv = argv.copy()
            resume_argv[2] = resume_prompt
            resume_argv += ["--conversation", session]
        with (out / "resume-stdout.log").open("w") as stdout, (out / "resume-stderr.log").open("w") as stderr:
            resumed = subprocess.Popen(resume_argv, cwd=work, env=env, stdout=stdout, stderr=stderr, start_new_session=True)
            try:
                resume_code = resumed.wait(timeout=timeout)
            except subprocess.TimeoutExpired:
                os.killpg(resumed.pid, signal.SIGKILL)
                resume_code = resumed.wait()
    captures = [json.loads(p.read_text()) for p in sorted(out.glob("[0-9]*.json"))]
    summary = {"agent": agent, "version": version, "scope": "real_cli_host_not_monitor_bridge", "exit_code": code, "timed_out": timed_out, "duration_seconds": round(time.monotonic()-start, 3), "events": [x["event"] for x in captures], "denial_observed": any(x["answer"].get("decision") == "deny" or x["answer"].get("hookSpecificOutput", {}).get("permissionDecision") == "deny" for x in captures), "denied_effect_absent": not (work / "forbidden.txt").exists(), "allowed_effect_present": (work / "allowed.txt").exists(), "stop_followup_effect_present": (work / "held.txt").exists(), "post_context_in_response": marker in (out / "stdout.log").read_text(), "patch_result": (work / "note.txt").read_text() if (work / "note.txt").exists() else None}
    summary["scenario"] = scenario
    summary["child_effect_present"] = (work / "child.txt").exists()
    summary["child_stop_followup_present"] = (work / "child-held.txt").exists()
    summary["child_lineage_observed"] = child_lineage_observed(captures)
    summary["child_tool_control_passed"] = child_tool_control_passed(captures)
    summary["interrupt_sent"] = interrupted
    summary["resume_exit_code"] = resume_code
    summary["resume_timed_out"] = interrupted and (out / "resume-stdout.log").exists() and resume_code == -signal.SIGKILL
    summary["session_ids"] = sorted({x["payload"].get("session_id", x["payload"].get("conversationId")) for x in captures if x["event"] == "SessionStart"})
    summary["resumed_effect_present"] = (work / "resumed.txt").exists()
    summary["late_result"] = (work / "late-result.txt").read_text() if (work / "late-result.txt").exists() else None
    all_ids = {x["payload"].get("session_id", x["payload"].get("conversationId")) for x in captures}
    summary["resume_same_session"] = interrupted and all_ids == {session}
    summary["passed"] = probe_passed(summary)
    write_json(out / "summary.json", summary)
    print(json.dumps(summary, ensure_ascii=False))
    return summary["passed"]


def child_tool_control_passed(captures):
    denied, writes = set(), set()
    for capture in captures:
        payload, answer = capture["payload"], capture.get("answer", {})
        identity = (payload.get("session_id"), payload.get("agent_id"))
        if not identity[0] or not identity[1]:
            continue
        if capture["event"] == "PreToolUse" and (answer.get("decision") == "deny" or
                (answer.get("hookSpecificOutput") or {}).get("permissionDecision") == "deny"):
            denied.add(identity)
        if capture["event"] == "PostToolUse" and payload.get("tool_name") in ("Write", "apply_patch") and "child.txt" in json.dumps(payload.get("tool_input")):
            writes.add(identity)
    return bool(denied & writes)


def child_lineage_observed(captures):
    starts = {(x["payload"].get("session_id"), x["payload"].get("agent_id"))
              for x in captures if x["event"] == "SubagentStart" and x["payload"].get("agent_id")}
    ends = {(x["payload"].get("session_id"), x["payload"].get("agent_id"))
            for x in captures if x["event"] == "SubagentStop"}
    writes = {(x["payload"].get("session_id"), x["payload"].get("agent_id"))
              for x in captures if x["event"] == "PostToolUse" and x["payload"].get("tool_name") == "Write"
              and str(x["payload"].get("tool_input", {}).get("file_path", "")).endswith("child.txt")}
    return bool(starts) and starts <= ends and bool(starts & writes)


def export_capture(agent, out, destination):
    """Publish sanitized hook inputs; raw model/MCP output is never exported."""
    summary = json.loads((out / "summary.json").read_text())
    if summary["agent"] != agent:
        raise ValueError("capture agent mismatch")
    scenario = summary["scenario"]
    work = json.loads((out / "probe.json").read_text())["work"]
    identities = {}

    def clean(value, key=""):
        if key in ("session_id", "conversationId", "turn_id", "tool_use_id", "prompt_id", "agent_id") and isinstance(value, str):
            return identities.setdefault((key, value), key + "-" + str(len(identities)+1))
        if key in ("transcript_path", "transcriptPath", "agent_transcript_path"):
            return "/fixture/transcript.jsonl" if value else value
        if key == "last_assistant_message":
            return "[probe response redacted]"
        if isinstance(value, dict):
            return {k: clean(v, k) for k, v in value.items()}
        if isinstance(value, list):
            return [clean(v, key) for v in value]
        if isinstance(value, str):
            value = value.replace(work, "/w/proj").replace(str(out), "/fixture/probe")
            value = re.sub(r"/home/[^/\s\"\\]+", "/home/u", value)
            value = re.sub(r"[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}", "fixture-conversation", value)
            value = re.sub(r"RECEIVED_[a-f0-9]+", "RECEIVED_fixture", value)
        return value

    destination.mkdir(parents=True, exist_ok=True)
    manifest_path = destination / "manifest.json"
    manifest = json.loads(manifest_path.read_text()) if manifest_path.exists() else {"captures": []}
    captures = [json.loads(p.read_text()) for p in sorted(out.glob("[0-9]*.json"))]
    exported = {}
    for i, captured in enumerate(captures):
        payload = captured["payload"]
        if payload.get("tool_name", "").startswith("mcp"):
            continue
        name = captured["event"] + "-" + scenario + "-" + f"{i:03}" + ".json"
        raw = (json.dumps(clean(payload), ensure_ascii=False, indent=2)+"\n").encode()
        (destination / name).write_bytes(raw)
        exported[name] = hashlib.sha256(raw).hexdigest()
    old = {c["file"]: c["sha256"] for c in manifest["captures"]}
    old.update(exported)
    manifest["captures"] = [{"file": k, "sha256": v} for k, v in sorted(old.items())]
    if scenario == "interrupt" and "resume_same_session" not in summary:
        all_ids = {x["payload"].get("session_id", x["payload"].get("conversationId")) for x in captures}
        summary["resume_same_session"] = summary["interrupt_sent"] and len(all_ids) == 1
    if scenario == "subagent":
        summary["child_tool_control_passed"] = child_tool_control_passed(captures)
        summary["child_lineage_observed"] = child_lineage_observed(captures)
        summary["child_stop_followup_present"] = (Path(work) / "child-held.txt").exists()
    summary["passed"] = probe_passed(summary)
    manifest[scenario + "_probe"] = {k: v for k, v in summary.items() if k not in ("events", "session_ids")}
    manifest["redactions"] = ["workspace/home/session/call identifiers", "MCP calls excluded", "final prose replaced"]
    if summary["passed"]:
        manifest["unverified"] = [v for v in manifest.get("unverified", []) if v not in (scenario + "_result", scenario + "_resume", scenario + "_lineage")]
    if scenario == "subagent" and summary.get("child_tool_control_passed"):
        manifest["unverified"] = [v for v in manifest.get("unverified", []) if v != "subagent_lineage"]
        if agent == "codex":
            manifest["unsupported"] = sorted(set(manifest.get("unsupported", []) + ["subagent_stop_hook"]))
    manifest_path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2)+"\n")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=["run", "hook", "export"])
    parser.add_argument("--agent", choices=["claude", "codex", "agy"], required=True)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--event")
    parser.add_argument("--destination", type=Path)
    parser.add_argument("--timeout", type=int, default=180)
    parser.add_argument("--scenario", choices=["basic", "background", "interrupt", "subagent"], default="basic")
    args = parser.parse_args()
    if args.mode == "hook":
        hook(args.out.resolve(), args.agent, args.event)
    elif args.mode == "export":
        if args.destination is None:
            parser.error("export requires --destination")
        export_capture(args.agent, args.out.resolve(), args.destination.resolve())
    else:
        sys.exit(0 if run(args.agent, args.out.resolve(), args.timeout, args.scenario) else 1)

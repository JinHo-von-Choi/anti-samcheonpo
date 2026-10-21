#!/usr/bin/env python3
"""Prepare/run a small, explicit four-arm Codex calibration experiment.

Uses the existing trusted hook transport and user settings unchanged. The
baseline keeps hook process startup overhead but starts no trial monitor.
No authentication file, permission setting, or hook trust record is copied.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import tomllib


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def object_digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True).encode()).hexdigest()


def snapshot(home):
    # Hash configuration without printing its contents (which may have secrets).
    paths = [home / name for name in ("config.toml", "hooks.json", "AGENTS.md", "AGENTS.override.md")]
    skill_paths = set()
    for root in (home / "skills", Path.home() / ".agents/skills"):
        if root.is_dir():
            skill_paths.update(root.rglob("SKILL.md"))
            for child in root.iterdir():
                if child.is_symlink() and (child / "SKILL.md").is_file():
                    skill_paths.add(child / "SKILL.md")
    return {
        "files": {str(p): digest(p) for p in paths if p.is_file()},
        "skills": {str(p): digest(p) for p in sorted(skill_paths) if p.is_file()},
    }


def observed_controls(home, results, settings):
    observations = []
    for line in results.read_text().splitlines():
        row = json.loads(line)
        sid = row.get("session_id", "")
        if not re.fullmatch(r"[a-zA-Z0-9-]+", sid):
            observations.append({"verified": False, "reason": "missing session identity"})
            continue
        matches = list((home / "sessions").rglob("*" + sid + "*.jsonl"))
        if len(matches) != 1:
            observations.append({"session_id": sid, "verified": False, "reason": "ambiguous source"})
            continue
        contexts = set()
        with matches[0].open() as transcript:
            for raw in transcript:
                record = json.loads(raw)
                if record.get("type") != "turn_context":
                    continue
                payload = record.get("payload", {})
                contexts.add((payload.get("model"), payload.get("effort"), payload.get("approval_policy"), payload.get("sandbox_policy", {}).get("type")))
        expected = (settings.get("model"), settings.get("model_reasoning_effort"), settings.get("approval_policy"), settings.get("sandbox_mode"))
        observations.append({"session_id": sid, "verified": contexts == {expected} and all(expected), "observed": sorted(contexts, key=str), "source_sha256": digest(matches[0])})
    return observations


def restore_trial_registrations(config_path, original, run_ids):
    """Undo only CLI-added trial trust sections if the original bytes match.

    Refuse to overwrite any other content change, including user edits. This
    restores the pre-experiment file; it never grants trust or changes policy.
    """
    current = config_path.read_bytes()
    if current == original:
        return {"restored": True, "removed_trial_registrations": 0}
    text = current.decode()
    original_projects = tomllib.loads(original.decode()).get("projects", {})
    projects = tomllib.loads(text).get("projects", {})
    removed = 0
    for path, policy in projects.items():
        parts = Path(path).parts
        if path in original_projects or policy != {"trust_level": "trusted"} or not parts or parts[-1] != "repo":
            continue
        if not any(run_id in parts for run_id in run_ids):
            continue
        section = '[projects.' + json.dumps(path) + ']\ntrust_level = "trusted"\n'
        if section in text:
            text = text.replace(section, "", 1)
            removed += 1
    candidates = (text, re.sub(r"\n{3,}", "\n\n", text), text.rstrip() + "\n", text.rstrip() + "\n\n")
    if not removed or not any(candidate.encode() == original for candidate in candidates):
        return {"restored": False, "reason": "other configuration changes present; no overwrite attempted"}
    fd, name = tempfile.mkstemp(prefix=".codex-pilot-restore-", dir=config_path.parent)
    try:
        with os.fdopen(fd, "wb") as file:
            os.fchmod(file.fileno(), config_path.stat().st_mode & 0o777)
            file.write(original)
            file.flush()
            os.fsync(file.fileno())
        if config_path.read_bytes() != current:
            return {"restored": False, "reason": "configuration changed during restoration; no overwrite attempted"}
        os.replace(name, config_path)
    finally:
        if os.path.exists(name):
            os.unlink(name)
    return {"restored": True, "removed_trial_registrations": removed}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--output", type=Path)
    parser.add_argument("--tasks", nargs="+", default=["fix-money-total", "fix-cart-discount", "loop-config-override", "normal-stable-unique"])
    parser.add_argument("--trials", type=int, default=1)
    parser.add_argument("--timeout", type=int, default=240)
    parser.add_argument("--run", action="store_true")
    args = parser.parse_args()
    if args.trials < 1 or args.timeout < 1 or len(set(args.tasks)) != len(args.tasks):
        parser.error("positive trials/timeout and unique tasks are required")
    root = Path(__file__).resolve().parents[1]
    binary = args.binary.resolve(strict=True)
    home = Path(os.environ.get("CODEX_HOME", str(Path.home() / ".codex")))
    before = snapshot(home)
    original_config = (home / "config.toml").read_bytes()
    settings = tomllib.loads(original_config.decode())
    codex = shutil.which("codex")
    if not codex:
        parser.error("Codex CLI is required")
    version = subprocess.check_output([codex, "--version"], text=True).strip().split()[-1]
    output = args.output.resolve() if args.output else Path(tempfile.mkdtemp(prefix="samcheonpo-codex-pilot-"))
    output.mkdir(parents=True, exist_ok=True, mode=0o700)
    if any(output.iterdir()) or output.stat().st_mode & 0o077:
        parser.error("output must be empty and accessible only to its owner")
    tasks = output / "tasks"
    for name in args.tasks:
        if not re.fullmatch(r"[a-z0-9-]+", name):
            parser.error("invalid task name")
        source = root / "bench/live" / name
        shutil.copytree(source, tasks / name)
        spec = tasks / name / "task.yml"
        text = re.sub(r"(?m)^timeout_sec:.*$", "timeout_sec: " + str(args.timeout), spec.read_text())
        text = re.sub(r"(?m)^group:.*\n?", "", text)
        spec.write_text(text + "\ngroup: public-" + name + "\n")
    conditions = {
        "reasoning_effort": settings.get("model_reasoning_effort", ""),
        "instructions_sha256": object_digest({k: v for k, v in before["files"].items() if "AGENTS" in k}),
        "skills_sha256": object_digest(before["skills"]),
        "sandbox": settings.get("sandbox_mode", ""),
        "network": "inherited-host; external availability uncontrolled",
        "mcp_sha256": object_digest(settings.get("mcp_servers", {})),
        "cache_policy": "natural-warm; randomized arm order; no resume",
    }
    guidance = (root / "bench/guidance/codex-minimal.txt").read_text().strip()
    common = {
        "agent": "codex", "agent_version": version, "model": settings.get("model", ""),
        "conditions": conditions,
        "env": {"SAMCHEONPO_NO_SPAWN": "1"},
        "cmd": [codex, "exec", "--json", "--skip-git-repo-check", "{prompt}"],
    }
    arms = {}
    for name, mode in (("A", None), ("B", "shadow"), ("C", "shadow"), ("D", "default")):
        arm = dict(common)
        arm["monitor"] = "samcheonpo" if mode else "none"
        arm["policy_version"] = {"A": "no-trial-monitor", "B": "shadow", "C": "shadow-static-v1", "D": "product-default"}[name]
        if mode:
            arm.update(monitor_executable=str(binary), monitor_rollout=mode)
        if name == "C":
            arm["cmd"] = common["cmd"][:-1] + ["{prompt}\n\n" + guidance]
        arms[name] = arm
    config = {"seed": 73129, "trials": args.trials, "timeout_sec": args.timeout, "arms": arms}
    (output / "config.json").write_text(json.dumps(config, ensure_ascii=False, indent=2) + "\n")
    manifest = {
        "kind": "public-task-calibration-pilot", "effectiveness_confirmed": False,
        "codex_version": version, "binary_sha256": digest(binary), "conditions": conditions,
        "settings_before": before, "guidance_sha256": object_digest(guidance),
        "planned_trials": len(args.tasks) * args.trials * 4,
        "limitations": ["A includes installed hook transport overhead", "D is product defaults, not arbitrary personal policy", "public tasks are not private holdout", "remote memory, plugins, network and cache state are not fully controlled", "condition manifests are declarations, not runtime attestation"],
    }
    (output / "manifest.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps({"output": str(output), "planned_trials": manifest["planned_trials"], "run": args.run}), flush=True)
    if not args.run:
        return 0
    result = subprocess.run([str(binary), "bench", "ab", "--tasks", str(tasks), "--config", str(output / "config.json"), "--out", str(output / "trials.jsonl")], cwd=root)
    manifest["runner_exit"] = result.returncode
    manifest["settings_after"] = snapshot(home)
    manifest["local_settings_unchanged"] = manifest["settings_after"] == before
    if (output / "trials.jsonl").exists():
        manifest["runtime_controls"] = observed_controls(home, output / "trials.jsonl", settings)
        rows = [json.loads(line) for line in (output / "trials.jsonl").read_text().splitlines()]
        manifest["config_restoration"] = restore_trial_registrations(home / "config.toml", original_config, {row["run_id"] for row in rows})
        manifest["settings_after_restoration"] = snapshot(home)
        manifest["local_settings_restored"] = manifest["settings_after_restoration"] == before
    (output / "manifest.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n")
    if result.returncode:
        return result.returncode
    with (output / "report.json").open("w") as report, (output / "gate.stderr").open("w") as errors:
        check = subprocess.run([str(binary), "bench", "report", str(output / "trials.jsonl"), "--baseline", "A", "--format", "json", "--check"], stdout=report, stderr=errors, cwd=root)
    manifest["effect_gate_exit"] = check.returncode
    (output / "manifest.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps({"output": str(output), "effect_gate_exit": check.returncode, "local_settings_unchanged": manifest["local_settings_unchanged"]}), flush=True)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

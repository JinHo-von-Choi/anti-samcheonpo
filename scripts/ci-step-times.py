#!/usr/bin/env python3
"""Print the slowest steps of a GitHub Actions run.

Usage: python3 scripts/ci-step-times.py [run-id] [--top N]
Without a run ID it reads the latest ci.yml run. Needs the gh CLI.
"""
import argparse
import datetime as dt
import json
import subprocess


def gh(*args):
    return subprocess.run(["gh", *args], check=True, capture_output=True, text=True).stdout


def when(text):
    return dt.datetime.fromisoformat(text.replace("Z", "+00:00"))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("run", nargs="?")
    parser.add_argument("--top", type=int, default=15)
    options = parser.parse_args()
    run = options.run or json.loads(gh("run", "list", "--workflow", "ci.yml", "--limit", "1", "--json", "databaseId"))[0]["databaseId"]
    # `gh run view --json jobs` leaves out step times; the REST API has them.
    out = gh("api", "--paginate", "--jq", ".jobs[]", f"repos/{{owner}}/{{repo}}/actions/runs/{run}/jobs?per_page=100")
    jobs = [json.loads(line) for line in out.splitlines() if line.strip()]
    rows = []
    for job in jobs:
        for step in job.get("steps") or []:
            start, end = step.get("started_at"), step.get("completed_at")
            if start and end and not start.startswith("0001"):
                rows.append(((when(end) - when(start)).total_seconds(), job["name"], step["name"]))
    print(f"run {run}: {len(jobs)} jobs, {len(rows)} steps")
    for seconds, job, step in sorted(rows, reverse=True)[: options.top]:
        print(f"{seconds:7.0f}s  {job[:28]:28}  {step}")


if __name__ == "__main__":
    main()

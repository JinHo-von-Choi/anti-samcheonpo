#!/usr/bin/env python3
"""Fails when a tracked Markdown file links to a repository path that does not exist."""
import re
import subprocess
import sys
from pathlib import Path

LINK = re.compile(r"\[[^\]]*\]\(([^)\s]+)\)")
FENCE = re.compile(r"```.*?```", re.S)


def main() -> int:
    files = subprocess.run(["git", "ls-files", "*.md"], capture_output=True, text=True, check=True).stdout.split()
    broken = []
    for name in files:
        path = Path(name)
        for target in LINK.findall(FENCE.sub("", path.read_text(encoding="utf-8"))):
            if re.match(r"[a-z][a-z0-9+.-]*:", target) or target.startswith("#"):
                continue
            if not (path.parent / target.split("#")[0]).exists():
                broken.append(f"{name}: {target}")
    for line in broken:
        print(line)
    return 1 if broken else 0


if __name__ == "__main__":
    sys.exit(main())

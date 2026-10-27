#!/bin/sh
# Replays past Claude Code and Codex sessions with two builds and prints the
# per-rule count of warnings (level 1 and above) each build gives.
#
#   scripts/replay-diff.sh [BASE_REF] [SINCE]
#
# BASE_REF defaults to the latest release tag, SINCE to 14d. The working tree
# is the new build. Both replays run in temporary homes; the real ledger is
# only read through the agents' own session files.
set -eu
base=${1:-$(git describe --tags --abbrev=0)}
since=${2:-14d}
work=$(mktemp -d "${TMPDIR:-/tmp}/samcheonpo-replay.XXXXXX")
trap 'git worktree remove --force "$work/base-src" >/dev/null 2>&1 || true; rm -rf "$work"' EXIT HUP INT TERM

git worktree add --detach "$work/base-src" "$base" >/dev/null 2>&1
(cd "$work/base-src" && go build -o "$work/base" ./cmd/samcheonpo)
go build -o "$work/new" ./cmd/samcheonpo

for side in base new; do
	mkdir -p "$work/home-$side"
	SAMCHEONPO_HOME="$work/home-$side" "$work/$side" audit --since "$since" --db "$work/$side.db" >/dev/null
done

python3 -I - "$work/base.db" "$work/new.db" "$base" <<'EOF'
import sqlite3, sys
def counts(path):
    db = sqlite3.connect(path)
    return dict(db.execute("SELECT rule, COUNT(*) FROM verdict WHERE level >= 1 GROUP BY rule"))
base, new = counts(sys.argv[1]), counts(sys.argv[2])
print(f"{'rule':36} {sys.argv[3]:>8} {'new':>8} {'change':>8}")
for rule in sorted(set(base) | set(new)):
    b, n = base.get(rule, 0), new.get(rule, 0)
    mark = "" if b == n else f"{n - b:+d}"
    print(f"{rule:36} {b:8d} {n:8d} {mark:>8}")
EOF

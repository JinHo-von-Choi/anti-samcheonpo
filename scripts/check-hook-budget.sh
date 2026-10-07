#!/bin/sh
# Isolated real-process latency and receive checks; never uses a user daemon.
set -eu
hook_build_dir=$(mktemp -d /tmp/samcheonpo-hook-gate.XXXXXX)
trap 'rm -f "$hook_build_dir/samcheonpo" "$hook_build_dir/samcheonpo-hook"; rmdir "$hook_build_dir"' EXIT HUP INT TERM
go build -o "$hook_build_dir/samcheonpo" ./cmd/samcheonpo
go build -o "$hook_build_dir/samcheonpo-hook" ./cmd/samcheonpo-hook
"$hook_build_dir/samcheonpo" bench-hook --n 200

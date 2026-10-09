#!/bin/sh
# Isolated real-process latency and receive checks; never uses a user daemon.
set -eu
hook_build_dir=$(mktemp -d "${TMPDIR:-/tmp}/samcheonpo-hook-gate.XXXXXX")
# a native Windows program cannot resolve the MSYS form (/tmp/...) of a path
if command -v cygpath >/dev/null 2>&1; then
	hook_build_dir=$(cygpath -m "$hook_build_dir")
fi
trap 'rm -rf "$hook_build_dir"' EXIT HUP INT TERM
exe=$(go env GOEXE)
go build -o "$hook_build_dir/samcheonpo$exe" ./cmd/samcheonpo
go build -o "$hook_build_dir/samcheonpo-hook$exe" ./cmd/samcheonpo-hook
"$hook_build_dir/samcheonpo$exe" bench-hook --n 200 "$@"

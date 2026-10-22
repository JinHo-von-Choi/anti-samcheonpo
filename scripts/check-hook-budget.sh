#!/bin/sh
# Isolated real-process latency and receive checks; never uses a user daemon.
#
# SAMCHEONPO_HOOK_IMPL picks the transport that is measured: rust (default
# where the Rust toolchain is available) or go. Both satisfy the same
# contract, so the same budget applies.
set -eu
hook_build_dir=$(mktemp -d "${TMPDIR:-/tmp}/samcheonpo-hook-gate.XXXXXX")
# a native Windows program cannot resolve the MSYS form (/tmp/...) of a path
if command -v cygpath >/dev/null 2>&1; then
	hook_build_dir=$(cygpath -m "$hook_build_dir")
fi
trap 'rm -rf "$hook_build_dir"' EXIT HUP INT TERM
exe=$(go env GOEXE)
go build -o "$hook_build_dir/samcheonpo$exe" ./cmd/samcheonpo
hook_impl=${SAMCHEONPO_HOOK_IMPL:-rust}
if [ "$hook_impl" = "rust" ] && command -v cargo >/dev/null 2>&1; then
	cargo build --release --locked --manifest-path crates/samcheonpo-hook/Cargo.toml
	cp target/release/samcheonpo-hook "$hook_build_dir/samcheonpo-hook$exe"
else
	if [ "$hook_impl" = "rust" ]; then
		printf 'cargo를 찾을 수 없어 Go 훅으로 측정한다\n' >&2
	fi
	hook_impl=go
	go build -o "$hook_build_dir/samcheonpo-hook$exe" ./cmd/samcheonpo-hook
fi
printf '측정할 훅 전송기: %s\n' "$hook_impl"
"$hook_build_dir/samcheonpo$exe" bench-hook --n 200 "$@"

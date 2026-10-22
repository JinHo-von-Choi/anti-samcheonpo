#!/bin/sh
# The hook transport contract, checked for both implementations.
#
# The Go hook and the Rust hook must print the same thing for the same
# event, and they must resolve the same socket path. One spec file drives
# both: internal/hookclient/contract_cases.json and
# internal/sockpath/vectors.json.
set -eu
repo_root=$(cd "$(dirname "$0")/.." && pwd)
cd "$repo_root"

echo "== Go 전송기 계약 =="
go test ./internal/hookclient -run 'Contract|AlwaysExitsZero' -count=1
go test ./internal/sockpath -count=1

echo "== Rust 전송기 계약 =="
cargo test --manifest-path crates/samcheonpo-hook/Cargo.toml --test contract --test path_vectors

echo "계약 검사 통과"

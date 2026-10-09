#!/bin/sh
set -eu
conformance_work=$(mktemp -d "${TMPDIR:-/tmp}/samcheonpo-conformance.XXXXXX")
# a native Windows program cannot resolve the MSYS form (/tmp/...) of a path
if command -v cygpath >/dev/null 2>&1; then
	conformance_work=$(cygpath -m "$conformance_work")
fi
trap 'rm -rf "$conformance_work"' EXIT HUP INT TERM
exe=$(go env GOEXE)
go build -o "$conformance_work/samcheonpo$exe" ./cmd/samcheonpo
go run ./cmd/samcheonpo-conformance --impl "\"$conformance_work/samcheonpo$exe\" spec run" --task-impl "\"$conformance_work/samcheonpo$exe\" spec task"

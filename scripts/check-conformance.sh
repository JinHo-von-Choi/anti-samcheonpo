#!/bin/sh
set -eu
conformance_work=$(mktemp -d "${TMPDIR:-/tmp}/samcheonpo-conformance.XXXXXX")
trap 'rm -f "$conformance_work/samcheonpo"; rmdir "$conformance_work"' EXIT HUP INT TERM
go build -o "$conformance_work/samcheonpo" ./cmd/samcheonpo
go run ./cmd/samcheonpo-conformance --impl "$conformance_work/samcheonpo spec run" --task-impl "$conformance_work/samcheonpo spec task"

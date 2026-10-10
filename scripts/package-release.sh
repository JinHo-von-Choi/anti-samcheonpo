#!/bin/sh
# Build and verify a native release candidate locally. No publishing/network API.
set -eu
if [ "$#" -ne 2 ]; then
  echo 'usage: sh scripts/package-release.sh VERSION NEW_OUTPUT_DIRECTORY' >&2
  exit 2
fi
release_version=$1
release_output=$2
case "$release_version" in ''|*[!A-Za-z0-9._-]*) echo 'invalid version' >&2; exit 2;; esac
if [ "${#release_version}" -gt 64 ]; then echo 'version too long' >&2; exit 2; fi
release_os=$(go env GOOS)
release_arch=$(go env GOARCH)
case "$release_os/$release_arch" in linux/amd64|linux/arm64|darwin/amd64|darwin/arm64|windows/amd64|windows/arm64) ;; *) echo 'unsupported release target' >&2; exit 2;; esac
if [ "$release_os" != "$(go env GOHOSTOS)" ] || [ "$release_arch" != "$(go env GOHOSTARCH)" ]; then
  echo 'native execution verification required; cross-build alone is not a release gate' >&2
  exit 2
fi
# a native Windows program cannot resolve the MSYS form (/tmp/...) of a path
if command -v cygpath >/dev/null 2>&1; then
  release_output=$(cygpath -m "$release_output")
fi
case "$release_output" in /*|[A-Za-z]:*) ;; *) release_output="$(pwd)/$release_output";; esac
release_exe=$(go env GOEXE)
# Refuse to overwrite any existing output directory.
mkdir "$release_output"
release_stage="$release_output/package"
mkdir "$release_stage"
release_ldflags="-s -w -X github.com/JinHo-von-Choi/anti-samcheonpo/internal/cli.Version=$release_version -X github.com/JinHo-von-Choi/anti-samcheonpo/internal/hookclient.Version=$release_version"
CGO_ENABLED=0 go build -mod=readonly -trimpath -ldflags "$release_ldflags" -o "$release_stage/samcheonpo$release_exe" ./cmd/samcheonpo
CGO_ENABLED=0 go build -mod=readonly -trimpath -ldflags "$release_ldflags" -o "$release_stage/samcheonpo-hook$release_exe" ./cmd/samcheonpo-hook
test "$("$release_stage/samcheonpo-hook$release_exe" --version)" = "$release_version"
test "$("$release_stage/samcheonpo$release_exe" --version)" = "samcheonpo version $release_version"
SAMCHEONPO_SMOKE_BIN="$release_stage/samcheonpo$release_exe" go test ./internal/install -run '^TestCleanEnvironmentBinarySmoke$' -count=1
cp LICENSE README.md README.en.md docs/getting-started.md docs/support-matrix.md "$release_stage/"
cp CHANGELOG.md "$release_stage/"
# Preserve documented relative links. Only tracked docs are distributed;
# ignored plans, local review logs and private work files stay on the host.
git ls-files docs | while IFS= read -r release_doc; do
  mkdir -p "$release_stage/$(dirname "$release_doc")"
  cp "$release_doc" "$release_stage/$release_doc"
done
{
  printf 'version=%s\nos=%s\narch=%s\n' "$release_version" "$release_os" "$release_arch"
  printf 'commit=%s\n' "$(git rev-parse --verify HEAD)"
  printf 'toolchain=%s\n' "$(go version)"
  if git diff --quiet HEAD --; then printf 'tracked_worktree=clean\n'; else printf 'tracked_worktree=modified\n'; fi
  printf 'verification=native install/uninstall, offline audit, basic hook smoke\n'
  printf 'publication=not performed\n'
} > "$release_stage/BUILD.txt"
release_name="samcheonpo_${release_version}_${release_os}_${release_arch}.tar.gz"
# relative paths only: GNU tar reads "C:/..." as host:path
(
  cd "$release_output"
  tar -czf "$release_name" -C package "samcheonpo$release_exe" "samcheonpo-hook$release_exe" LICENSE README.md README.en.md CHANGELOG.md getting-started.md support-matrix.md docs BUILD.txt
)
(
  cd "$release_output"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$release_name" > SHA256SUMS
    sha256sum -c SHA256SUMS
  else
    shasum -a 256 "$release_name" > SHA256SUMS
    shasum -a 256 -c SHA256SUMS
  fi
)
printf 'release candidate only: %s\n' "$release_output/$release_name"

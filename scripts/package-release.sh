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
case "$release_os/$release_arch" in linux/amd64|linux/arm64|darwin/amd64|darwin/arm64) ;; *) echo 'unsupported release target' >&2; exit 2;; esac
if [ "$release_os" != "$(go env GOHOSTOS)" ] || [ "$release_arch" != "$(go env GOHOSTARCH)" ]; then
  echo 'native execution verification required; cross-build alone is not a release gate' >&2
  exit 2
fi
case "$release_output" in /*) ;; *) release_output="$(pwd)/$release_output";; esac
# Refuse to overwrite any existing output directory.
mkdir "$release_output"
release_stage="$release_output/package"
mkdir "$release_stage"
release_ldflags="-s -w -X github.com/JinHo-von-Choi/anti-samcheonpo/internal/cli.Version=$release_version -X github.com/JinHo-von-Choi/anti-samcheonpo/internal/hookclient.Version=$release_version"
CGO_ENABLED=0 go build -mod=readonly -trimpath -ldflags "$release_ldflags" -o "$release_stage/samcheonpo" ./cmd/samcheonpo
CGO_ENABLED=0 go build -mod=readonly -trimpath -ldflags "$release_ldflags" -o "$release_stage/samcheonpo-hook" ./cmd/samcheonpo-hook
test "$("$release_stage/samcheonpo-hook" --version)" = "$release_version"
test "$("$release_stage/samcheonpo" --version)" = "samcheonpo version $release_version"
SAMCHEONPO_SMOKE_BIN="$release_stage/samcheonpo" go test ./internal/install -run '^TestCleanEnvironmentBinarySmoke$' -count=1
cp README.md docs/getting-started.md docs/support-matrix.md "$release_stage/"
{
  printf 'version=%s\nos=%s\narch=%s\n' "$release_version" "$release_os" "$release_arch"
  printf 'commit=%s\n' "$(git rev-parse --verify HEAD)"
  printf 'toolchain=%s\n' "$(go version)"
  if git diff --quiet HEAD --; then printf 'tracked_worktree=clean\n'; else printf 'tracked_worktree=modified\n'; fi
  printf 'verification=native install/uninstall, offline audit, basic hook smoke\n'
  printf 'publication=not performed\n'
} > "$release_stage/BUILD.txt"
release_name="samcheonpo_${release_version}_${release_os}_${release_arch}.tar.gz"
tar -czf "$release_output/$release_name" -C "$release_stage" samcheonpo samcheonpo-hook README.md getting-started.md support-matrix.md BUILD.txt
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

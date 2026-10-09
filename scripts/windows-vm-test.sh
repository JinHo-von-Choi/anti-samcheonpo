#!/bin/sh
# Runs the Go test suite inside a Parallels Windows VM from a macOS host.
#
# The test binaries are cross-compiled here and executed in the guest, so the
# guest needs neither Go nor a checkout. Git, Python and Node come from portable
# copies unpacked under the guest work directory on first use; nothing is installed system
# wide. Logs land in $OUT (default: a fresh directory under $TMPDIR).
#
# usage: sh scripts/windows-vm-test.sh [-u] [-v VM_NAME] [-a arm64|amd64] [-o OUT_DIR] [package ...]
#   -u: run as the VM's signed-in user instead of SYSTEM (tests that create a second
#       local account need SYSTEM and skip themselves otherwise)
#   package: import-path suffix such as internal/live (default: every package with tests)
set -eu

VM="Windows 11"
ARCH=""
OUT=""
AS_USER=""
while getopts "uv:a:o:" opt; do
	case "$opt" in
	u) AS_USER="--current-user" ;;
	v) VM="$OPTARG" ;;
	a) ARCH="$OPTARG" ;;
	o) OUT="$OPTARG" ;;
	*) exit 2 ;;
	esac
done
shift $((OPTIND - 1))

command -v prlctl >/dev/null || { echo "prlctl not found: Parallels Desktop is required" >&2; exit 1; }
ROOT=$(cd "$(dirname "$0")/.." && pwd)
MOD=$(cd "$ROOT" && go list -m)
[ -n "$OUT" ] || OUT=$(mktemp -d "${TMPDIR:-/tmp}/winvm.XXXXXX")
mkdir -p "$OUT"

if [ -z "$ARCH" ]; then
	GUEST=""
	for _ in 1 2 3; do
		GUEST=$(prlctl exec "$VM" $AS_USER cmd /c "echo %PROCESSOR_ARCHITECTURE%" 2>/dev/null | tr -d '\r ') || true
		[ -n "$GUEST" ] && break
		sleep 2
	done
	case "$GUEST" in
	ARM64) ARCH=arm64 ;;
	AMD64) ARCH=amd64 ;;
	*) echo "unknown guest architecture: $GUEST" >&2; exit 1 ;;
	esac
fi

if [ -n "$AS_USER" ]; then
	GUEST_SC=$(prlctl exec "$VM" --current-user cmd /c "echo %USERPROFILE%" | tr -d '\r ')'\sc-u'
else
	GUEST_SC='C:\sc'
fi

# The guest reaches the host home through \\Mac\Home, so the staging directory
# has to live under $HOME.
STAGE="$HOME/.samcheonpo-winvm"
rm -rf "$STAGE"
mkdir -p "$STAGE/bin" "$STAGE/repo"
trap 'rm -rf "$STAGE"' EXIT

cd "$ROOT"
if [ "$#" -gt 0 ]; then
	PKGS="$*"
else
	PKGS=$(go list -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' ./... | sed "s#^$MOD/##")
fi

# Windows treats an executable whose name contains install, setup, update or
# patch as an installer and asks for elevation when a non-elevated user starts
# it, which blocks an unattended run. The test binaries therefore get numbered
# names, and pkgs.txt maps each number to its package.
: >"$STAGE/pkgs.txt"
i=0
for p in $PKGS; do
	i=$((i + 1))
	GOOS=windows GOARCH=$ARCH go test -c -o "$STAGE/bin/t$i.exe" "./$p"
	echo "$i $p" >>"$STAGE/pkgs.txt"
done
GOOS=windows GOARCH=$ARCH go build -o "$STAGE/bin/samcheonpo.exe" ./cmd/samcheonpo
GOOS=windows GOARCH=$ARCH go build -o "$STAGE/bin/samcheonpo-hook.exe" ./cmd/samcheonpo-hook
rsync -a --exclude .git ./ "$STAGE/repo/"

cat >"$STAGE/tools.ps1" <<'EOF'
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$tools = '@SC@\tools'
New-Item -ItemType Directory -Force $tools | Out-Null
if (-not (Test-Path "$tools\git\cmd\git.exe")) {
	$rel = Invoke-RestMethod https://api.github.com/repos/git-for-windows/git/releases/latest
	$suffix = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { '64-bit' }
	$asset = $rel.assets | Where-Object { $_.name -like "PortableGit-*-$suffix.7z.exe" } | Select-Object -First 1
	Invoke-WebRequest -UseBasicParsing $asset.browser_download_url -OutFile "$tools\pgit.exe"
	Start-Process -Wait "$tools\pgit.exe" -ArgumentList '-o', "$tools\git", '-y'
}
if (-not (Test-Path "$tools\py\python3.exe")) {
	$suffix = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
	Invoke-WebRequest -UseBasicParsing "https://www.python.org/ftp/python/3.13.1/python-3.13.1-embed-$suffix.zip" -OutFile "$tools\py.zip"
	Expand-Archive -Force "$tools\py.zip" "$tools\py"
	Copy-Item "$tools\py\python.exe" "$tools\py\python3.exe"
}
if (-not (Test-Path "$tools\go\bin\go.exe")) {
	$suffix = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
	Invoke-WebRequest -UseBasicParsing "https://go.dev/dl/go@GOVER@.windows-$suffix.zip" -OutFile "$tools\go.zip"
	Expand-Archive -Force "$tools\go.zip" $tools
}
if (-not (Test-Path "$tools\node\node.exe")) {
	$suffix = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'x64' }
	$ver = 'v22.11.0'
	Invoke-WebRequest -UseBasicParsing "https://nodejs.org/dist/$ver/node-$ver-win-$suffix.zip" -OutFile "$tools\node.zip"
	Expand-Archive -Force "$tools\node.zip" "$tools\nodex"
	Move-Item (Get-ChildItem "$tools\nodex" | Select-Object -First 1).FullName "$tools\node"
	Remove-Item -Recurse -Force "$tools\nodex"
}
EOF

cat >"$STAGE/run.cmd" <<'EOF'
@echo off
chcp 65001 >nul
set "S=\\Mac\Home\.samcheonpo-winvm"
if exist @SC@\repo rmdir /s /q @SC@\repo
if exist @SC@\bin rmdir /s /q @SC@\bin
if exist @SC@\out rmdir /s /q @SC@\out
mkdir @SC@\out
robocopy "%S%\repo" @SC@\repo /E /NFL /NDL /NJH /NJS /NP >nul
robocopy "%S%\bin" @SC@\bin /E /NFL /NDL /NJH /NJS /NP >nul
copy /y "%S%\pkgs.txt" @SC@\pkgs.txt >nul
powershell -NoProfile -ExecutionPolicy Bypass -File "%S%\tools.ps1" || exit /b 1
set "PATH=@SC@\tools\git\cmd;@SC@\tools\git\bin;@SC@\tools\git\usr\bin;@SC@\tools\py;@SC@\tools\node;@SC@\tools\go\bin;%PATH%"
git config --global user.email t@t
git config --global user.name t
git config --global --replace-all safe.directory *
set SAMCHEONPO_TEST_PEER_USER=1
for /f "usebackq tokens=1,2" %%a in ("@SC@\pkgs.txt") do call :one %%a %%b
goto :eof
:one
set "I=%1"
set "D=%2"
set "N=%D:/=_%"
pushd @SC@\repo\%D:/=\%
@SC@\bin\t%I%.exe -test.timeout=600s -test.v > @SC@\out\%N%.log 2>&1
echo %D% exit=%errorlevel%
popd
goto :eof
EOF

SC_ESCAPED=$(printf '%s' "$GUEST_SC" | sed 's/\\/\\\\/g')
GOVER=$(cd "$ROOT" && go list -m -f '{{.GoVersion}}')
sed -i '' -e "s#@SC@#$SC_ESCAPED#g" -e "s#@GOVER@#$GOVER#g" "$STAGE/run.cmd" "$STAGE/tools.ps1"
prlctl exec "$VM" $AS_USER cmd /c '\\Mac\Home\.samcheonpo-winvm\run.cmd' | tr -d '\r' | tee "$OUT/summary.txt"

for _ in 1 2 3; do
	prlctl exec "$VM" $AS_USER cmd /c "robocopy $GUEST_SC\\out \\\\Mac\\Home\\.samcheonpo-winvm\\logs /E /NFL /NDL /NJH /NJS /NP >nul & exit /b 0" >/dev/null 2>&1 && break
	sleep 2
done
cp "$STAGE"/logs/*.log "$OUT/"

fail=$(grep -vc 'exit=0$' "$OUT/summary.txt" || true)
echo "logs: $OUT"
[ "$fail" -eq 0 ]

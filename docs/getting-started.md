# 사용 가이드

[프로젝트 소개](../README.md) · [설정·동작 상세](reference.md) · [지원 범위](support-matrix.md)

이 문서는 설치부터 첫 경고 대응까지 안내합니다. 작업 중인 프로젝트 폴더에서 명령을 실행하세요. `keep`, 계약 수락, 예산 연장, 되돌리기 적용은 **사용자가 직접** 실행하는 명령입니다.

## 설치와 업그레이드

### 압축 파일로 설치

[Releases](https://github.com/JinHo-von-Choi/anti-samcheonpo/releases/latest)에서 자신의 OS·CPU에 맞는 파일을 받습니다.

| 환경 | 파일 이름 끝부분 |
| --- | --- |
| Linux x86-64 / ARM64 | `linux_amd64.tar.gz` / `linux_arm64.tar.gz` |
| Intel Mac / Apple Silicon | `darwin_amd64.tar.gz` / `darwin_arm64.tar.gz` |
| Windows x64 / ARM64 | `windows_amd64.tar.gz` / `windows_arm64.tar.gz` |

`SHA256SUMS`도 같은 폴더에 받으세요. Linux는 `sha256sum --ignore-missing -c SHA256SUMS`, macOS는 `shasum -a 256 -c SHA256SUMS --ignore-missing`으로 다운로드를 확인합니다. Windows PowerShell에서는 `Get-FileHash .\samcheonpo_0.9.5_windows_amd64.tar.gz -Algorithm SHA256`의 값을 `SHA256SUMS`의 해당 줄과 대조합니다.

압축을 풀고 `samcheonpo`와 `samcheonpo-hook`을 PATH에 등록한 **같은 폴더**에 둡니다. Windows에서는 두 `.exe`를 함께 두고, 검사 명령용 Git for Windows도 설치합니다.

```sh
samcheonpo --version
samcheonpo-hook --version
samcheonpo doctor
```

두 버전이 `0.9.5`인지 확인하세요. 첫 실행 전의 원장 없음·에이전트 연결 없음은 설치 실패를 뜻하지 않습니다.

### 소스에서 설치

Go 1.27 이상이 필요합니다. Go 실행 파일 설치 경로도 PATH에 있어야 합니다.

```sh
go install github.com/JinHo-von-Choi/anti-samcheonpo/cmd/samcheonpo@v0.9.5
go install github.com/JinHo-von-Choi/anti-samcheonpo/cmd/samcheonpo-hook@v0.9.5
```

### 기존 버전에서 업그레이드

1. 진행 중인 에이전트 작업을 끝내고 감시 프로세스를 종료합니다. Linux/macOS에서는 `ps`로 `samcheonpo daemon` 프로세스를 확인해 해당 PID에 `kill <PID>`를 보냅니다. Windows에서는 작업 관리자에서 해당 프로세스를 종료합니다.
2. `~/.samcheonpo/` 전체를 별도 위치에 백업합니다. 원장뿐 아니라 설정·키·복구 기록도 포함합니다.
3. 두 실행 파일을 함께 교체하고 위 버전 확인 명령을 실행합니다.
4. 연결했던 에이전트마다 플러그인을 갱신합니다. Codex 예시는 아래와 같습니다.

```sh
samcheonpo uninstall --agent codex
samcheonpo install --agent codex
samcheonpo doctor --agent codex
```

새 에이전트 세션에서 사용하세요. v0.9.5의 원장 스키마는 v0.9.2와 같은 **12**입니다. 스키마 12 원장은 v0.8.0 이하에서 열 수 없습니다. 사용자 수정 때문에 제거 충돌이 나오면 해당 파일을 보존하고 메시지를 확인하세요.

## 지난 작업 보기

실시간 연결 전에 기존 Claude Code·Codex 기록을 분석할 수 있습니다. 모델 호출은 없고, 결과를 로컬 원장에 저장합니다.

```sh
samcheonpo audit --agent codex --since 7d
samcheonpo sessions
samcheonpo receipt <세션ID>
```

`<세션ID>`는 `sessions`에 나온 값으로 바꿉니다. `audit --agent claude`는 Claude Code, `--agent all`은 두 종류의 기록을 분석합니다. 기록이 없으면 결과도 비어 있습니다.

## 에이전트 연결 확인

쓰는 에이전트의 이름을 지정합니다.

```sh
samcheonpo install --agent codex
samcheonpo doctor --agent codex
```

이름은 `claude`, `codex`, `opencode`, `agy`, `hermes`, `openclaw`, `copilot`를 지원합니다. `cursor`는 관찰 전용입니다. Hermes·OpenClaw의 플러그인 활성화와 게이트웨이 재시작은 설치 명령이 출력하는 안내를 따르세요.

### GitHub Copilot CLI에 연결하기

현재 소스로 빌드한 실행 파일에서는 다음 명령으로 연결합니다.

```sh
samcheonpo install --agent copilot
samcheonpo doctor --agent copilot
```

설치는 기본적으로 실행 중인 폴더의 `.github/hooks/samcheonpo.json`에 들어갑니다. Copilot은 신뢰된 작업 폴더에서만 저장소 훅을 불러오므로, 다른 폴더까지 쓰려면 사용자 수준 파일을 지정합니다.

```sh
samcheonpo install --agent copilot --hooks-file "$HOME/.copilot/hooks/samcheonpo.json"
```

같은 경로로 다시 실행하면 삼천포가 쓴 파일만 교체합니다. 이미 있는 파일은 덮어쓰지 않습니다. `samcheonpo install --agent copilot`을 경로 지정 없이 다시 실행하면 삼천포가 기록한 Copilot 연결 파일을 모두 지웁니다.

새 Copilot 세션에서 작업한 뒤 `doctor --agent copilot`의 마지막 수신 기록을 확인하세요. 필수 훅이 맞아도 수신 기록이 없다면 실제 연결은 아직 확인되지 않은 상태입니다.

Copilot은 도구 호출 ID를 보내지 않습니다. 시작과 결과는 도구 이름과 도착 순서로 이어지며, 셸 종료 코드는 결과 본문에만 있습니다. 사용량·비용은 Copilot 훅이 알려 주지 않아 비어 있습니다. 실행 전 차단과 도구 뒤 안내는 실제 CLI로 확인했지만 종료 후속 실행과 프롬프트 주입은 꺼 둡니다. [확인한 범위](support-matrix.md#github-copilot-cli)

### Cursor CLI에서 관찰하기

현재 소스로 빌드한 실행 파일에서는 다음 명령으로 연결합니다. 아직 배포된 v0.9.5의 Cursor 연결은 이 범용 훅 처리를 포함하지 않습니다.

```sh
samcheonpo install --agent cursor
samcheonpo doctor --agent cursor
```

설정은 기본적으로 `~/.cursor/hooks.json`에 들어갑니다. 다른 파일을 쓰려면 `--hooks-file /절대경로/hooks.json`을 지정합니다. 기존 연결을 갱신할 때도 같은 명령·같은 경로를 사용하세요. 삼천포가 기록한 기존 항목만 교체하며 다른 훅과 사용자 수정은 보존합니다.

새 Cursor 세션에서 작업한 뒤 `doctor --agent cursor`의 마지막 수신 기록을 확인하세요. 필수 훅이 맞아도 수신 기록이 없다면 실제 연결은 아직 확인되지 않은 상태입니다. 사용량이 수집되지 않은 비용은 미확인으로 읽습니다. 현재는 실행을 차단하거나 안내를 전달하지 않습니다. [확인한 범위](support-matrix.md#cursor-cli)

연결을 제거하려면 `samcheonpo uninstall --agent cursor`를 실행합니다.

에이전트에서 새 세션을 시작한 뒤 프로젝트의 사용자 터미널에서 확인합니다.

```sh
samcheonpo cmd status
samcheonpo cmd summary
```

연결 성공은 감시가 동작한다는 뜻입니다. 작업 완료나 비용 절감을 뜻하지 않습니다.

## 경고가 나오면

경고의 근거를 읽고 다음 중 하나를 선택하세요.

| 판단 | 사용자 터미널에서 실행 |
| --- | --- |
| 제안한 다음 행동을 적용하고 싶음 | `samcheonpo cmd steer` |
| 판정이 틀렸음 | `samcheonpo cmd keep normal` |
| 필요성을 알고 이번만 진행함 | `samcheonpo cmd keep now` |
| 지금 무엇이 끝났는지 확인하고 싶음 | `samcheonpo cmd summary` |

Claude Code에서는 `samcheonpo cmd summary` 대신 `/samcheonpo:summary`처럼 사용할 수 있습니다. Codex에는 이 슬래시 명령을 쓰지 않습니다.

`keep normal`의 해제는 현재 목표의 같은 규칙·대상에 적용됩니다. 보호 경로·예산은 해제하지 않습니다. 경고 없이 기록만 하려면 사용자 설정 `~/.samcheonpo/config.yml`의 `rollout.mode`를 `shadow`로 바꾸고 새 세션을 시작하세요. 기존에 수락한 보호 경로·예산은 계속 적용됩니다.

## 결과와 비용 확인

| 명령 | 용도 |
| --- | --- |
| `samcheonpo receipt <세션ID>` | 해당 세션의 사용량·판정 요약 |
| `samcheonpo why <세션ID>` | 판단한 근거와 판단하지 못한 이유 |
| `samcheonpo coverage <세션ID>` | 기록과 비용의 확인 범위 |
| `samcheonpo interventions --since 7d --by-agent` | 안내 전달·차단 기록 |
| `samcheonpo dashboard` | 로컬 웹 화면. 출력된 주소와 접근 코드로 로그인 |

0%는 관측된 비용 중 낭비로 분류한 몫이 없다는 뜻입니다. 정확한 작업·완료·절약을 보장하지 않습니다. 원화는 API 등가 추정이며 미확인 부분을 0원으로 읽으면 안 됩니다.

## 문제가 생기면

| 증상 | 먼저 할 일 |
| --- | --- |
| 명령을 찾지 못함 | 두 실행 파일의 위치와 PATH 확인 |
| 연결했는데 경고·상태가 없음 | 새 세션 시작, 훅 신뢰 확인 후 `doctor --agent codex` 실행 |
| 데몬 연결 실패 | 에이전트에서 첫 작업 실행 후 다시 확인. 계속 실패하면 `doctor` 결과 확인 |
| 버전 또는 훅 형식 경고 | 두 실행 파일을 함께 갱신하고 해당 에이전트 연결 재설치 |
| 완료 근거 미확인 | 계약이 없거나 미수락 상태인지 확인. 설치 오류와 구분 |
| 원장 오류 | 원장을 삭제하지 말고 백업 후 오류 메시지 확인 |
| 판정 때문에 필요한 작업이 막힘 | 근거 확인 후 `keep normal` 또는 `keep now`. 예산·보호 경로는 해당 설정 확인 |

## 연결 해제

```sh
samcheonpo uninstall --agent codex
```

설치한 에이전트마다 실행하고 새 세션을 시작합니다. 삼천포가 설치한 연결을 제거하며, 원장·영수증·실행 파일은 남깁니다. 사용자가 수정한 파일은 보존하고 충돌을 알립니다.

## 선택 기능

완료 조건을 직접 검사하게 하거나 예산·보호 경로를 지정하려면 [작업 계약](reference.md#작업-계약)을 사용하세요. 파일 복구는 [되돌리기](reference.md#되돌리기), 안내 규칙과 시간 상한은 [설정·동작 상세](reference.md)에 있습니다. 처음 사용할 때 모두 설정할 필요는 없습니다.

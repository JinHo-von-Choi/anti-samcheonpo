# 지원 범위

에이전트에 연결하면 도구 호출과 실행 결과를 기록합니다. 안내를 전달하는 시점과 종료 검사 방식은 에이전트마다 다릅니다.

## 에이전트

| 에이전트 | 연결 | 주요 기능 |
| --- | --- | --- |
| Claude Code 2.1.x | 플러그인 | 실행 전 차단, 도구 결과 안내, 종료 검사, 상태줄·슬래시 명령, 하위 에이전트 보호 |
| Codex 0.160–0.166 | `~/.codex/hooks.json` | 실행 전 차단, 안내, 종료 검사, 하위 에이전트 도구 추적 |
| Antigravity CLI(agy) 1.3.x–1.4.12 | `~/.gemini/config/hooks.json` | 실행 전 차단, 다음 모델 호출에 안내, 종료 검사, 백그라운드 결과 수집 |
| opencode 1.18.x | 플러그인 | 도구 결과 안내, 종료 시 후속 요청 |
| Hermes 0.21.x | 플러그인 | 실행 전 차단, 도구 결과 안내, 코드 수정 후 종료 검사 |
| OpenClaw 2026.7.x | 플러그인 | 실행 전 차단, 다음 요청에 안내, 종료 시 후속 요청 |
| MiniMax Code 0.6.13 | `~/.minimax/hooks.json` | 실행 전 차단, 도구 결과 안내, 종료 검사, 백그라운드 결과 수집 |
| GitHub Copilot CLI 1.0.101–1.0.104 | `.github/hooks/samcheonpo.json` | 실행 전 차단, 도구 결과 안내, 종료 검사, 요청 시 안내 |
| Cursor CLI 2026.10.19-6bbf2c4 | `~/.cursor/hooks.json` | 도구 호출·결과 기록 |
| Deep Agents 0.7.32 | Python 미들웨어 | 실행 전 차단, 도구 결과 안내, 종료 보류 후 재개, 하위 에이전트 보호, 토큰 계측 |

[에이전트 연결 방법](getting-started.md#에이전트-연결-확인) · [Deep Agents 연결 방법](../plugins/deepagents/README.md)

연결을 점검하려면 `samcheonpo doctor --agent codex`처럼 에이전트 이름을 지정합니다. 에이전트 업데이트 뒤 훅 형식이 바뀌면 `hook_shape`에 안내가 나옵니다. 이때 삼천포와 연결 설정을 갱신하세요.

### Claude Code와 Codex

Claude Code는 상태줄과 `/samcheonpo:summary` 등의 슬래시 명령을 제공합니다. Codex에서는 프로젝트 터미널의 `samcheonpo cmd summary`를 사용합니다.

두 에이전트 모두 하위 에이전트의 도구 호출을 별도로 기록하고 공유 예산에 합산합니다. Claude Code는 자식의 종료 검사를 지원합니다. Codex는 자식 도구를 실행 전에 검사하며, 종료 검사는 부모 세션에서 처리합니다.

### Antigravity CLI

연결할 때는 `--agent agy`를 지정합니다. 도구 결과는 대화 기록에서 수집하며, 백그라운드 명령은 최종 결과가 도착할 때 반영합니다. 중단한 대화는 같은 세션으로 재개할 수 있습니다.

### Cursor CLI

Cursor CLI는 관찰용 연결입니다. 도구 호출과 결과를 기록하고 `doctor --agent cursor`로 마지막 수신을 확인할 수 있습니다.

기본 훅 파일은 `~/.cursor/hooks.json`입니다. 다른 경로를 쓰면 설치·갱신 시 같은 `--hooks-file` 경로를 지정하세요.

### GitHub Copilot CLI

기본 연결은 현재 프로젝트의 `.github/hooks/samcheonpo.json`에 저장됩니다. 여러 프로젝트에서 쓰려면 사용자 훅 파일을 지정합니다.

```sh
samcheonpo install --agent copilot --hooks-file "$HOME/.copilot/hooks/samcheonpo.json"
```

Copilot에서 신뢰한 작업 폴더의 새 세션을 시작하세요. 종료 검사가 필요한 경우 보류 사유가 다음 요청으로 전달되어 작업을 이어갑니다. 사용량은 Copilot이 제공하는 기록 범위에 따라 표시합니다.

### MiniMax Code

mcode는 훅 파일의 `enabled`가 `true`일 때 연결을 불러옵니다. 설치 후 새 세션을 시작하세요. 사용자가 꺼 둔 훅 설정은 그대로 유지됩니다.

백그라운드 셸은 `task_output`으로 도착하는 최종 결과를 반영합니다. 지난 기록은 `samcheonpo audit --agent mcode`로 분석합니다.

### opencode 1.18.x

도구 결과에 안내를 붙이고, 종료 시 필요한 작업을 후속 요청으로 보냅니다. 이 요청에 응답하려면 서버나 TUI 세션을 유지하세요.

### Hermes와 OpenClaw

설치 명령이 안내하는 플러그인 활성화 절차를 마친 뒤 실행 중인 게이트웨이를 재시작합니다. Hermes는 도구 결과에, OpenClaw는 다음 요청에 안내를 전달합니다.

## 운영체제

| 운영체제 | 배포 아키텍처 | 설치 시 참고 |
| --- | --- | --- |
| Linux | amd64, arm64 | 두 실행 파일을 같은 PATH 폴더에 배치 |
| macOS | amd64, arm64 | 다운로드 체크섬 확인 후 설치 |
| Windows | amd64, arm64 | Windows 10 1803 이상, 검사 명령용 Git for Windows |
| WSL | Linux 파일 사용 | WSL 안에 설치하고 에이전트도 같은 환경에서 실행 |

Windows의 검사 명령은 Git for Windows의 `bash`로 실행합니다. `samcheonpo doctor`의 `shell` 항목에서 실행 셸을 확인하세요. 직접 지정하려면 `SAMCHEONPO_SHELL`에 `bash.exe` 경로를 넣습니다.

## 기능별 사용 안내

- [경고와 차단 설정](reference.md#귀띔에서-차단까지)
- [완료 조건과 결과물 검사](agent-failure-controls.md)
- [예산과 장시간 작업](reference.md#장시간-세션)
- [파일 되돌리기](reference.md#되돌리기)
- [개발·평가 도구](development.md)

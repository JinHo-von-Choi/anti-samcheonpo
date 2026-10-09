# 지원·검증 범위

아래 상태는 자동 배포 약속이 아니라 실제 검증 기록을 구분하기 위한 표다.

| 환경 | 현재 확인한 범위 | 미확인/제약 |
| --- | --- | --- |
| Linux amd64 | 실제 훅 수신·지연, 작업 인수인계, 2026-10-06 네이티브 후보의 키 없는 설치·제거/감사/첫 훅 및 체크섬 통과. 2026-10-07 격리 홈에서 실제 실행 파일로 설치·제거 시 사용자 설정 보존, 안내 후 실행 전 차단, 편집 뒤 허용, 상태줄, 되돌리기, 영수증 확인. 2026-10-07 0.2.1 후보 실행 파일로 위 항목과 회복 확인(서비스 원인 실패 뒤 재실행 1회 허용), 사용자 전용 명령의 에이전트 셸 거부, 계획 ID가 있어야 되돌리기 적용, 바꾸기 전 백업까지 확인 | 2026-10-07 측정(load average 약 69)에서 훅 p95 13.7–18.1ms로 10ms 목표 미달. 같은 조건의 이전 버전도 12.8–16.3ms. 같은 날 부하가 낮을 때 0.2.1 후보 p95 9.1–9.5ms |
| Linux arm64 | 네이티브 릴리스 후보 워크플로 정의 | 이 작업 환경에서 실제 실행하지 않음 |
| macOS (GitHub Actions macos-15) | 2026-10-07 CI에서 `go vet`, `go test ./...`(데몬·훅·체크포인트·되돌리기 흐름 포함), 적합성 17건, 훅 처리 구간 지연 검사 통과. 피어 자격은 LOCAL_PEERCRED로 확인 | 실제 에이전트 연결과 설치·제거 실행 없음. 릴리스 파일 없음(소스 설치) |
| Windows 11 arm64 (Parallels VM, 2026-10-09) | 교차 컴파일한 시험 바이너리 전체를 VM에서 실행해 모든 패키지 통과(데몬·훅·체크포인트·되돌리기·설치·제거·피어 확인 포함, SYSTEM 계정). VM 안에서 네이티브로 적합성 17건, 훅 지연 200회 응답 200/200(프로세스 기동 포함 p95 약 12ms, 처리 구간 p95 약 2ms), 릴리스 후보 `package-release.sh`의 키 없는 설치·제거·감사·첫 훅·`cmd card` 확인 통과. 같은 사용자 확인은 `SIO_AF_UNIX_GETPEERPID`로 얻은 PID의 토큰 SID 비교이며 다른 로컬 계정의 연결이 거부됨을 확인. 런타임 디렉터리는 현재 사용자와 SYSTEM만 허용하는 보호된 DACL. 명령 실행은 Git for Windows의 bash(없으면 `cmd.exe /S /C`), 프로세스 수명은 Job Object | Claude Code 2.1.295(Windows arm64)의 실제 세션에서 플러그인 훅이 Git Bash로 실행되고 데몬이 자동으로 뜨며 같은 실패 명령 반복이 탐지·귀띔되고 4번째 실행이 실행 전 거부됨(차단 사유가 에이전트에 한국어 그대로 전달)을 확인(2026-10-09, 격리 설정 폴더와 `--plugin-dir`, 모델 호출 2세션). 그 밖의 에이전트(Codex·opencode·agy·Hermes·OpenClaw)는 Windows에서 미확인. Windows x64 미실행. 로그인 사용자 계정 전체 실행도 통과(심볼릭 링크 권한이 필요한 시험은 건너뜀). 설치된 `Bash(...)` 허용 규칙이 에이전트가 만드는 명령 문자열과 맞는지 미확인(Unix 동일). Git for Windows가 없으면 POSIX 문법 검사 명령이 같은 뜻으로 동작하지 않음 |

2026-10-07에 더한 기능 가운데 두 가지는 같은 날 실제 Claude Code 2.1.292(`claude -p --model sonnet`, 격리 홈, 플러그인 디렉터리 직접 지정)로 확인했다.

| 기능 | 실제 실행으로 확인한 것 | 확인하지 못한 것 |
| --- | --- | --- |
| 코드 밖 원인 수정 보류 | 설치되지 않은 `yaml` 모듈로 `node test_app.cjs`가 두 번 같은 실패를 낸 뒤 `s2.environment` L2가 외부 조치 `npm install yaml`과 함께 기록됐고, 이어진 `src/app.cjs` 쓰기에 보류 사유가 에이전트에게 전달됐다. 에이전트는 사유를 그대로 보고하고 멈췄다 | 전달 뒤 두 번째 쓰기의 실행 전 거부와 `keep normal` 해제는 하네스 시험으로만 확인. 첫 시도에서는 에이전트가 두 명령을 `;`로 묶거나 `\| head`로 종료 코드를 가려 반복이 성립하지 않았다. 종료 코드가 가려진 실패는 판정하지 않는다 |
| 통과 시점 기록과 `rollback golden` | `python3 -m unittest` 통과 시점에 `stats.py`가 `.samcheonpo/snapshots`에 기록됐고(git 트리 지문 포함), 이름을 바꿔 실패하게 만든 뒤 에이전트가 실행한 `samcheonpo cmd rollback golden`이 그 시점을 나열하고 `golden <id>` 미리보기가 `stats.py: 이전 내용으로 복구`와 계획 ID를 냈다 | `apply`는 사용자 전용이라 에이전트가 실행하지 않았다. 세션이 끝난 뒤에는 `rollback golden`이 "보고 있는 세션이 없다"로 끝나므로 되돌리기는 세션 안에서 해야 한다 |

군집 예산, 압축 뒤 앵커, HUD는 하네스 흐름 시험과 적합성 사례로만 확인했다.

2026-10-09에 더한 장시간 세션 규칙은 다음 범위에서 확인했다.

| 기능 | 확인한 것 | 확인하지 못한 것 |
| --- | --- | --- |
| Codex 실제 기록 재생 | 42시간 40분짜리 Codex 0.160 세션 기록(셸 명령 5,101건)을 감사 모드로 재생했다. 변경 전 엔진은 검증 명령을 거의 인식하지 못했다(라이브 원장에서 셸 약 4,800건 중 검증 5건). 경로가 붙은 인터프리터(`.venv/bin/python -m pytest`), heredoc 안의 `subprocess` 시험 실행, 실행용 스크립트(`/tmp/…-gates-run.py`)를 인식하게 한 뒤 검증 483건, 원격 CI 실패 뒤 로컬 재현 없는 재배포 8건(v0.5.1~v0.5.8), 같은 도구의 반복 거부 8건, 세션 길이 알림·확인이 판정됐다 | 이 판정이 실제 세션에서 에이전트 행동을 바꾸는지는 측정하지 않았다 |
| Codex 라이브 훅 | 하네스 시험에서 Codex 훅 형식으로 CI 실패 뒤 첫 재배포에 귀띔, 태그를 바꾼 두 번째 재배포를 실행 전에 거부 | 실제 Codex 프로세스로는 미확인 |
| Codex 0.162.0 훅 | 격리한 `CODEX_HOME`에서 실제 Codex 0.162.0(`codex exec`)을 실행해 받은 SessionStart·UserPromptSubmit·PreToolUse(Bash, apply_patch)·PostToolUse·Stop·SessionEnd 입력이 0.160 기록과 필드·도구 이름·호출 ID 형식까지 같음을 확인해 지원 범위를 0.162.x로 넓혔다. 데몬이 에이전트 버전 확인에 한 번 실패하면 재시작 전까지 그 에이전트를 관찰 전용으로 두던 문제를 고쳐, 30초 뒤 다음 훅에서 다시 확인한다 | 0.162에서 실제 세션의 귀띔·차단 전달은 이번에 다시 확인하지 않았다 |
| 오탐 방지 | 기존 오탐 방지 시나리오 전부 통과(작은 프로젝트의 빠른 전체 시험 반복, `timeout` 상한을 늘린 재실행은 판정하지 않음) | 다른 실제 장시간 세션 기록으로는 미확인 |
| macOS amd64/arm64 | 네이티브 릴리스 후보 워크플로 정의 | 실제 설치·제거·훅 실행 결과 대기 |
| WSL | Linux 실행 파일 사용을 검증할 대상 | 실제 WSL 환경 smoke 대기; 일반 Linux 결과로 대체하지 않음 |
| Windows (GitHub Actions windows-latest·windows-11-arm) | CI 정의에 전체 시험·적합성·훅 지연 검사와 릴리스 후보 빌드를 추가함 | 원격 실행 결과 대기 |

실제 A/B 실행의 감시 프로세스 종료 확인은 현재 Linux의 프로세스 소유 홈 확인 경로를 사용한다. 다른 OS에서 판정 데몬을 사용한 시행은 종료/정산 완료를 확정하지 못하면 감시 비용을 미확인으로 남긴다. 단순 소켓 소멸을 최종 원장 정산의 증거로 취급하지 않는다.

## 에이전트별 실제 실행 확인

2026-10-07 격리 홈(사용자 설정을 건드리지 않음)에서 실제 에이전트로 같은 실패 명령을 반복하게 해 확인했다.

| 에이전트 | 연결 방식 | 실제 실행으로 확인한 것 | 제약 |
| --- | --- | --- | --- |
| Antigravity(agy) 1.3.1 | `~/.gemini/config/hooks.json`의 `samcheonpo` 훅 묶음 | 첫 모델 호출에서 요청 기록, 대화 기록의 종료 코드·토큰 반영, 다음 모델 호출에서 `injectSteps`로 귀띔(대화 기록에 표시됨), 무시한 반복을 `deny`로 거부, 두 번째 요청 인식, 제거 시 파일 복원 | 셸 결과는 훅이 끝난 뒤 대화 기록에 쓰여 다음 이벤트에서 반영. 세션 종료 이벤트 없음 |
| Hermes 0.21.5 | `~/.hermes/plugins/samcheonpo` + `hermes plugins enable` | 종료 코드·토큰 반영, `transform_tool_result`로 도구 결과에 귀띔(모델에 보내는 대화 메시지에 포함됨), 무시한 반복 거부, 세션 종료 시 영수증, 제거 시 `plugins.enabled` 정리 | 종료 보류(`pre_verify`)는 코드를 고친 차례에서만 불림 |
| OpenClaw 2026.7.1 | 플러그인 + `openclaw plugins install` | 종료 코드 반영, 다음 요청의 `before_prompt_build`로 귀띔(모델이 그대로 인용), 거부 사유 전달, 제거 시 확장·설정 정리 | 실행 중 도구 결과를 모델에 보이게 바꾸는 훅 없음(`tool_result_persist`는 저장 기록만 바꿈). 같은 실행 안의 반복은 막지 않음. 시험에 쓴 모델(Ollama Cloud)은 사용량을 보내지 않아 토큰 반영은 미확인 |

에이전트별 캡처 fixture와 시험 버전 범위는 `internal/adapter/caps.go`가 기준이다. 범위 안이라는 사실은 모든 버전의 실제 제품 동작이나 개입 효과를 인증하지 않는다. `doctor --agent <이름>`은 현재 바이너리의 버전 문자열과 설정을 확인하며, 에이전트에 모델 작업을 요청하지 않는다.

## 릴리스 후보 만들기

```sh
sh scripts/package-release.sh 0.5.0-dev /tmp/samcheonpo-release-new
```

기존 출력 디렉터리를 덮어쓰지 않는다. 네이티브 플랫폼에서 두 실행 파일을 만들고, API 키 없는 분리 환경의 설치/제거·감사·기본 훅 검사를 실행한 뒤 tar.gz와 SHA256SUMS를 만든다. 교차 컴파일만으로 플랫폼 검증을 주장하지 않도록 호스트와 타깃이 다르면 거절한다. BUILD.txt에 버전·커밋·도구 체인·추적 파일 변경 여부를 기록한다. 산출물은 후보이며 스크립트는 게시하지 않는다.

`.github/workflows/release.yml`은 수동 `workflow_dispatch`만 제공한다. 원격 실행 전에 `release-candidate` 환경의 필수 검토자를 설정해야 한다. 워크플로는 후보 artifact만 올리고 태그·GitHub Release·배포를 만들지 않는다. 코드 라이선스는 MIT(`LICENSE`)다. 배포 정책은 실제 공개 릴리스 전에 별도로 확정해야 한다. 현재 로컬 실행을 원격 CI 성공으로 표시하지 않는다.

구성은 GitHub의 [수동 실행 문서](https://docs.github.com/en/actions/how-tos/manage-workflow-runs/manually-run-a-workflow), [Go 설정 액션](https://github.com/actions/setup-go), [artifact 액션](https://github.com/actions/upload-artifact), [호스티드 러너 표](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)를 확인해 작성했다. 액션은 확인한 v7 커밋으로 고정했으며, 버전 변경 시 다시 검증해야 한다.

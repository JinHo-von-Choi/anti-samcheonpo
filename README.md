<div align="center">
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/logo-dark.png">
  <img src="docs/assets/logo.png" alt="삼천포 (anti-samcheonpo)" width="260">
</picture>

**AI 코딩 에이전트의 반복 작업과 실패를 기록하고, 필요하면 경고하거나 실행을 막는 로컬 도구**

[CI](https://github.com/JinHo-von-Choi/anti-samcheonpo/actions/workflows/ci.yml) · [다운로드](https://github.com/JinHo-von-Choi/anti-samcheonpo/releases/latest) · [English](README.en.md)
</div>

## 무엇에 쓰나요?

Claude Code나 Codex에 연결해 **같은 검사 반복, 같은 오류 재시도, 요청 범위를 벗어난 수정** 같은 행동을 살펴봅니다. 명령·파일 변경·실행 결과를 근거로 에이전트에게 다음 행동을 안내하고, 일부 반복 행동은 실행 전에 막습니다. 지난 세션의 사용량과 판정 근거도 조회할 수 있습니다.

삼천포는 AI 대신 코드를 작성하지 않습니다. **비용 절감이나 완료율 개선 효과는 아직 입증되지 않았습니다.** 현재는 작업을 관찰하고 사용자가 개입할 근거를 얻는 용도로 사용하세요. [Codex 16회 파일럿 결과](docs/benchmarks/2026-10-21-codex-pilot.md)

## 설치

[릴리즈](https://github.com/JinHo-von-Choi/anti-samcheonpo/releases/latest)에서 운영체제·CPU에 맞는 압축 파일과 `SHA256SUMS`를 받으세요. Linux·macOS·Windows의 amd64/arm64 파일을 제공합니다.

Linux x86-64 예시:

```sh
sha256sum --ignore-missing -c SHA256SUMS
tar -xzf samcheonpo_0.9.13_linux_amd64.tar.gz
mkdir -p ~/.local/bin
cp samcheonpo samcheonpo-hook ~/.local/bin/
export PATH="$HOME/.local/bin:$PATH"
samcheonpo --version
```

두 실행 파일은 **같은 폴더**에 둡니다. PATH는 사용 중인 셸 설정에도 추가하세요. [macOS·Windows·소스 설치와 업그레이드](docs/getting-started.md#설치와-업그레이드)

## 바로 사용하기

**1. 지난 기록을 분석합니다.** 에이전트 연결 없이 실행할 수 있습니다. 모델을 호출하지 않으며 분석 결과는 로컬 원장에 저장합니다.

```sh
samcheonpo audit --agent codex --since 7d
samcheonpo sessions
```

**2. 쓰는 에이전트에 연결합니다.** 아래 둘 중 필요한 명령만 실행하세요.

```sh
samcheonpo install --agent codex
# Claude Code를 쓴다면: samcheonpo install --agent claude
samcheonpo doctor --agent codex
```

에이전트에서 **새 세션을 시작**하세요. 훅·플러그인 신뢰 확인이 나오면 설치한 삼천포 항목을 확인해 승인합니다. 첫 훅이 호출되면 감시 프로세스가 자동으로 시작됩니다.

Cursor CLI는 관찰 전용입니다. 현재 소스의 연결·갱신 방법과 확인 범위는 [Cursor 사용 안내](docs/getting-started.md#cursor-cli에서-관찰하기)를 참고하세요.

MiniMax Code(mcode)는 `samcheonpo install --agent mcode`로 연결합니다. 실행 전 차단, 도구 뒤 안내, 종료 보류를 실제 CLI로 확인했습니다. [mcode 사용 안내](docs/getting-started.md#minimax-codemcode에-연결하기)

GitHub Copilot CLI는 `samcheonpo install --agent copilot`로 연결합니다. 실행 전 차단, 도구 뒤 안내, 종료 보류, 프롬프트 안내를 실제 CLI로 확인했습니다. 연결 방법과 확인 범위는 [Copilot 사용 안내](docs/getting-started.md#github-copilot-cli에-연결하기)를 참고하세요.

**3. 평소처럼 작업하고, 경고가 나오면 판단합니다.** 아래 명령은 작업 중인 프로젝트의 사용자 터미널에서 실행합니다.

| 상황 | 명령 |
| --- | --- |
| 현재 상태를 보고 싶음 | `samcheonpo cmd summary` |
| 판정이 틀렸음 | `samcheonpo cmd keep normal` |
| 이번 한 번은 진행해야 함 | `samcheonpo cmd keep now` |
| 안내한 다음 행동을 에이전트에게 전달 | `samcheonpo cmd steer` |

Claude Code에서는 `/samcheonpo:summary`, `/samcheonpo:keep normal` 같은 슬래시 명령도 사용할 수 있습니다. Codex는 위 터미널 명령을 사용합니다.

## 언제 작업을 막나요?

기본 설정은 **먼저 경고하고, 일부 규칙에서 같은 목표·대상의 반복이 확인되면 차단**합니다. 반복 경고에 의한 차단은 세션당 최대 3회입니다. 기록만 하는 규칙도 있습니다. 사용자가 수락한 보호 경로·예산은 별도 조건으로 집행합니다.

처음에는 경고 없이 기록만 보고 싶다면 `~/.samcheonpo/config.yml`의 기존 설정에 다음을 합친 뒤 새 세션을 시작하세요.

```yaml
rollout:
  mode: shadow
```

`shadow`에서도 이미 수락한 보호 경로·예산은 유지됩니다. **연결을 완전히 해제**하려면 다음을 실행하고 에이전트를 새로 시작하세요. 기록과 사용자가 수정한 설정은 지우지 않습니다.

```sh
samcheonpo uninstall --agent codex
```

## 결과를 읽을 때

- **경고 없음 ≠ 작업 완료.** 완료 여부는 실제 결과물과 검사를 확인해야 합니다.
- **원화 표시는 토큰의 API 등가 비용**입니다. 실제 청구액이나 절감액이 아닙니다. 누락된 사용량·단가는 미확인으로 표시합니다.
- **오탐이 가능합니다.** 잘못된 판정은 `keep normal`로 해제합니다. 이 명령은 보호 경로·예산까지 해제하지 않습니다.
- **기록은 기본적으로 로컬에 저장**합니다. 외부 판정 모델·전송은 선택 사항입니다. 원장과 인수인계 자료에는 경로·작업 내용이 남을 수 있습니다.

## 필요한 문서만 읽기

- [사용 가이드](docs/getting-started.md): 설치, 연결 확인, 경고 대응, 업그레이드, 문제 해결
- [설정·동작 상세](docs/reference.md): 완료 조건, 차단 조건, 예산, 되돌리기, 개인정보
- [지원 범위](docs/support-matrix.md): 에이전트·운영체제별 확인 수준
- [개발·평가 안내](docs/development.md): 빌드, 검사, 실험 도구, 릴리즈 절차
- [변경 이력](CHANGELOG.md)

[MIT License](LICENSE)

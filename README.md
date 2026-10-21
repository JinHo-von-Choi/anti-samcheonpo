<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/logo-dark.png">
  <img src="docs/assets/logo.png" alt="삼천포 (anti-samcheonpo)" width="300">
</picture>

**AI 코딩 에이전트가 헛짓에 시간과 돈을 태우는 것을 알아채고, 알려 주고, 멈추게 하는 하네스**

[![CI](https://github.com/JinHo-von-Choi/anti-samcheonpo/actions/workflows/ci.yml/badge.svg)](https://github.com/JinHo-von-Choi/anti-samcheonpo/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go 1.27+](https://img.shields.io/badge/go-1.27+-00ADD8.svg)](https://go.dev/dl/)

한국어 | [English](README.en.md)

</div>

---

## 왜 필요한가

개발자는 AI가 같은 시험을 또 돌리거나 시키지 않은 파일을 고치기 시작하면 바로 알아채고 끼어듭니다. 비개발자는 그 순간을 알아볼 기준이 없습니다. 그사이 AI는 "거의 다 됐습니다"라고 말하고, 시간과 사용 한도는 계속 줄어듭니다.

헛짓은 대개 세 가지 모양입니다.

- **필요 이상의 검증.** 코드는 그대로인데 같은 시험을 몇 번이고 다시 돌립니다.
- **엉뚱한 방향.** 로그인 오류를 고쳐 달라고 했는데 로그인 방식 전체를 갈아엎습니다.
- **같은 자리에서 맴돌기.** 같은 실패를 되풀이하거나, 설치되지 않은 패키지나 꺼진 서버처럼 코드로 풀 수 없는 문제를 코드로 풀려고 합니다.

삼천포는 이런 헛짓을 실행 결과로 잡아내는 로컬 도구입니다. 이야기가 엉뚱한 데로 새는 것을 "삼천포로 빠진다"고 하는 데서 이름을 따왔습니다.

## 동작 방식

삼천포는 에이전트의 훅에 붙어 명령, 파일 수정, 시험 결과, 토큰 사용량을 봅니다. AI가 "통과했다"고 말해도 실제 실행 결과로만 판단합니다. 판단은 내 컴퓨터 안에서 하고, 모델을 호출하지 않습니다.

```mermaid
flowchart LR
    U[사용자] -->|요청| A[AI 에이전트]
    A -->|명령 · 파일 수정<br/>시험 결과 · 토큰| H[삼천포 데몬<br/>내 컴퓨터]
    H --> J{헛짓인가}
    J -->|처음| N[귀띔<br/>근거와 다음 행동]
    J -->|경고 뒤 반복| B[실행 전 차단]
    J -->|예산 초과 · 코드 밖 원인| S[정지 · 수정 보류]
    H -->|경고 · 상태줄 · 영수증| U
    U -->|keep · steer · rollback| H
```

처음에는 에이전트에게 근거와 함께 귀띔만 합니다. 같은 세션에서 경고를 받고도 같은 대상에 같은 헛짓을 반복하면 다음 실행을 **실행 전에** 막고 이유를 돌려줍니다. 차단은 세션당 3회까지이고, 잘못 막았으면 `/samcheonpo:keep normal`로 풉니다.

사용자에게는 쉬운 말로 알립니다.

```text
AI가 같은 시험을 4번 돌렸는데, 그 사이 코드는 한 글자도 바뀌지 않았습니다.
AI에게 이렇게 말해 보세요: "같은 시험을 다시 돌리지 말고, 직전 실패 원인을 먼저 찾아 줘."
[/samcheonpo:keep now 이번만 허용]  [/samcheonpo:keep normal 이 판정은 틀림]  [/samcheonpo:summary 멈추고 요약 받기]
```

상태줄 맨 앞에는 지금 상태가 보입니다.

```text
[지켜보는 중] 진척 1/2 · 공회전 API환산 1,240원 · 헛짓 12% (계측분)
```

| 상태 | 뜻 |
| --- | --- |
| 순조로움 | 검사 통과 같은 진척 근거가 있고, 그 뒤 경고가 없습니다 |
| 지켜보는 중 | 아직 진척 근거가 없거나, 가벼운 안내가 있었습니다 |
| 지금 끼어드세요 | 반복된 헛짓이나 차단이 있습니다 |
| 확인 불가 | 관측이 빠져 판단할 수 없습니다 |

> 원화는 **계측한 토큰을 API 단가로 환산한 금액**입니다. 실제 청구액이나 절감액이 아닙니다. 사용량을 모르면 0원이 아니라 "미확인"으로 표시합니다.

## 무엇을 잡나

| 헛짓 | 예 | 개입 |
| --- | --- | --- |
| 과잉 검증 | 코드 변경 없이 같은 시험 반복, 몇 줄 고치고 5분 넘게 걸리는 전체 시험 재실행, 짧은 확인 없이 긴 부하 시험 시작 | 귀띔 → 반복 시 차단 |
| 맥락 이탈 | 요청 범위 밖 파일 수정, 보호 경로 수정, 맥락 압축 뒤 목표 잊기 | 귀띔, 보호 경로는 즉시 차단, 압축 직후 목표 재안내 |
| 같은 실패 반복 | 고쳐도 같은 오류, 이미 실패한 수정 재시도, 고쳤다 되돌리기, 브라우저 시험 타이밍 땜질, 같은 이유로 계속 거부당하는 제출 | 귀띔 → 반복 시 차단 → 인수인계 |
| 코드 밖 문제 | 설치되지 않은 의존성, 꺼진 서비스, 권한, 포트 점유 | 소스 수정 보류, 사람이 할 일 안내 |
| 허위 완료 | 시험 삭제·skip·단언 약화, 오류 숨기기, 완료 조건이 실패한 채 "완료" | 귀띔 → 반복 시 차단 |
| 배포 연타 | CI 실패를 로컬에서 재현하지 않고 재배포, 실패한 CI를 그대로 다시 돌리기, 시간당 배포 횟수 초과 | 귀띔 → 태그가 바뀌어도 반복 시 차단 |
| 완료 뒤 대기·진행 정체 | 완료 검사를 통과한 뒤 계속 기다림, 수정해도 같은 검사에서 실패가 줄지 않음 | 기본은 기록만, 켜면 가벼운 안내 |
| 비용 폭주 | 진척 없는 지출, 예산 초과, 세션 시간·토큰 과다 | 알림 → 확인 → 상한에서 정지 |
| 지시 밀수 | 프롬프트·파일·도구 출력에 섞여 들어온 "감시를 피하라", "검증하지 않은 것을 통과했다고 하라" 같은 지시 | 지시의 형태와 출처를 기록, 저장소에 심는 쓰기는 실행 전 차단 |
| 채점기 접촉 | 채점기·정답·루브릭 경로에 닿는 도구 호출 | 실행 전 차단 |

지시 밀수 규칙은 사용자 프롬프트, 파일 내용, 도구 출력, 기억 파일을 합친 전체 컨텍스트를 봅니다. 원문은 저장하지 않고, 지시가 있었다는 사실과 들어온 경로만 남깁니다. 대기·정체 안내를 켜는 방법은 [시작 가이드](docs/getting-started.md#대기-반복진행-정체-안내-켜기)에 있습니다.

### 코드 밖 문제

코드를 아무리 고쳐도 풀리지 않는 실패가 있습니다. 같은 명령이 작업 폴더 변화 없이 같은 외부 원인으로 두 번 실패하면, 삼천포는 그 원인이 풀릴 때까지 소스 수정을 막고 사람이 실행할 명령을 알려 줍니다.

```mermaid
flowchart LR
    F[명령 실패] --> C{원인}
    C -->|코드| K[평소대로 귀띔]
    C -->|일시 장애| T[재시도 허용]
    C -->|의존성 · 서비스<br/>권한 · 포트 · 디스크| X{두 번째인가}
    X -->|아니오| K
    X -->|예| Z[소스 수정 보류<br/>사람이 할 일: npm install yaml]
    Z -->|명령 통과 · 설치 성공<br/>keep normal| K
```

보류 중에도 의존성 선언 파일(`package.json`, `requirements.txt`, `go.mod` 등)은 고칠 수 있습니다. 시간 초과와 강제 종료는 코드가 원인일 수 있어 보류하지 않습니다.

### 판단 원칙

1. **결과로 판단합니다.** AI의 자기 보고가 아니라 실행 결과와 작업 폴더 상태를 봅니다.
2. **횟수만으로 막지 않습니다.** 입력과 환경이 그대로이고 새 정보도 진척도 없을 때만 막습니다. 코드를 바꾼 재시도는 막지 않습니다.
3. **막을 때는 이유를 줍니다.** 근거와 다음에 할 일을 함께 돌려줍니다.
4. **모르는 것은 모른다고 합니다.** 계측이 빠지면 미확인, 추정은 추정으로 표시합니다.
5. **기록은 내 컴퓨터에 둡니다.** 원격 전송과 외부 판정 모델은 사용자가 켜기 전에는 쓰지 않습니다.

---

## 설치

### 릴리스 파일

[Releases](https://github.com/JinHo-von-Choi/anti-samcheonpo/releases)에서 `samcheonpo_<버전>_<OS>_<아키텍처>.tar.gz`와 `SHA256SUMS`를 받습니다. linux, darwin, windows용 amd64와 arm64 파일이 있습니다.

```bash
sha256sum -c --ignore-missing SHA256SUMS   # macOS: shasum -a 256 -c --ignore-missing SHA256SUMS
tar -xzf samcheonpo_0.9.2_linux_amd64.tar.gz
mkdir -p ~/.local/bin && cp samcheonpo samcheonpo-hook ~/.local/bin/
samcheonpo doctor
```

`samcheonpo`와 훅 전용 `samcheonpo-hook`은 항상 같은 폴더에 둡니다. Windows는 같은 방법으로 풀어 두 `.exe`를 PATH의 같은 폴더에 두고, Git for Windows를 설치합니다.

### 소스에서 설치

Go 1.27 이상이 필요합니다.

```bash
go install github.com/JinHo-von-Choi/anti-samcheonpo/cmd/samcheonpo@v0.9.2
go install github.com/JinHo-von-Choi/anti-samcheonpo/cmd/samcheonpo-hook@v0.9.2
```

### 업그레이드

업그레이드 전에 원장(`~/.samcheonpo/ledger.db`)을 백업합니다. 현재 원장 스키마는 12이고, 한 번 올라간 원장은 v0.8.0 이하에서 열 수 없습니다. 기존 봉인과 분석 이력은 그대로 유지됩니다. 실행 파일을 바꾼 뒤에는 `uninstall`과 `install`을 다시 실행해 플러그인 파일을 갱신합니다.

## 빠른 시작

**1. 지난 30일 동안 토큰이 어디에 쓰였는지 봅니다.** 아무것도 바꾸지 않고 모델도 호출하지 않습니다.

```bash
samcheonpo audit --since 30d
```

```text
계측된 토큰               26871.5M토큰
  진척 (추정)              3423.1M토큰    13%
  필요한 탐색             13523.5M토큰    50%
  헛짓                      770.3M토큰     3%
실시간이었다면 개입 1418회
헛짓 상위 세션
  3f2a91c0  09-08 10:10     26.7M토큰  헛짓  18%  결제 화면 오류 고쳐 줘
```

세션 하나를 자세히 보려면 `samcheonpo receipt <세션ID>`를 씁니다.

**2. 쓰는 에이전트에 연결합니다.**

```bash
samcheonpo install --agent claude     # Claude Code (플러그인, 상태줄)
samcheonpo install --agent codex      # Codex
samcheonpo install --agent opencode   # opencode
samcheonpo install --agent agy        # Antigravity CLI
samcheonpo install --agent hermes     # Hermes
samcheonpo install --agent openclaw   # OpenClaw
```

기존 훅과 상태줄 설정은 그대로 둡니다. 새 에이전트 세션부터 적용되며, 첫 훅이 불릴 때 데몬이 자동으로 뜹니다.

**3. 평소처럼 일을 시킵니다.** 경고가 뜨면 아래 명령으로 답합니다(Claude Code 기준).

| 명령 | 하는 일 |
| --- | --- |
| `/samcheonpo:summary` | 요청한 일, 지금 하는 일, 끝난 것, 막힌 것, 쓴 돈을 요약 |
| `/samcheonpo:keep normal` | 방금 판정이 틀렸다고 기록. 같은 목표 안에서 같은 대상은 다시 막지 않음 |
| `/samcheonpo:keep wrong` | 판정 근거가 틀렸다고 기록. 해제 범위는 `normal`과 같음 |
| `/samcheonpo:keep now` | 이번만 허용. 다시 생기면 막기 전에 먼저 안내 |
| `/samcheonpo:steer` | 삼천포의 처방을 에이전트에게 전달 |
| `/samcheonpo:check` | 완료 조건을 지금 검사 |
| `/samcheonpo:rollback` | AI가 바꾼 파일을 마지막으로 검사가 통과한 시점으로 되돌림 |
| `/samcheonpo:extend 2h` | 세션 상한을 늘림. 시간은 `2h`·`30m`, 토큰은 `20M`·`500K` |
| `/samcheonpo:accept`, `/samcheonpo:edit`, `/samcheonpo:skip` | 작업 계약 수락·수정, 계약 없이 진행 |

다른 에이전트는 터미널에서 `samcheonpo cmd <명령>`으로 같은 일을 합니다. `accept`, `edit`, `skip`, `keep`, `extend`, `rollback apply`는 사용자 전용이라 에이전트가 셸로 실행하면 거부됩니다.

**4. 연결을 해제합니다.**

```bash
samcheonpo uninstall --agent claude
```

설치 전 상태로 되돌립니다. 사용자가 고친 파일과 기록은 지우지 않습니다.

## 되돌리기

검사가 통과할 때마다 그 시점의 AI 파일을 `.samcheonpo/snapshots`에 기록합니다. 일이 꼬이면 그 시점으로 돌아갈 수 있고, 데몬이 다시 시작돼도 기록은 남습니다.

```mermaid
flowchart LR
    W1[AI가 a.py 수정] --> P[pytest 통과<br/>시점 기록]
    P --> W2[AI가 a.py · b.py 수정]
    W2 --> X[pytest 실패 반복]
    X -->|rollback| R[a.py를<br/>통과 시점으로]
    U[사용자가 b.py 직접 수정] -.->|소유권 불명| R
    R --> D[미리보기 → apply]
```

**AI가 쓰기 도구로 바꿨고 그 뒤 아무도 손대지 않은 파일만 되돌립니다.** 사람이 고친 파일, 셸 명령으로 바뀐 파일, 링크는 건드리지 않고 미리보기에 이유를 표시합니다. 바꾸기 전 내용은 `~/.samcheonpo/backup/`에 백업합니다.

| 명령 | 기준 시점 |
| --- | --- |
| `/samcheonpo:rollback` → `apply <계획 ID>` | 이 세션에서 마지막으로 검사가 통과한 시점 |
| `/samcheonpo:rollback golden` → `golden <id>` → `golden <id> apply <계획 ID>` | 기록된 통과 시점 가운데 하나(최근 5개) |

## 작업 계약 (선택)

기본은 관찰 전용입니다. 완료 여부를 삼천포가 직접 확인하게 하려면 작업 계약을 씁니다.

```yaml
# .samcheonpo/contract.yml
goal: 로그인 세션이 만료되면 재로그인 화면으로 보낸다
done:
  - check: "pytest -q tests/test_session.py"   # 삼천포가 직접 실행
scope:
  allow: ["src/auth/**", "tests/**"]
  protect: ["migrations/**"]                   # 수정하면 즉시 차단
budget: {krw: 5000, minutes: 40}               # 넘으면 정지
```

`/samcheonpo:accept`로 수락하면 완료 조건으로 진척을 재고, 보호 경로를 지키고, 예산을 넘으면 멈춥니다. 수락 기록은 사용자 홈의 키로 서명하므로 에이전트가 쓴 기록은 인정하지 않습니다. 계약 내용을 바꾸면 다시 수락해야 합니다. 세션 밖에서는 `samcheonpo contract show`와 `samcheonpo contract accept`를 씁니다. 작업마다 에이전트가 계약 초안을 쓰게 하려면 `.samcheonpo.yml`에 `contract: {draft: on}`을 둡니다.

## 설정

사용자 설정은 `~/.samcheonpo/config.yml`, 프로젝트 설정은 `.samcheonpo.yml`입니다. `samcheonpo init`이 프로젝트 설정 파일을 만듭니다.

```yaml
rollout:
  mode: recommend            # shadow: 기록만 | recommend: 귀띔, 승격 가능한 규칙만 반복 시 차단
  escalate_max_blocks: 3     # 반복 귀띔의 차단 상한. 0이면 끔. 보호 경로·예산은 별도
  recommend_rules: []        # 기본 기록 전용 규칙 가운데 안내를 받을 규칙 ID (사용자 설정에서만)
contract:
  draft: off                 # on이면 작업마다 계약 초안 요청
notify:
  desktop: true
swarm:
  tree_krw: 0                # 부모·자식 세션을 합한 예산(원). 0이면 없음
detectors:
  s8_cost:
    notice_hours: 2          # 활동 시간 알림
    warn_hours: 4            # 계속할지 확인
    ceiling_hours: 0         # 넘으면 읽기만 허용. 0이면 없음
    ceiling_tokens: 0        # 같은 상한을 토큰으로
  s1_verify_treadmill:
    expensive_check_sec: 300 # 이보다 오래 걸린 시험을 작은 변경 뒤 재실행하면 판정
  s5_release:
    releases_per_hour: 2
```

- 기본 기록 전용 규칙은 `s1.verify_after_docs`, `s1.review_repeat`, `s1.explicit_waiting`, `s8.progress_stall`과 반복·조사·인계 탐지 후보입니다. 안내를 받으려면 규칙 ID를 `recommend_rules`에 넣고, `shadow_rules`에 같은 ID가 있으면 뺍니다.
- 활동 시간은 이벤트 사이 공백이 30분을 넘는 구간을 빼고 셉니다. 계약의 `budget.minutes`도 같은 상한으로 집행합니다.
- 프로젝트 설정은 사용자 설정을 **약하게만** 바꿀 수 있습니다. 차단 상한·예산·세션 상한은 낮추기만 하고, 외부 전송·알림 목적지·외부 프로그램은 끄기만 할 수 있습니다. 항목별 권한은 [시작 가이드](docs/getting-started.md#9-설정-권한)에 있습니다.
- 부모·자식 세션 묶음은 `samcheonpo handoff link`로 이었거나 에이전트가 부모 세션 ID를 보낸 경우에만 생깁니다.

## 기록 조회와 진단

모든 조회 명령은 원장을 읽기만 하고 모델을 호출하지 않습니다.

| 명령 | 보여 주는 것 |
| --- | --- |
| `samcheonpo doctor [--agent 이름]` | 설정, 원장, 훅 연결, 에이전트 버전, 파일 감시 자원 상태 |
| `samcheonpo sessions` | 분석된 세션 목록과 헛짓률 |
| `samcheonpo receipt <세션>` | 세션 하나의 영수증. `--share`는 공유용 비식별 출력 |
| `samcheonpo verify <세션>` | 다시 계산해 봉인 값과 대조 |
| `samcheonpo report --since 30d --mode live` | 에이전트별 관측 범위·환산액·헛짓 분류 비교 |
| `samcheonpo why <세션>`, `coverage <세션>` | 판정 근거, 비용 귀속, 판단하지 못한 이유 |
| `samcheonpo interventions --by-agent` | 처방이 제안·전달·인지·효과 관측 중 어디까지 갔는지, 실행 전 차단 횟수 |
| `samcheonpo lineage <작업ID>` | 같은 목표에 이어진 세션들의 계보와 판정 |
| `samcheonpo handoff <세션>` | 다른 에이전트나 새 세션에 넘길 첫 지시문 |
| `samcheonpo export <세션> --redact` | 경로와 명령을 가린 재현 묶음. `--otel`은 OTLP/JSON |
| `samcheonpo dashboard` | `127.0.0.1:18773`에서 여는 로컬 화면. 시작할 때 출력하는 접근 코드로 로그인 |
| `samcheonpo plugins` | 설치한 플러그인 묶음이 설치 때와 같은지 확인 |
| `samcheonpo compact [--prune-days N]` | 원장 용량 보고와 빈 공간 회수. N일 전에 끝난 세션의 상세만 정리하고 봉인·영수증·판정은 남김 |

`0%`는 계측된 비용 가운데 낭비로 분류한 몫이 없다는 뜻입니다. 정상 작업이나 완료를 보장하지 않습니다. 작은 양수는 `<0.1%`로 표시합니다.

### 탐지 규칙 평가

탐지 규칙의 정확도는 사람이 표시한 구간으로 잽니다. 이 도구들은 정책을 자동으로 적용하지 않습니다.

```sh
samcheonpo eval prepare-review --since 30d --out review     # 실제 원장에서 검토 구간을 뽑아 고정
samcheonpo dashboard --review-queue review/review-queue.json # 판정을 가린 화면에서 사람이 검토
samcheonpo eval compare --queue review/review-queue.json --labels labels --format json
samcheonpo rules propose  --labels labels --split split.json --out proposal.json
samcheonpo rules evaluate proposal.json --labels labels --split split.json --out evaluation.json --check
samcheonpo rules promote  proposal.json --labels labels --split split.json --out rulepack.json
```

- 검토 화면은 탐지 판정과 다른 검토자의 답을 숨기고, 답을 원장·검토 큐의 지문에 묶어 저장합니다.
- 비교 지표는 두 검토자가 합의한 구간에서만 계산합니다. 분모가 없으면 `null`입니다. 검토 큐는 층화 표본이라 이 수치를 전체 정확도로 일반화하지 않습니다.
- `promote`는 독립 평가를 다시 계산한 뒤에만 규칙 팩을 만듭니다. `--install`로 설치해도 기록 전용이며, 안내를 받으려면 출력된 규칙 ID를 `recommend_rules`에 넣어야 합니다.
- 실제 에이전트로 효과를 비교하는 `samcheonpo bench ab`, `bench probe`, `bench tasks`와 회피 측정 `samcheonpo evasion`도 있습니다. 실행 방식 설정 예시는 [bench/ab.example.yml](bench/ab.example.yml)에 있습니다.

세부 조건은 [탐지 평가](docs/spec/detection-assessment.md)와 [규칙 학습](docs/spec/detection-phase2.md)에 있습니다.

## 지원 범위

| 에이전트 | 지원 | 훅 입력을 확인한 버전 |
| --- | --- | --- |
| Claude Code | 관찰 · 귀띔 · 실행 전 차단 · 상태줄 · 슬래시 명령 | 2.1.x |
| Codex | 관찰 · 귀띔 · 실행 전 차단 | 0.160–0.162 |
| opencode | 관찰 · 귀띔 · 실행 전 차단 | 1.18.x |
| Antigravity(agy) | 관찰 · 귀띔 · 실행 전 차단 | 1.3.x |
| Hermes | 관찰 · 귀띔 · 실행 전 차단 | 0.21.x |
| OpenClaw | 관찰 · 귀띔(다음 요청부터) · 실행 전 차단 | 2026.7.x |
| GitHub Copilot · Cursor | 관찰 | |

에이전트가 업데이트돼도 버전 번호만으로 개입을 끄지 않습니다. 훅 입력에 세션, 도구 이름, 호출 ID, 도구 입력, 결과가 그대로 있으면 계속 개입합니다. 형식이 달라진 입력이 오면 그 에이전트는 관찰만 하고, 상태줄과 `samcheonpo doctor`에 이유를 표시합니다.

운영체제는 Linux, macOS, Windows 10 1803 이상을 지원합니다. Windows는 검사 명령을 Git for Windows의 bash로 실행합니다. 플랫폼별 확인 범위는 [지원 범위](docs/support-matrix.md)에 있습니다.

## 한계

- **효과는 아직 입증되지 않았습니다.** 사람이 표시한 정답과 켠 경우·끈 경우의 완료율·비용 비교가 충분하지 않아 절감률을 주장하지 않습니다. 실험 방법은 [실험 프로토콜](docs/benchmarks/protocol-v1.md)에 있습니다.
- **오탐이 있을 수 있습니다.** 그래서 처음에는 귀띔만 하고, 경고를 무시했을 때만 세션당 상한 안에서 막습니다. 틀린 판정은 `/samcheonpo:keep normal`로 알려 주세요.
- **실행 전 차단은 15ms 안에 판정이 끝나야 걸립니다.** 넘기면 실행을 막지 않고 상태줄에 `확인 불가`를 표시합니다.
- **되돌리기는 소유권이 확인된 파일만 다룹니다.** 셸 명령으로 바뀐 파일, 사람 편집이 섞인 파일, 다른 세션의 AI가 쓴 파일은 대상이 아닙니다.
- **사용자 전용 명령의 격리는 완전하지 않습니다.** 에이전트 셸에서는 막지만, 같은 OS 사용자로 도는 프로세스가 데몬에 직접 접속하는 것까지 막지는 않습니다.
- **짧고 명확한 작업에서는 할 일이 거의 없습니다.** 헛짓은 대개 길고 복잡한 세션에서 생깁니다.
- **지시 밀수 탐지는 지시의 존재만 봅니다.** 누가 보냈는지, 에이전트가 따랐는지는 답하지 않습니다. 문서나 시험 픽스처에 같은 형태의 문장이 있어도 반응하므로 사람이 확인해야 합니다.
- **플러그인 무결성 기록은 서명이 아닙니다.** 설치할 때의 묶음이 지금 그대로인지만 확인하고, 배포자는 확인하지 않습니다.
- **공개 벤치 과제의 결과는 약한 근거입니다.** 저장소에 공개된 22개 과제는 모델이 이미 봤을 수 있어, 요약에서 공개 과제와 비공개 과제를 따로 셉니다.

## 더 알아보기

- [시작 가이드](docs/getting-started.md): 설치부터 운영 규칙까지
- [지원 범위](docs/support-matrix.md): 플랫폼과 에이전트별로 확인한 것
- [에이전트용 지침](plugins/claude-code/skills/samcheonpo/SKILL.md): AI가 삼천포를 세팅하고 경고에 맞게 행동하는 규칙. Claude Code 플러그인에 함께 설치됩니다
- 규격: [진척 계약](docs/spec/progress-contract-v1.md) · [근거 원장](docs/spec/evidence-ledger-v1.md) · [영수증 표시](docs/spec/receipt-billing.md) · [복구와 코드 밖 원인](docs/spec/recovery-v1.md) · [인수인계](docs/spec/handoff-v1-draft.md) · [대기·정체 안내](docs/spec/agenttime-reliability.md) · [판정 예산과 위험 등급](docs/spec/judge-budget.md) · [하네스 성분 위치](docs/spec/harness-surface.md) · [훅 기동](docs/spec/hook-startup.md)
- [적합성 사례](conformance/): 다른 구현을 같은 규격으로 채점하는 시험 모음
- [변경 이력](CHANGELOG.md)

## 라이선스

[MIT](LICENSE)

---

<p align="center">
  Made by <a href="mailto:jinho.von.choi@nerdvana.kr">Jinho Choi</a> &nbsp;|&nbsp;
  <a href="https://buymeacoffee.com/jinho.von.choi">Buy me a coffee</a>
</p>

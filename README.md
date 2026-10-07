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

## 왜 만들었나

바이브코딩 현장에서 개발자와 비개발자의 가장 큰 차이는 **AI가 지금 헛짓을 하고 있는지 알아보는 눈**에 있습니다. 개발자는 화면을 흘겨보다가도 "또 같은 걸 돌리네", "이건 시킨 일이 아닌데" 하고 끼어듭니다. 비개발자에게는 그 순간을 알아볼 기준 자체가 없습니다.

그래서 이런 일이 생깁니다.

- **필요 이상의 검증.** 결과물은 그대로인데 같은 시험을 몇 번이고 다시 돌리며 며칠씩 토큰을 태우다 사용 한도가 바닥납니다.
- **엉뚱한 방향.** 로그인 오류를 고쳐 달라고 했는데 어느새 로그인 방식 전체를 갈아엎고 있습니다.
- **같은 자리에서 맴돌기.** 모델이 감당하지 못하는 문제 앞에서 간단한 작업도 계속 실패하며 무한 루프를 돕니다. 설치할 수 없는 패키지, 꺼져 있는 서버처럼 코드로 풀 수 없는 문제를 코드로 풀려고 덤벼들기도 합니다.

AI는 그 시간 내내 "거의 다 됐습니다"라고 말합니다. 사용자는 모른 채 멀뚱멀뚱 지켜보다가 시간과 돈을 날립니다. 눈 뜨고 코 베이는 식으로. 삼천포는 이 **멍청비용**을 줄이려고 만든 하네스입니다. 이야기가 엉뚱한 데로 새는 것을 "삼천포로 빠진다"고 하듯, 작업이 삼천포로 빠지는 순간을 잡는다는 뜻입니다.

## 어떻게 돕는가

**1. 지켜봅니다.**
Claude Code, Codex, opencode의 훅에 붙어 에이전트가 실행하는 명령, 고치는 파일, 시험 결과, 쓰는 토큰을 실시간으로 봅니다. AI가 "통과했다"고 말해도 믿지 않고 실제 실행 결과로만 판단합니다.

**2. 쉬운 말로 알려 줍니다.**
"identical rerun detected" 대신 이렇게 말합니다.

```text
AI가 같은 시험을 4번 돌렸는데, 그 사이 코드는 한 글자도 바뀌지 않았습니다.
코드 밖 조건(설치되지 않은 의존성) 때문일 가능성이 높은 실패로 같은 명령이 2번 실패했습니다.
AI가 요청 범위 밖 파일을 고쳤습니다 (src/theme/dark.css, 누적 2개).
```

경고마다 AI에게 그대로 붙여 넣을 수 있는 한 문장과 선택지가 함께 나옵니다.

```text
AI에게 이렇게 말해 보세요: "src/theme/dark.css는 내가 부탁한 범위가 아니야. 꼭 필요한 변경이면 이유를 먼저 설명하고, 아니면 되돌려 줘."
[/samcheonpo:keep now 이번만 허용]  [/samcheonpo:keep normal 이 판정은 틀림]  [/samcheonpo:steer 방향 바꾸라고 하기]  [/samcheonpo:summary 멈추고 요약 받기]
```

상태줄 맨 앞에는 지금 상태가 네 가지 중 하나로 보입니다: 순조로움(진척 근거가 있음), 지켜보는 중, 지금 끼어드세요, 확인 불가(관측이 불완전함). 경고가 없다는 것만으로 순조로움이라고 표시하지 않습니다.

```text
[지켜보는 중] 진척 1/2 · 공회전 API환산 1,240원 · 헛짓 12% (계측분)
```

**3. 무시하면 막습니다.**
처음에는 에이전트에게 근거와 함께 귀띔만 합니다. 같은 세션에서 같은 경고를 받고도 같은 대상에 같은 헛짓을 반복하면, 그다음 실행을 **실행 전에** 막고 이유를 돌려줍니다. 막힌 것이 잘못된 판단이면 `/samcheonpo:keep normal` 한 번으로 풀립니다.

**4. 지난 일을 돌아보게 합니다.**
`samcheonpo audit`는 지난 Claude Code·Codex 기록을 읽어, 토큰이 진척·필요한 탐색·헛짓 중 어디에 쓰였는지 영수증으로 보여 줍니다. 모델을 호출하지 않으며 기록은 내 컴퓨터를 떠나지 않습니다.

```text
AI로 일을 시킨다 → 삼천포가 지켜본다 → 헛짓이면 알려 준다 → 무시하면 막는다 → 영수증으로 돌아본다
```

> 삼천포가 보여 주는 원화는 **계측된 토큰을 API 단가로 환산한 금액**입니다. 실제 청구액이나 절감액이 아닙니다. 사용량을 알 수 없으면 0원이 아니라 "미확인"으로 표시합니다.

---

## 무엇을 잡나

| 멍청비용 | 삼천포가 보는 신호 | 개입 |
| --- | --- | --- |
| **과잉 검증** | 코드 변경 없이 같은 시험 반복 · 이미 통과한 근거가 있는 검사 재실행 · 문서만 바꾸고 재검증 · 같은 상태에서 같은 리뷰 반복 | 귀띔 → 반복 시 실행 전 차단(이전 결과 제시) |
| **맥락 이탈** | 요청 범위 밖 파일 수정 · 보호 경로 수정 · 실제 서비스를 가짜로 바꿔 시험 통과 · 맥락 압축 뒤 이전 목표로 회귀 | 귀띔 · 보호 경로는 즉시 차단 · 요청한 일과 지금 하는 일을 나란히 표시 |
| **능력 부족 루프** | 고쳐도 같은 오류 반복 · 같은 파일만 계속 고치다 실패 · 이미 실패한 수정을 다시 시도(세션이 바뀌어도) · 왔다 갔다 되돌리기 · 코드 밖 조건(설치·서비스·권한) 때문일 가능성이 높은 반복 실패 | 귀띔 → 반복 시 차단 → 자동 재시도 중단과 인수인계 문서 |
| **허위 완료** | 시험 삭제·skip·단언 약화 · 오류를 숨기는 코드 · 완료 조건이 실패한 채 "완료" 선언 | 귀띔 → 반복 시 차단 |
| **비용 폭주** | 진척 없이 쌓이는 지출 · 비정상적인 지출 속도 · 설정한 예산 초과 | 알림 · 예산 초과 시 정지 |

판단 원칙은 다섯 가지입니다.

1. **결과로 판단한다.** AI의 자기 보고가 아니라 실행 결과·작업 폴더 상태·시험 결과로 판단합니다.
2. **횟수만으로 막지 않는다.** 입력과 환경이 그대로이고 새 정보도 진척도 없을 때만 막습니다. 코드를 바꾼 재시도는 막지 않습니다. 마지막 실패가 서비스·의존성·권한·네트워크 원인이었다면 다음 재실행 한 번은 회복 확인으로 통과시키고, 설치나 서비스 기동 명령이 사이에 있으면 반복을 새로 셉니다.
3. **막을 때는 이유를 준다.** 무엇을 근거로, 무엇을 하면 되는지 함께 돌려줍니다. 실행 결과를 흉내 낸 가짜 출력은 만들지 않습니다.
4. **모르는 것은 모른다고 한다.** 계측이 빠지면 0원이 아니라 미확인, 추정은 추정이라고 표시합니다.
5. **기록은 내 컴퓨터에 둔다.** 원격 전송이나 외부 판정 모델은 사용자가 명시적으로 켜기 전에는 쓰지 않습니다.

---

## 설치

### 릴리스 파일 (Linux x86-64)

[Releases](https://github.com/JinHo-von-Choi/anti-samcheonpo/releases)에서 `samcheonpo_0.2.1_linux_amd64.tar.gz`와 `SHA256SUMS`를 받습니다.

```bash
sha256sum -c SHA256SUMS
tar -xzf samcheonpo_0.2.1_linux_amd64.tar.gz
mkdir -p ~/.local/bin && cp samcheonpo samcheonpo-hook ~/.local/bin/
samcheonpo doctor
```

두 실행 파일(`samcheonpo`, 가벼운 훅용 `samcheonpo-hook`)은 같은 폴더에 둡니다.

### 소스에서 설치 (macOS·Linux arm64 등)

Go 1.27 이상이 필요합니다. 현재 실제 실행을 확인한 환경은 Linux x86-64뿐입니다.

```bash
go install github.com/JinHo-von-Choi/anti-samcheonpo/cmd/samcheonpo@v0.2.1
go install github.com/JinHo-von-Choi/anti-samcheonpo/cmd/samcheonpo-hook@v0.2.1
```

Windows 네이티브는 지원하지 않습니다(WSL은 미검증).

---

## 빠른 시작

**1. 지난 30일에 토큰이 어디로 갔는지 봅니다.** 설치 직후 바로 해 볼 수 있고, 아무것도 바꾸지 않습니다.

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

세션 하나를 자세히 보려면 `samcheonpo receipt <세션>`을 씁니다.

**2. 에이전트에 연결합니다.**

```bash
samcheonpo install --agent claude     # Claude Code (플러그인과 상태줄)
samcheonpo install --agent codex      # Codex
samcheonpo install --agent opencode   # opencode
```

기존 훅과 상태줄 설정은 보존됩니다. 이제 평소처럼 AI에게 일을 시키면 됩니다. 처음 훅이 불릴 때 백그라운드 데몬이 자동으로 뜹니다.

**3. 작업 중에 쓸 수 있는 명령 (Claude Code)**

| 명령 | 하는 일 |
| --- | --- |
| `/samcheonpo:summary` | 요청한 일, 지금 하는 일, 끝난 것, 막힌 것, 쓴 돈을 평문으로 요약 |
| `/samcheonpo:keep normal` | 방금 판정이 틀렸다고 알림. 지금 작업 목표 안에서 같은 대상의 같은 판정은 다시 막지 않음(보호 경로·예산은 그대로) |
| `/samcheonpo:keep now` | 이번만 넘어감. 다시 생기면 막기 전에 먼저 안내 |
| `/samcheonpo:steer` | 삼천포의 처방을 에이전트에게 전달 |
| `/samcheonpo:check` | 완료 조건을 지금 검사 |
| `/samcheonpo:rollback` | AI가 쓰기 도구로 바꾼 파일을 마지막 진척 시점(검사 통과)으로 되돌리기. 인자 없이는 미리보기, `apply <계획 ID>`로 미리본 그대로 실행. 소유권이 확실하지 않은 파일(사람 편집이 섞임, 셸·링크로 바뀜)은 건드리지 않고, 바꾸기 전 내용을 백업 |
| `/samcheonpo:accept`, `/samcheonpo:edit` | 작업 계약 수락·수정 (계약을 쓸 때) |

`/samcheonpo:summary` 예시:

```text
요청한 일: "src/auth/session.py 만료 처리 고쳐 줘"
지금 하는 일: 최근에 src/theme/dark.css, src/auth/session.py를 바꿨다, 마지막 명령은 `pytest -q`.
요청 범위 밖으로 보이는 변경: src/theme/dark.css. 의도한 변경이 아니라면 방향을 다시 알려 주면 된다.
끝난 것: 아직 확인된 진척이 없다.
```

**4. 연결을 해제합니다.**

```bash
samcheonpo uninstall --agent claude
```

설치 전 상태로 되돌리며, 사용자가 직접 고친 파일은 지우지 않습니다.

---

## 작업 계약 (선택)

기본은 **관찰 전용**입니다. 에이전트에게 추가 일을 시키지 않고 지켜보기만 합니다. "무엇이 끝나야 완료인가"를 하네스가 직접 확인하게 하려면 작업 계약을 씁니다.

```yaml
# .samcheonpo/contract.yml
goal: 로그인 세션 만료 시 재로그인 화면으로 보낸다
done:
  - check: "pytest -q tests/test_session.py"
scope:
  allow: ["src/auth/**", "tests/**"]
  protect: ["migrations/**"]
budget: {krw: 5000, minutes: 40}
```

`/samcheonpo:accept`로 수락하면, 삼천포가 완료 조건 검사를 직접 실행해 진척을 재고, 보호 경로 수정은 막고, 예산을 넘으면 멈춥니다. 매 작업마다 에이전트가 초안을 쓰게 하려면 `.samcheonpo.yml`에 `contract: {draft: on}`을 둡니다.

수락한 뒤 계약 파일의 항목을 하나라도 바꾸면(목표 포함) 다시 수락해야 합니다. 수락 기록은 사용자 홈의 키로 서명하므로, 에이전트가 직접 쓰거나 다른 프로젝트에서 복사한 기록은 수락으로 인정하지 않습니다.

## 설정 (`.samcheonpo.yml`, `~/.samcheonpo/config.yml`)

```yaml
rollout:
  mode: recommend            # shadow(기록만) | recommend(귀띔, 무시하면 차단) | validated
  escalate_max_blocks: 3     # 세션당 차단 상한, 0이면 차단하지 않음
contract:
  draft: off                 # on이면 새 작업마다 계약 초안을 요청
notify:
  desktop: true
```

프로젝트 설정은 사용자 설정보다 **약하게만** 바꿀 수 있습니다. 탐지 기준을 더 엄격하게 하거나 차단 상한을 올릴 수 없고, 외부 전송·알림 목적지·실행할 외부 프로그램을 추가하거나 바꿀 수 없으며 끄기만 할 수 있습니다. 새로 추가된 규칙은 오탐률을 잴 때까지 기록만 하는 `shadow_rules`에서 시작합니다.

연구용 실험 모드(`experiment: {enabled: true}`)는 기본으로 꺼져 있습니다. 켜면 일부 안내를 대조군으로 보류해 전달하지 않으며, 상태줄에 그 사실을 표시합니다. 프로젝트 설정으로는 켤 수 없습니다.

## 지원 범위

| 에이전트 | 지원 | 확인한 버전 |
| --- | --- | --- |
| Claude Code | 관찰 · 귀띔 · 실행 전 차단 · 상태줄 · 슬래시 명령 | 2.1.x |
| Codex | 관찰 · 귀띔 · 실행 전 차단 | 0.160.x |
| opencode | 관찰 · 귀띔 · 실행 전 차단 | 1.18.x |
| GitHub Copilot · Cursor · Antigravity(agy) | 관찰 전용 | |

| 환경 | 상태 |
| --- | --- |
| Linux x86-64 | 실제 설치·제거·훅 지연(p95 10ms 이내) 확인 |
| macOS · Linux arm64 | 소스 설치 가능, 실제 실행 미확인 |
| WSL | 미확인 |
| Windows 네이티브 | 미지원 |

---

## 한계

- **효과는 아직 입증 중입니다.** 예비 실험에서 헛도는 과제의 완료당 비용이 줄어든 사례는 있지만, 표본이 작아 일반적인 절감률을 주장하지 않습니다. 2026-10-07 실제 Claude Code(Sonnet) 6과제 파일럿에서는 하네스 유무와 관계없이 6/6을 완료했고 개입은 0건이었습니다. 과제가 현재 모델에서 헛돌지 않아 효과를 측정하지 못했습니다. 실험 방법과 한계는 [실험 프로토콜](docs/benchmarks/protocol-v1.md)에 있습니다.
- **오탐이 있을 수 있습니다.** 그래서 처음에는 귀띔만 하고, 차단은 같은 세션의 같은 대상에서 경고를 무시했을 때만, 세션당 상한 안에서 합니다. 틀린 판정은 `/samcheonpo:keep normal`로 알려 주세요.
- **실행 전 차단은 훅이 기다리는 시간(15ms) 안에 판정이 끝나야 전달됩니다.** 넘기면 실행을 막지 않고, 상태줄에 판정 시간 초과 건수와 `확인 불가`를 표시합니다.
- **되돌리기는 쓰기 도구(Write·Edit)로 바뀐 파일 중 소유권이 확인된 것만 다룹니다.** 셸 명령으로 바뀐 파일, 사람 편집이 섞인 파일, 링크는 대상이 아닙니다.
- **사용자 전용 명령은 에이전트 셸에서 막지만 완전한 격리는 아닙니다.** `accept`·`keep`·`rollback apply`를 에이전트의 셸 도구로 실행하면 거부합니다. 같은 OS 사용자로 도는 프로세스가 데몬 소켓에 직접 접속하는 것까지 막지는 않습니다.
- **짧고 명확한 작업에서는 할 일이 거의 없습니다.** 헛짓은 대개 길고 복잡한 세션에서 생깁니다.
- **원화는 API 환산액입니다.** 구독 요금제의 실제 청구액이나 남은 한도를 뜻하지 않습니다.
- **에이전트의 훅 규격이 바뀌면** 일부 신호를 놓칠 수 있습니다. `samcheonpo doctor`로 연결 상태를 확인할 수 있습니다.

## 더 알아보기

- [처음 사용하기](docs/getting-started.md) · [지원·검증 범위](docs/support-matrix.md)
- 규격: [진척 계약](docs/spec/progress-contract-v1.md) · [근거 원장](docs/spec/evidence-ledger-v1.md) · [영수증 표시](docs/spec/receipt-billing.md) · [인수인계](docs/spec/handoff-v1-draft.md)
- 평가: `samcheonpo bench`(결정적 시나리오), `samcheonpo bench ab`(실제 에이전트 비교), `samcheonpo gaps`(규칙이 놓친 의심 구간 추출), `samcheonpo interventions`(처방이 어디까지 전달됐는지)
- 다른 도구도 같은 규격으로 채점할 수 있게 [적합성 사례](conformance/)를 공개합니다.

## 라이선스

[MIT](LICENSE)

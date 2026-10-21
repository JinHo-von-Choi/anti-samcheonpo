# GitHub Copilot CLI 지원 실측

`2026-10-22`, Linux x86-64, GitHub Copilot CLI `1.0.101`에서 실측한 결과다.
측정은 `scripts/probe_copilot_hooks.py`가 임시 작업 디렉터리와 임시 `COPILOT_HOME`에서
`--allow-all-tools`와 `COPILOT_ALLOW_ALL=true`로 실행해 얻었다.
사용자의 실제 Copilot 설치·인증·감시 설정은 건드리지 않았다.

## 계약을 대체로 읽었다

Copilot 훅은 두 가지 표기를 쓴다. 런타임 이벤트 이름(`sessionStart`, `preToolUse`,
`postToolUse`, `agentStop`, `userPromptSubmitted`, `sessionEnd`)은 camelCase payload를,
PascalCase 별칭(`SessionStart`, `PreToolUse`, `PostToolUse`, `Stop`, `SessionEnd`)은
snake_case payload를 준다. 두 표기를 모두 등록하면 같은 사건이 두 번 도착한다.
설치 파일은 런타임 이름만 등록한다.

`UserPromptSubmitted` 별칭은 발화하지 않았다. `permissionRequest`와 `PermissionRequest`는
같은 camelCase payload를 준다. `errorOccurred`·`preCompact`·`notification`·`subagentStart`·
`subagentStop`는 이번 수집에서 발화하지 않았다.

## 지면에서 본 사실

- **어느 payload에도 도구 호출 ID가 없다.** 시작과 결과는 세션 안에서 도구 이름과 도착
  순서로만 이어진다. 두 호출의 입력이 같아도 삼천포는 두 사건으로 남긴다.
- `timestamp`는 런타임 이름에서 epoch 밀리초, PascalCase 별칭에서 ISO 8601 문자열이다.
- 셸 종료 코드는 payload 필드가 아니라 결과 본문의
  `<shellId: N completed with exit code N>` 문장으로만 온다. 종료 코드 7이어도
  `resultType`은 `success`이고 `postToolUseFailure`는 발화하지 않는다. 코드가 없으면
  삼천포는 성공을 추정하지 않고 미상으로 남긴다.
- `apply_patch`의 도구 입력은 객체가 아니라 patch 본문 문자열이다. `view`는 `file_path`가
  아니라 `path`를 쓴다.
- `agentStop`은 `stopReason`, `sessionEnd`는 `reason`("complete")을 준다.
- `errorOccurred`는 `error.message`·`errorContext`·`recoverable`를 준다.

입력 35건에서 대표 21건을 익명화해 `testdata/hooks/copilot/`에 넣었다.
[입력 자료 설명](../../testdata/hooks/copilot/README.md) · [원본 캡처](../../bench/copilot/2026-10-22/raw/)

## 전달을 확인한 것

Copilot CLI가 삼천포의 응답을 실제로 읽는지, CLI 계약만으로 따로 확인했다.

| 확인 | 방법 | 결과 |
| --- | --- | --- |
| 실행 전 차단 | `permissionDecision: "deny"` + 사유를 stdout으로 출력 | 도구 호출이 차단되고 에이전트가 사유를 본다 |
| 도구 뒤 안내 | `additionalContext`를 stdout으로 출력 | 문맥이 모델에 들어가 최종 답에 반영된다 |
| 시간 초과 | Copilot 문서 + 훅 파일의 `timeoutSec` | 모든 이벤트에서 시간 초과·다른 0 아닌 종료 코드는 계속 진행(fail-open). `preToolUse`만 0 아닌 종료 코드에 fail-closed다 |

삼천포가 실제로 만들어내는 응답도 같은 경로로 확인했다.

| 확인 | 방법 | 결과 |
| --- | --- | --- |
| 관측 경로 | 실제 CLI → `samcheonpo-hook` → 데몬 → 원장 | 도구 5건이 1:1로 기록된다. 셸 종료 코드 0·7·0이 그대로 남고, 읽기·편집은 종료 코드가 없다. `copilot session end: complete`도 남는다 |
| 도구 뒤 안내 전달 | 같은 명령을 반복해 삼천포 관찰 문장을 만들고 실제 CLI에 전달 | `[삼천포] 관찰: ...` 문장이 에이전트 출력에 나타난다 |
| 실행 전 차단 전달 | 에이전트가 `samcheonpo cmd accept`를 실행하려 할 때 | `Denied by preToolUse hook: [삼천포] ...`로 차단되고 명령은 실행되지 않는다 |

## 확인하지 못한 것

- **종료 후속 실행**(stop continuation)은 검증하지 않았다. `agentStop`의 `decision: "block"`
  경로는 Copilot에 등록하지 않았고 비활성 상태다.
- **프롬프트 주입**은 검증하지 않았다. Copilot은 `userPromptSubmitted`의 출력 계약을
  문서에 적어 두지 않았다.
- **실행 전 안내**는 전달할 자리가 없다. Copilot의 `preToolUse` 출력에는 안내 문맥 자리가
  없고, 차단 사유 자리만 있다. 실행 전 조언은 조용히 사라지며 전달된 것으로 기록되지 않는다.
- 토큰·비용은 확인하지 않았다. Copilot 훅은 사용량을 알려 주지 않아 원장의 비용이 비어 있다.
- 종료 코드 7이 `postToolUseFailure`로 오지 않아, 실패 도구 전용 경로의 전달은 확인하지 못했다.
- IDE 연동, Cloud Agent, Windows·macOS는 확인하지 않았다.
- `errorOccurred`·`preCompact`·`notification`·`subagentStart`·`subagentStop`의 payload는
  이번 수집에 없었다. 변환은 문서 계약에 맞춰 만들었지만 실측 근거는 없다.

## 판단

Copilot은 관찰 전용을 유지하지 않는다. 실행 전 차단과 도구 뒤 안내는 위 표처럼 실제 CLI로
확인했고, 삼천포가 만들어내는 응답이 같은 경로로 전달되는 것도 확인했다.
그래서 `adapter.Profiles["copilot"]`에 `BlockPre`와 `InjectPost`만 켜고,
`BlockStop`·`PromptInject`·`InjectPre`·`EndTurnPost`·`UserMessage`는 끈 채로 둔다.

입력 캡처가 있는 날짜와 빌드 문자열(`1.0.101`)만 시험 버전으로 표시한다.
범위 밖 버전이라고 개입을 끄지는 않되, 훅 입력 형식이 달라지면 그때 관찰 전용으로 낮춘다.

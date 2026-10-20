# 하네스 성분 위치 규격

외부 하네스 용어(내부/외부 하네스, 가이드/센서)를 삼천포 자신의 위치에 대응시킨다. 규격은 데이터로 있고, 파일은 `internal/surface/harness-surface.json`이다. `samcheonpo surface`가 그 표를 그대로 보여 준다.

## 왜 이 문서가 있다

다른 프로젝트의 하네스 용어를 읽는 사람은 늘 같은 질문을 한다. "이건 센서인가 가이드인가, 내부 하네스인가 외부 하네스인가." 삼천포는 감시자다. 에이전트의 하네스를 편집하는 제품이 아니다. 그 경계를 말로만 반복하면 읽는 사람이 매번 다시 판단해야 한다. 그래서 대응표를 데이터로 두고, 그 데이터가 실제 경로를 가리키는지를 시험으로 확인한다.

## 용어 대응

| 외부 용어 | 삼천포에서의 뜻 |
|---|---|
| 내부 하네스 | 에이전트가 스스로 읽는 파일. 삼천포는 이쪽을 고치지 않는다. 수정 대상은 감시 구성뿐이다. |
| 외부 하네스 | 삼천포가 에이전트를 감시하며 제공하는 구성. 계약을 붙이고 권고를 넣고 차단을 건다. |
| 가이드 | 에이전트의 목표와 완료 조건을 담는 성분. 삼천포에서 계약·목표 카드가 이 역할이다. |
| 센서 | 에이전트의 행동을 읽고 사실을 남기는 성분. 삼천포 전체가 이 역할이다. |
| 편집 대상 | 바꾸면 감시의 판정이 달라지는 성분. 삼천포에서는 규칙 팩 하나뿐이다. |

## 성분표

| 성분 | 역할 | 위치 | 비고 |
|---|---|---|---|
| sensor | sensor | `internal/detect`, `internal/ledger`, `internal/receipt`, `internal/analyze` | 도구 호출을 읽고 사실과 판정을 남긴다. 관찰만 한다. |
| guide | guide | `internal/contract`, `internal/intent` | 목표와 완료 조건. 파일 표현은 `.samcheonpo/contract.yml`과 원장 속 개정 기록. |
| editable | editable | `internal/rulepack`, `internal/learning`, `internal/eval` | 규칙 팩이 유일하게 편집 대상이다. |
| policy | external_harness | `internal/policy`, `internal/config` | 권고·차단 경로와 개입 수위. 사용자 설정만 바꾼다. |
| injection | guide | `internal/intervene`, `internal/recovery` | 감지 결과를 에이전트에게 전달하는 통로. 실행 권한을 만들지 않는다. |
| adapter | external_harness | `internal/adapter/claude`, `internal/adapter/codex` | 에이전트별 사건을 하나의 사건 모형으로 옮긴다. |
| plugin | external_harness | `plugins/claude-code`, `plugins/opencode`, `plugins/hermes`, `plugins/openclaw` | 배포 산출물. 무결성 매니페스트의 대상이다. |
| distribution | external_harness | `internal/install` | 설치·제거와 무결성 기록. |
| measurement | external_harness | `internal/bench`, `internal/evasion`, `internal/fp` | 오탐·회피 측정과 실행 동일성. 감시자가 아니라 실험 장치다. |

## 성분 관찰 가능성

편집 대상 성분은 파일로 표현되어 차분이 남아야 한다. 삼천포에서 이 조건을 만족하는 성분은 규칙 팩 하나다. `advisory-rulepack/1` 형식은 JSON이고, 배워서 쓰는 규칙은 `~/.samcheonpo/learned-rules.json`에 남는다. 그래서 "무엇을 바꿨고 왜 바꿨는지"를 파일 차이로 남길 수 있다.

## 이 규격이 하지 않는 것

이 표는 위치를 말한다. 권한을 바꾸지 않는다. 새 규칙이 만들어도 이 표에 없는 성분이 편집 대상이 되지는 않는다. 규칙 팩 외의 성분을 편집 대상으로 만들려면 이 문서와 `internal/surface/harness-surface.json`을 함께 고쳐야 하며, 그때 감시의 성질이 바뀐다는 뜻이다.

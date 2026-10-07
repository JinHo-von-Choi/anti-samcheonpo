# Evidence Ledger v1

식별자: `evidence-ledger/1`

근거 원장은 에이전트 세션의 사건, 판정, 비용을 제3자가 다시 계산할 수 있는 형태로 남기는 형식이다. 같은 입력과 같은 판정기 버전, 단가표, 설정이면 누구든 같은 행, 같은 합계, 같은 봉인 값을 얻어야 한다.

## 1. 사건(event)

공통 사건 형식의 필드는 다음과 같다. 원문 텍스트(코드, 프롬프트, 명령 원문, 도구 출력)는 원장에 넣지 않는다.

| 필드 | 형식 | 의미 |
| --- | --- | --- |
| `seq` | 정수 | 세션 안 단조 증가 번호 |
| `ts` | 문자열 | UTC, `2006-01-02T15:04:05.000Z` 형식, 모르면 빈 문자열 |
| `kind` | 문자열 | `tool`, `message`, `compact`, `prompt`, `stop`, `session_start`, `session_end` |
| `tool` | 문자열 | `shell`, `read`, `write`, `edit`, `search`, `web`, `todo`, `task`, `other`, `checkpoint` 또는 빈 문자열 |
| `cmd_fp` | 문자열 | 정규화 명령의 지문 |
| `paths` | 문자열 배열 | 경로 지문(PathID) 배열, 사전순 정렬 |
| `write_hashes` | 객체 | 경로 지문 → 결과 내용 해시 |
| `ws_before`, `ws_after` | 문자열 | 작업 폴더 지문 |
| `exit` | 정수 또는 null | 종료 코드, -1은 실행되지 않음(차단) |
| `err_fps` | 문자열 배열 | 오류 지문, 정렬 |
| `failed_tests` | 문자열 배열 | 실패 시험 이름의 지문(`H("test", 이름)`), 정렬 |
| `result_fp` | 문자열 | 결과 지문 |
| `in`, `out`, `cache_read`, `cache_write`, `cache_write_1h` | 정수 | 이 사건에 귀속된 토큰. `cache_write_1h`는 `cache_write` 중 1시간 보관분 |
| `model` | 문자열 | 모델 식별자 |
| `cost_micro_krw` | 정수 | 마이크로원 |
| `priced` | 불리언 | 단가표로 원화 환산되었는가 |
| `category` | 문자열 | `produce`, `verify`, `explore`, `repair`, `revert`, `plan`, `watch` |
| `bucket` | 문자열 | 영수증 분류: `progress`, `explore`, `waste`, `other`, `watch` |
| `symptom` | 문자열 | `bucket`이 `waste`일 때 `S1`~`S8` 또는 `revert` |
| `estimated` | 불리언 | 추정 판정의 근거 사건(영수증 `확인 필요`) |
| `forced` | 불리언 | 자동 계속 장치가 만든 턴 |

### 1.1 지문

- `H(parts...)`: 각 부분을 0x00으로 이어 SHA-256을 구하고 앞 16바이트를 소문자 16진수로 쓴다.
- 명령 지문: `H("cmd", 정규화 명령)`. 정규화는 환경변수 접두사, `cd <dir> &&` 접두사, `2>&1`·`>/dev/null` 같은 출력 방향, 끝의 `| tail/head/grep/less/cat/tee` 단계, `timeout N`·`time`·`npx --yes` 래퍼를 걷어내고 연속 공백을 하나로 줄인다. 이 지문은 표시와 통계용이다.
- 실행 지문(`exec_fp`): 실시간 반복 판정과 차단에 쓰며 원장의 정규 행과 봉인에는 들어가지 않는다. `H("exec/1", 유효 디렉터리, 정렬한 환경변수 할당을 0x00으로 이은 문자열, 정규화 명령)`이다. 명령 지문과 달리 `cd` 대상과 앞에 붙은 환경변수 할당을 버리지 않는다. 치환(`$()`, 백틱), 변수 참조, 명령 목록(`&&`, `||`, `;`), 셸 자체를 바꾸는 내장 명령(`cd`, `export`, `source` 등), 프로젝트 밖 디렉터리를 쓰는 명령은 실행 내용을 글자로 확정할 수 없다고 보고 반복으로 차단하지 않는다.
- 경로 지문(PathID): `H("path", 루트 기준 상대 경로)`.
- 오류 지문: `H("err", 예외 종류, 메시지 틀, 상위 3개 프레임(파일명:함수명)을 ;로 이은 문자열)`. 메시지 틀은 시각을 `<t>`, 32자 넘는 따옴표 문자열을 `<s>`, 경로를 `<path>/파일명`, 16진수를 `<hex>`, 숫자를 `<n>`으로 바꾼다.
- 결과 지문: `H("result", 종료 코드 또는 "nil", 정렬한 오류 지문을 ,로 이은 문자열, 정렬한 실패 시험을 ,로 이은 문자열)`.

## 2. 판정(verdict)

| 필드 | 형식 |
| --- | --- |
| `seq` | 판정이 나온 사건 번호 |
| `detector` | `S1`~`S8` |
| `rule` | 규칙 식별자, 예: `s1.identical_rerun` |
| `confidence` | 소수 둘째 자리까지 |
| `level` | 0~4 |
| `evidence` | 근거 사건 번호 배열 |
| `waste_micro_krw` | 이 판정이 확정한 낭비 |
| `primary` | 그 사건에서 전달된 판정인가 |
| `suppressed` | 쿨다운으로 전달되지 않았는가 |
| `estimate` | 추정 판정인가 |

## 3. 정규 직렬화

각 행은 필드 순서와 표기가 고정된 JSON 한 줄이다. 공백을 넣지 않고, 문자열은 JSON 이스케이프를 따르며, 배열은 위 정렬 규칙을 따르고, 객체는 키 사전순이다. 사건 행의 필드 순서는 1절 표 순서, 판정 행은 2절 표 순서다.

## 4. 근거 해시 사슬

```text
chain_0 = SHA256("" || 0x00 || row_0)
chain_i = SHA256(chain_{i-1} || 0x00 || row_i)
```

행 순서는 사건(seq 순) 다음 판정(발생 순)이다. 마지막 `chain` 값이 머리 값(head)이다. 소문자 16진수 64자로 쓴다.

## 5. 봉인(seal)

```json
{"spec": "evidence-ledger/1", "session_id": "…", "agent": "claude", "source_hash": "<입력 기록 SHA-256>",
 "contract_hash": "<checks_hash>", "evaluator": "samcheonpo-eval/…", "price_version": "…", "config_hash": "…",
 "head": "…", "rows": 12, "total_micro_krw": 732040, "total_tokens": 1030, "bucket_micro_krw": {"waste": 732040}}
```

## 6. 비용

`cost_micro_krw = round((in·p_in + out·p_out + cache_read·p_cr + (cache_write − cache_write_1h)·p_cw + cache_write_1h·p_cw1h) · fx)`

단가 `p_*`는 USD/100만 토큰, `p_cw1h`가 없으면 `2·p_in`, `fx`는 사건 시각 기준 USD/KRW. 100만이 서로 상쇄되어 결과는 마이크로원이다. 단가표에 없는 모델은 0원이 아니라 `priced: false`로 남기고 토큰으로만 집계한다. 영수증 줄의 원 단위 반올림 오차는 가장 큰 줄에 몰아 합계를 맞춘다.

## 7. 재현 묶음과 검증

`samcheonpo export <세션> --redact`가 만드는 묶음은 봉인, 정규 행, 사용한 단가 행과 환율을 담는다. 경로와 시험 이름은 이미 지문이므로 행을 바꾸지 않고 내보낼 수 있다. 원본 기록 해시는 비운다.

- 묶음 검증: 사슬을 다시 계산해 끊긴 행이 없는지, 머리 값이 봉인과 같은지 확인하고, 각 사건의 비용을 묶음의 단가로 다시 계산해 행 값, 분류 합계, 총합과 대조한다.
- 원본 검증: 원본 기록이 있는 장비에서 같은 판정기, 단가표, 설정으로 다시 분석해 봉인 전체를 대조한다. 판정, 분류, 마이크로원 합계가 바이트 단위로 같아야 한다.

## 8. 예시

`seq` 0인 실패한 셸 검증 사건 하나와 그 판정 하나로 이루어진 원장:

```text
{"seq":0,"ts":"2026-10-01T09:00:00.000Z","kind":"tool","tool":"shell","cmd_fp":"4f2a","paths":["9d3578cb9bdba9e920085e06f11fca16"],"write_hashes":{},"ws_before":"ws1","ws_after":"ws1","exit":1,"err_fps":[],"failed_tests":["6a2ce6c4db7c0dc19bfd6afe4378295c"],"result_fp":"r1","in":10,"out":20,"cache_read":1000,"cache_write":0,"cache_write_1h":0,"model":"claude-sonnet-5-5","cost_micro_krw":732040,"priced":true,"category":"verify","bucket":"waste","symptom":"S1","estimated":false,"forced":false}
chain_0 = 892dacd795d4125b6da515995638d83b8fa642fd206438d121d44770aeae80d6
{"seq":0,"detector":"S1","rule":"s1.identical_rerun","confidence":0.95,"level":1,"evidence":[0],"waste_micro_krw":0,"primary":true,"suppressed":false,"estimate":false}
chain_1 = 33ac78bad2ea4e7765ca99e29234259973936f614272c34f8b055f014a270068 (head)
```

여기서 `9d3578cb…`는 `H("path", "src/a.py")`다. 구현은 이 예시로 직렬화와 사슬 계산을 먼저 맞춘다.

# Progress Contract v1

식별자: `progress-contract/1`

작업 계약은 사용자의 지시를 기계가 확인할 수 있는 완료 조건, 범위, 예산으로 고정한 파일이다. 진척은 이 계약에 대해서만 잰다. 이 문서는 계약 파일의 형식, 상태 전이, 완료 조건의 의미, 진척 등급을 정한다. 구현이 이 문서를 따르면 다른 도구가 만든 계약을 읽고 같은 진척 판정을 낼 수 있다.

## 1. 파일

- 위치: 프로젝트 루트의 `.samcheonpo/contract.yml`
- 인코딩: UTF-8 YAML. 알 수 없는 키가 있으면 계약 전체가 무효다(오타가 조건 누락으로 이어지지 않게 하기 위해서다).

```yaml
goal: 로그인 토큰이 만료되면 재로그인 화면으로 보낸다     # 필수, 한 문장
done:                                                     # 필수, 1개 이상
  - id: auth-tests                                        # 선택, 없으면 c1, c2 ... 순번
    check: "npm test -- auth"                             # 기계 검증 명령
    expect: exit0                                         # 선택, exit0만 정의됨
    isolation: worktree                                   # 선택, 작업 중 실행 허용 조건
    pure: false                                           # 선택, 부작용 없음 표시
    timeout_sec: 120                                      # 선택
  - manual: 만료된 토큰으로 접속하면 /login으로 이동한다  # 사람 확인 조건
scope:
  allow: ["src/auth/**"]                                  # 글롭, 비어 있으면 제한 없음
  protect: ["tests/**", ".env*"]                          # 실행 전 차단 대상
budget: {krw: 5000, minutes: 40, same_error_retries: 5}
forbid: ["테스트 수정", "새 의존성 추가"]                  # 자연어, 일부 키워드는 규칙으로 해석
explore: false                                            # 선택, 조사 작업 표시
```

### 1.1 필드 규칙

| 필드 | 규칙 |
| --- | --- |
| `goal` | 비어 있으면 무효 |
| `done[]` | 각 항목은 `check`와 `manual` 중 정확히 하나를 가진다. `id`는 계약 안에서 유일하다 |
| `done[].expect` | 생략 또는 `exit0`. 종료 코드 0이면 충족 |
| `done[].isolation` | 생략 또는 `worktree` |
| `scope.allow`, `scope.protect` | doublestar 글롭(`**` 포함). 경로는 프로젝트 루트 기준 상대 경로, 구분자는 `/` |
| `budget.*` | 0 이상의 정수. 0은 제한 없음 |
| `forbid[]` | `테스트` 또는 `test`를 포함한 항목은 테스트 파일 쓰기 금지로, `의존성`, `dependenc`, `라이브러리`를 포함한 항목은 의존성 파일 변경 금지로 해석한다 |

### 1.2 경로

- 절대 경로는 루트 기준 상대 경로로 바꾼다. 루트 밖 경로(`..`로 시작, 다른 절대 경로)는 언제나 범위 밖이다.
- `.samcheonpo/` 아래 경로는 언제나 범위 안이다.
- 임시 경로(`/tmp/`, `/var/tmp/`)는 범위 판정에서 제외한다.

## 2. 상태 전이

```text
draft ──accept──▶ accepted ──(계약 내용 변경)──▶ stale ──accept──▶ accepted
  │                  │
  └──skip──▶ skipped └──(모든 기계 조건 충족 후 새 작업)──▶ done
```

| 상태 | 의미 | 판정 기준 |
| --- | --- | --- |
| `draft` | 계약 파일이 있으나 사용자가 수락하지 않았다 | 범위와 예산은 첫 프롬프트 기준, 체크포인트 실행 안 함 |
| `accepted` | 사용자가 수락했다 | 범위, 예산, 완료 조건 모두 계약 기준 |
| `stale` | 수락 뒤 계약 내용(목표·완료 조건·범위·보호 경로·예산·금지 항목)이 바뀌었거나, 수락 기록의 서명이 맞지 않는다 | `draft`와 같음. 다시 수락해야 체크포인트가 돈다 |
| `skipped` | 사용자가 계약 없이 진행을 골랐다 | 추정 진척만 |
| `done` | 작업이 끝났다 | 다음 프롬프트는 새 작업 |

수락 기록은 `.samcheonpo/contract.state.json`에 둔다.

```json
{"state": "accepted", "checks_hash": "…", "authority_hash": "…", "file_hash": "…", "accepted_at": "2026-10-06T03:41:40Z", "side_effect": ["c2"], "mac": "…"}
```

`checks_hash`는 기계 검증 조건만으로 계산한다. 각 조건에 대해 `id + 0x00 + check + 0x00 + isolation + ("true"|"false")`를 만들고, 앞에 `"checks"`를 둔 목록을 0x00으로 이어 SHA-256을 구한 뒤 앞 16바이트를 소문자 16진수로 쓴다. `checks_hash`는 검사 결과를 다시 쓸 수 있는지 가리는 키다. 수락이 유효한지는 `authority_hash`로 판단한다. `authority_hash`는 계약 전체(목표, 사람 확인 조건 포함)의 정규 JSON으로 계산하므로, 어느 항목을 고쳐도 다시 수락해야 한다. `authority_hash`가 없는 이전 수락 기록도 다시 수락해야 한다.

`mac`은 사용자 홈의 `acceptance.key`로 계산한 HMAC-SHA256이다. 입력은 프로젝트 절대 경로, 0x00, 그리고 `mac`을 비운 수락 기록 JSON이다. 에이전트가 직접 쓰거나 다른 프로젝트에서 복사한 수락 기록은 서명이 맞지 않아 `stale`로 취급한다.

## 3. 완료 조건 실행

- 수락된 계약의 기계 검증 조건만 실행한다.
- 기본 실행 시점은 에이전트가 끝내려 할 때(Stop)와 사용자 요청이다. 작업 중간 실행은 `isolation: worktree` 또는 `pure: true`인 조건만 한다.
- `isolation: worktree` 조건은 현재 작업 폴더 상태(추적 파일의 변경과 무시되지 않은 새 파일)를 복사한 임시 작업 트리에서 실행한다.
- 실행 전후 작업 폴더 지문이 달라지면 그 조건을 `side_effect`로 기록하고, 다시 수락하기 전까지 자동 실행하지 않으며, 결과를 진척 판정에 쓰지 않는다.
- 각 실행은 새 프로세스 그룹에서 돌고, 시한을 넘기면 그룹 전체를 종료한다.

## 4. 진척 등급

진척 수치는 언제나 등급과 함께 표시한다. 등급 없는 진척 수치는 규격 위반이다.

| 등급 | 조건 |
| --- | --- |
| `verified` (검증) | 수락된 계약의 기계 검증 조건이 체크포인트에서 통과했다. 근거는 체크포인트 실행 기록 하나 이상이다 |
| `estimated` (추정) | 계약이 수락되지 않았거나 기계 조건이 없다. 범위 내 산출물의 순증가(파일별 최고 줄 수 갱신) 또는 같은 검증 명령의 결과 개선(실패 감소, 실패에서 통과로)이 있었다 |
| `unmeasured` (측정 불가) | 위 어느 것도 없다 |

## 5. 적합성

`conformance/`의 사례는 이 규격과 [Evidence Ledger v1](evidence-ledger-v1.md)의 적합성 시험이다. 구현은 `<구현 명령> <사례 입력>`으로 실행되어 Evidence Ledger v1 형식의 결과를 내야 하며, `samcheonpo-conformance --impl "<구현 명령>"`이 기대값과 대조한다.

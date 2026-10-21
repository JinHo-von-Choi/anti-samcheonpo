# opencode 1.18.35 지원 실측 (2026-10-22)

Linux x86-64, samcheonpo 0.9.3 설치본, 플러그인 무결성 일치, opencode 1.18.35, 모델 glm-5.3-flash-free.

## 확인한 것

- `samcheonpo doctor --agent opencode`: 15항목 모두 ok.
- 두 개의 실제 opencode 세션에서 훅이 불리고 도구 뒤 귀띔이 모델 문맥에 들어가는 것을 확인했다. `interventions --agent opencode` 집계 기준 최근 30일 게시 34건, 전달 27건 (s4.read_only_streak 9, s5.error_hiding 6, s5.test_weakening 2, s1.verify_ratio 1, s2.stuck_error 1, s7.memory_rot 1 등).
- 세션 2 (`ses_ea0a130e9ffeAEqn3yCK9cKnJO`): 실제 도구 뒤 귀띔 문맥에서 `[삼천포] 관찰:` 문장이 도구 결과 뒤에 붙어 모델에 전달된 것을 모델 응답에서 확인했다.
- 세션 1 (`ses_ea0a8787bffer3Lgx8va1651Os`): 모델에러 발생류(`exit 7`)는 `tool_schema_unknown`으로 판정 불가 사유에 남았다. 이는 트리거 선정이 규칙 분류 범위 밖이었을 뿐이고 opencode 전송 결함은 아니었다.

## 확인하지 못한 것

- 같은 실패한 검사 명령(`bash test-run.sh`, `echo FAIL; exit 1`)을 한 세션에서 세 번 동일하게 반복했지만 실행 전 차단은 발화하지 않았다(`interventions` 차단 0, 관찰 메시지 1회만 도달). 미발화 원인은 확인하지 않았다. 이 소스에서 opencode 실행 전 차단은 시험 수준이다.
- 실행 전 차단이 발화하지 않아 Stop 차단의 후속 프롬프트 전달도 실측하지 못했다.
- 세션이 관측 창에 남아 있어 종료 영수증은 발급받지 못했다 (`samcheonpo why`는 집계판만 확인).

## 남은 것

- 차단 미발화 원인 파악. Claude Code·Codex 같은 훅 경로에서는 같은 조건에서 발화한다.

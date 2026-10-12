# 탐지 개선 구현 검증 — 2026-10-12

이 문서는 작업 트리의 코드 동작을 검사한 결과다. 합성 사례의 통과를 실사용 정확도·절감률로 바꾸지 않는다. 기존 데몬·설정·외부 모델 연결은 변경하지 않았다.

## 수행 결과

- `go test ./...`: 통과. 최종 실행에서 모든 패키지 통과.
- `go test -race ./...`: 통과. 이후 추가한 실행 맥락/연결 입력 보정은 fp/detect/assessment/ledger/notify/cli/live의 관련 회귀를 `-race -count=1`로 다시 검사해 통과.
- `go vet ./...`: 최종 코드 통과.
- `gofmt -l internal cmd plugins`, `git diff --check`: 출력 없음.
- 적합성 실행: **24/24 통과**. 새 Claude/Codex 사례는 실행 래퍼 변경의 같은 실패를 찾고 정상 검사 선택 변경을 제외한다. 나머지는 기존 계약·봉인·외부 장애·실행 전 판단 사례다.
- 격리된 실제 프로세스 훅 검사: PreToolUse 200/200 응답, 도구 수신 200/200, 시간 초과 0, 실패 0, 큐 누락 0. 프로세스 기동 포함 p95 **8.85ms**, 처리 구간 p95 **2.48ms**로 6ms 회귀 게이트 통과. 콜드 스타트는 **42.52ms**로 별도 측정했다.
- 이벤트 10,000개를 접은 뒤 HUD 진단 반복 조회: **1,988ns/op**. Linux amd64, Xeon E5-2695 v4에서의 microbenchmark다. 전체 훅 시간이나 모든 환경의 성능을 보장하는 수치는 아니다.

재현 명령:

```sh
go test ./...
go test -race ./...
go vet ./...
go build -o /tmp/anti-samcheonpo-implementation-check ./cmd/samcheonpo
go run ./cmd/samcheonpo-conformance \
  --impl '/tmp/anti-samcheonpo-implementation-check spec run' \
  --task-impl '/tmp/anti-samcheonpo-implementation-check spec task'
sh scripts/check-hook-budget.sh --p95-max 0 --handling-p95-max 6ms
go test ./internal/live -run '^$' -bench '^BenchmarkHUDAssessmentPolling$' -benchtime=1s -count=1
```

## 기존 0% 사례의 설명

기존 Claude 원장의 세션 하나를 새 바이너리의 `why --format json`으로 읽기 전용 조회했다. 원장 버전은 조회 전후 모두 **10**이었다. 원문 대화나 원래 세션 ID는 이 보고서에 포함하지 않았다.

- 표시 비율: `0%`, 금액 기준.
- 기록된 판정: 2건.
- 분류 사건: 0건. 판정의 비용 귀속은 `unattributed_findings`.
- 관측된 전경 셸: 125건. 종료 결과 98건, 결과 지문 98건, 작업 폴더 지문 105건.
- 종료 결과 누락: 27건. 작업 폴더 지문 누락: 20건.
- 완료 근거와 전체 수집률: 미확인. 기록된 수락 계약 없음.

`0%`가 정상이나 완료라는 증거가 아니라는 것을 실제 기존 데이터에서 설명할 수 있게 됐다. 이 세션의 판정이 올바른 낭비 탐지였는지, 누락된 관측에 실제 낭비가 있었는지는 독립 사람 라벨 없이는 판정하지 않는다. 집계 JSON은 [읽기 전용 진단 결과](2026-10-12-readonly-diagnostic.json)에 있다.

## 제품 효과 인수와 남은 범위

독립 라벨 코퍼스, 미상/오탐/정상 재시도 대조군, 두 에이전트의 실제 안내 전달·관련 회복, 사전 등록 A/B 완료율과 감시 포함 총비용은 아직 별도 검증이 필요하다. 세션 간 후보 자동 안내, 주장–요구사항–검사 결과 연결, 실행 규칙 설계·독립 평가·승격은 후속 [2차 구현 검증](2026-10-12-phase2-validation.md)에서 다룬다. 이 문서의 수치는 1차 구현 검증 기록이다.

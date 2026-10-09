# 적합성 시험

[Progress Contract v1](../docs/spec/progress-contract-v1.md)과 [Evidence Ledger v1](../docs/spec/evidence-ledger-v1.md) 구현의 적합성 사례다. 사례마다 `case.json`에 입력 기록, 에이전트, 계약 파일, 기대값(울려야 하는 판정, 울리면 안 되는 판정, 계약 상태, 진척 등급, 분류별 토큰)을 둔다. `same_verdicts_as`가 있는 사례는 다른 에이전트 형식으로 같은 상황을 기록한 것이며 판정 집합이 같아야 한다.

```bash
go build -o samcheonpo ./cmd/samcheonpo
go build -o samcheonpo-conformance ./cmd/samcheonpo-conformance
./samcheonpo-conformance --impl "./samcheonpo spec run" --cases conformance/cases
# v1 10건 + v2 10건
./samcheonpo-conformance --impl "./samcheonpo spec run" --task-impl "./samcheonpo spec task"
```

실행기는 표준 라이브러리만 쓰고 구현을 명령으로만 호출한다. 다른 구현은 `<명령> <입력> [--agent A] [--contract 파일]`로 같은 JSON을 내면 된다. 실행기는 근거 사슬을 직접 다시 계산하고, 봉인의 머리 값과 행 수, 행의 토큰 합계를 대조한다.

v2는 `conformance/v2/task-input.json`의 구조화된 의도/검증 근거를 별도 `spec task` 경계로 전달한다. 고정 기대값은 구현의 seal 함수를 호출하지 않는 SHA256 계산으로 대조했다. 정규 출력·기존 봉인 검증, 미래 버전·도구 출처·개정 누락·입력 지문 누락·중복 근거·모순된 통과·변조 목표·알 수 없는 필드 거부를 검사한다. 셸 검사 실행이나 사용자 승인 전이는 하지 않는다. 봉인 해시는 서명이나 사용자 출처 진위 인증이 아니다.

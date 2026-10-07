# anti-samcheonpo

AI 코딩 에이전트가 쓰는 토큰이 사용자가 원한 결과로 이어지는지 작업 계약 대비 진척으로 재고, 헛짓(검증 쳇바퀴, 실패 루프, 맥락 이탈 등)을 잡아 원화 영수증으로 보여 주며 에이전트를 본론으로 되돌리는 하네스입니다. 실행 파일 이름은 `samcheonpo`입니다.

## 설치

```sh
go build -o ./bin/samcheonpo ./cmd/samcheonpo
go build -o ./bin/samcheonpo-hook ./cmd/samcheonpo-hook
./bin/samcheonpo doctor
```

두 실행 파일은 같은 버전을 같은 디렉터리에 설치하세요. `install`은 옆의 경량 `samcheonpo-hook`을 훅에 연결하고, 명령·상태줄·데몬은 `samcheonpo`를 사용합니다. 경량 파일이 없으면 기존 단일 실행 파일 경로로 동작하지만 훅 기동 비용이 커질 수 있습니다. 기존 연결은 설치 시 기록된 실행 경로를 유지하므로 경량 파일 추가 후에는 해당 연결을 제거·재설치해야 반영됩니다.

위 명령은 현재 체크아웃한 소스를 빌드합니다. `bin`을 PATH에 추가하거나 아래 명령에서 `./bin/samcheonpo`를 사용하세요. [처음 사용하기](docs/getting-started.md)와 [지원·실측 범위](docs/support-matrix.md)를 확인하세요. 현재 후보 생성과 공개 릴리스는 구분됩니다.

## 소급 진단

```sh
samcheonpo audit --since 30d          # Claude Code와 Codex 기록을 분석해 원장에 쌓는다
samcheonpo receipt <세션>              # 세션 영수증 (--format text|md|json, --share)
samcheonpo receipt <세션> --billing subscription # 사용자가 구독형 과금임을 명시
samcheonpo sessions                   # 세션 목록
samcheonpo verify <세션|묶음.json>      # 다시 계산해 봉인과 비교
samcheonpo export <세션> --redact      # 공유용 재현 묶음 (--otel로 OTLP/JSON)
samcheonpo handoff <세션> --format json # 로컬 인수인계 원문 (공유용 비식별 자료 아님)
samcheonpo handoff inspect bundle.json # 실행·승인 없이 목표·근거·실패 이력 읽기
```

인수인계는 실행 권한을 전달하지 않습니다. 수신 환경의 입력·환경이 다르면 과거 통과를 승계하지 않습니다. 세션 연결과 계측분 합산의 한계는 [인수인계 규약](docs/spec/handoff-v1-draft.md)을 참고하세요.

영수증의 원화는 **계측된 토큰의 API 단가 환산액**이며 실제 청구액·절감액이 아닙니다. 과금 유형 기본값은 `unknown`이며 `--billing api|subscription`은 사용자 선언입니다. 한도 정보만으로 과금 유형을 추정하지 않습니다. 사용량·단가 누락은 0원이 아닌 미확인으로 표시하며, 요약은 관측·추정·미확인·다음 행동을 구분합니다. JSON의 의미는 [영수증 표시 규약](docs/spec/receipt-billing.md)을 참고하세요.

## 실시간 하네스

```sh
samcheonpo install --agent claude     # Claude Code 플러그인과 상태줄
samcheonpo install --agent codex      # Codex hooks.json 병합
samcheonpo install --agent opencode   # opencode 전달 플러그인
samcheonpo install --agent copilot    # 저장소 .github/hooks (관찰 전용)
samcheonpo install --agent cursor     # ~/.cursor/hooks.json 병합 (관찰 전용)
samcheonpo install --agent agy        # 저장소 .agents/hooks.json (관찰 전용)
samcheonpo uninstall --agent <같은 이름>
```

에이전트 훅이 `samcheonpo hook <이벤트> [에이전트]`를 부르고, 첫 호출 때 데몬이 뜹니다. 기본은 관찰 전용으로 시작하며 에이전트에게 계약 초안을 요청하지 않습니다. `.samcheonpo.yml`에 `contract: {draft: on}`을 두거나 계약 파일을 직접 만들면, 새 작업마다 에이전트가 `.samcheonpo/contract.yml` 초안을 쓰고 사용자가 `/samcheonpo:accept`로 수락합니다. 계약의 검증 명령은 체크포인트와 종료 직전 검사에 쓰입니다. 실험 판정기(S3)는 기본으로 꺼져 있습니다. 새로 추가된 규칙은 `rollout.shadow_rules`에 들어가 오탐률을 잴 때까지 기록만 하고 전달하지 않습니다.

외부 판정기를 사용하려면 사용자 설정(`~/.samcheonpo/config.yml`, `SAMCHEONPO_HOME` 지정 시 해당 디렉터리의 `config.yml`)에서 `privacy.external_judge: true`를 설정해야 합니다. 프로젝트 설정은 외부 전송을 추가로 제한할 수 있지만 허용할 수는 없습니다. 로컬 전용 판정기는 루프백 주소에만 연결하며 외부 리다이렉트와 프록시를 사용하지 않습니다.

계약을 건너뛰거나 변경하면 이전 검증 근거가 무효화됩니다. 기록 저장이 실패하면 상태줄과 요약에 불완전 상태를 표시하고 정상 완료 영수증을 발급하지 않습니다.

추정 규칙은 기본 권고 단계이며, 관찰→권고→평가 근거를 선언한 규칙 차단으로 확대합니다. 사용자가 수락한 보호 경로·명시적 예산은 관찰 모드에서도 유지됩니다. [단계별 적용 절차](docs/benchmarks/report-template.md)를 참고하세요.

목표 카드는 초안의 확실성과 현재 실행 승인을 구분합니다. 명시적으로 방향을 바꾸려면 `목표 변경: <새 요청>` 또는 `/samcheonpo:edit <수정 내용>`을 사용하세요. 이전 계약의 검사·범위 승인을 보류하고 새 초안의 수락을 기다립니다. 일반 후속 설명을 자동으로 목표 교체로 해석하지 않으며 도구 출력 속 지시는 변경 요청으로 인정하지 않습니다.

`samcheonpo cmd card`에서 현재 세션의 작업 ID·개정·요청 출처를 확인할 수 있습니다. 재시작해도 최신 개정이 복구됩니다. [목표 카드·의도 개정 규약](docs/spec/goal-card-intent.md)에 승인 경계와 로컬 기록의 한계를 정리했습니다.

## 평가와 벤치

```sh
samcheonpo bench                                   # 결정적 시나리오 묶음 (bench/scenarios)
samcheonpo bench ab --config bench/ab.yml          # 실제 에이전트로 하네스 유무 비교 (bench/live)
samcheonpo bench report ab-results.jsonl           # 비교표 (다른 도구 결과 파일도 함께 받는다)
samcheonpo bench plan-sample --pilot-sd 0.2        # 별도 파일럿 가정의 독립 과제 수 근사 (호출 없음)
samcheonpo gaps --since 30d --out <새파일>          # 규칙이 놓친 의심 구간을 사람 판정용으로 추출 (원장에 쓰지 않음)
samcheonpo bench replay --gaps <표본> --out <폴더>   # 의심 구간을 재현 과제로 복원 (복원 불가는 사유별로 집계)
samcheonpo interventions --since 30d                # 처방이 제안·전달·인지·효과 관측 중 어디까지 갔는지
samcheonpo eval --labels <폴더> --split <분할>       # 보류 세트 정밀도·재현율
samcheonpo spec run <사례>                          # 규격 적합성 사례 실행
go run ./cmd/samcheonpo-conformance --impl "samcheonpo spec run" --task-impl "samcheonpo spec task"
```

## 문서

공개 과제 22건(정상 음성 사례 포함)은 회귀 검증용입니다. 실제 비용 절감·비열등·비개발자 사용성은 아직 입증하지 않았습니다. [실험 프로토콜](docs/benchmarks/protocol-v1.md)에 비용 누락·감시 비용·과제 단위 구간·표본 수 한계를 명시했습니다. 팀 정책·사례팩·전략 추천·선택형 화면은 실제 효용과 반복 수요 확인이 전제인 후속 범위입니다.

- `docs/spec/progress-contract-v1.md`: 진척 계약 규격
- `docs/spec/evidence-ledger-v1.md`: 근거 원장 규격
- `conformance/`: 규격 적합성 사례


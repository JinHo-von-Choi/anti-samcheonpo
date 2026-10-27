# 개발·평가·릴리즈

사용 방법은 [README](../README.md)와 [사용 가이드](getting-started.md)에 있습니다. 이 문서는 코드를 수정하거나 효과를 평가할 때 사용합니다.

## 빌드와 검사

Go 1.27 이상이 필요합니다. 아래 개발·평가 명령은 이 Git 저장소를 내려받은 뒤 저장소 루트에서 실행합니다.

```sh
go build -o bin/samcheonpo ./cmd/samcheonpo
go build -o bin/samcheonpo-hook ./cmd/samcheonpo-hook
go test ./...
go vet ./...
```

변경한 기능의 관련 검사를 먼저 실행하세요. 릴리즈는 CI의 전체 검사·적합성·훅 지연 검사와 각 플랫폼의 패키지 검사를 통과해야 합니다. 모델을 호출하는 실험은 자동 검사와 별도로 실행합니다.

## 효과 검증의 현재 결론

**Codex의 완료율·비용 개선은 미입증입니다.** 2026-10-21 공개 과제 4개×4군 실험에서 기본 Codex는 2/4회, 감시·정적 지침·기본 정책 군은 각각 4/4회 통과했습니다. 기본 군의 시간 초과 비용 누락, 환경 변동, 전달 확인 경고 0건 때문에 정책 효과를 확정하지 못했습니다.

- [실험 결과와 한계](benchmarks/2026-10-21-codex-pilot.md)
- [계측 수정과 구현 검사](benchmarks/2026-10-21-codex-implementation.md)
- [사람 검토 절차](benchmarks/codex-human-review-protocol.md)

## 평가 도구

| 도구 | 하는 일 |
| --- | --- |
| `samcheonpo bench` | 내장 기록 사례 검사 |
| `samcheonpo bench ab` | 실제 에이전트를 실행해 독립 채점·사용량 수집 |
| `samcheonpo bench report` | 군별 결과·완료당 비용과 근거 부족 사유 출력 |
| `samcheonpo bench probe` | 후보 규칙의 결과·효율·증거 충분성 구분 |
| `samcheonpo eval prepare-review` | 원본에 연결된 사람 검토 큐 생성 |
| `samcheonpo eval compare` | 실제 검토 답변이 있는 공통 구간 평가 |

각 명령의 인자는 `--help`로 확인합니다. 실제 실행 설정 예시는 [bench/ab.example.yml](../bench/ab.example.yml)에 있습니다. `bench ab`와 아래 파일럿은 모델 사용량을 발생시킬 수 있습니다.

```sh
# --run 없이는 실행 계획만 생성
python3 scripts/codex_pilot.py --binary /absolute/path/to/samcheonpo
# 명시적으로 실제 4군 시행을 실행
python3 scripts/codex_pilot.py --binary /absolute/path/to/samcheonpo --run
```

사람 답변 없이 정확도를 만들거나, 공개·생성 과제를 비공개 검증 자료라고 부르지 않습니다. 비용 누락·조건 불일치·표본 부족은 효과 판정을 보류합니다. 상세 기준은 [실험 프로토콜](benchmarks/protocol-v1.md)과 [탐지 평가 규격](spec/detection-assessment.md)에 있습니다.

## 릴리즈 절차

1. 변경 이력과 한국어·영문 설치 버전을 갱신합니다. `internal/cli/cli.go`와 `internal/hookclient/executable.go`의 버전도 일치시킵니다.
2. 변경을 커밋하고 CI를 확인합니다. 규칙을 바꿨다면 `scripts/replay-diff.sh [이전 태그] [기간]`으로 지난 세션 기록에서 규칙별 경고 수가 어떻게 달라지는지 확인합니다. 두 빌드를 임시 폴더에서 차례로 돌리므로, 진행 중인 세션이 있으면 몇 건의 차이는 실행 사이에 늘어난 기록 때문일 수 있습니다.
3. `release-candidate` 워크플로를 해당 커밋에서 실행합니다. 여섯 플랫폼의 빌드·설치·제거·기록 분석·첫 훅 검사를 통과한 압축 파일만 사용합니다.
4. artifact의 체크섬을 확인하고 전체 `SHA256SUMS`를 합칩니다. 태그가 검사한 커밋을 가리키는지 확인한 뒤 GitHub Release에 게시합니다.
5. 공개 릴리즈의 태그·파일 6개·체크섬을 확인합니다.

로컬 플랫폼의 후보만 만들 때:

```sh
sh scripts/package-release.sh 0.9.3 ./release-out
```

출력 폴더가 이미 있으면 덮어쓰지 않습니다. 로컬 후보 생성과 `release-candidate` 워크플로는 게시를 수행하지 않습니다. 원본·개인 원장·인증 자료·무시된 작업 문서는 배포하지 않습니다. 문서의 내부 설계 초안은 현재 사용 계약과 구분합니다.

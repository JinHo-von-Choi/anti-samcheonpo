# 개발·평가·릴리즈

저장소를 내려받은 뒤 루트 폴더에서 실행합니다. 사용자 안내는 [사용 가이드](getting-started.md)에 있습니다.

## 빌드와 검사

Go 1.27 이상이 필요합니다.

```sh
go build -o bin/samcheonpo ./cmd/samcheonpo
go build -o bin/samcheonpo-hook ./cmd/samcheonpo-hook
go test ./...
go vet ./...
```

Unix 배포용 훅 전송기는 Rust로 빌드합니다.

```sh
cargo fmt --all -- --check
cargo clippy --workspace --all-targets --locked -- -D warnings
cargo test --workspace --locked
```

훅 전송 규격과 처리 시간은 다음 스크립트로 확인합니다.

```sh
sh scripts/check-hook-contract.sh
sh scripts/check-hook-budget.sh --p95-max 0 --handling-p95-max 6ms
```

변경한 기능의 관련 검사를 먼저 실행하고, 릴리즈 전에는 CI의 전체 검사와 플랫폼별 패키지 검사를 확인합니다.

## Deep Agents 검사

Python 3.14 가상환경에서 SDK 의존성을 설치하고, 빌드한 데몬의 경로를 지정합니다.

```sh
python3 -m venv .venv-sdk
.venv-sdk/bin/python -m pip install -r plugins/deepagents/requirements-test.txt
SAMCHEONPO_TEST_BIN="$PWD/bin/samcheonpo" \
  .venv-sdk/bin/python -m pytest -q -p no:cacheprovider plugins/deepagents/test_sdk.py
```

로컬 모델과 격리한 데몬으로 도구 차단, 종료 후 재개, 병렬 하위 에이전트, 예산과 사용량 기록을 검사합니다.

## 호스트 연결 검사

고정된 훅 입력과 수집 스크립트는 공급자 API 없이 검사할 수 있습니다.

```sh
python3 -m unittest discover -s scripts -p test_probe_native_hooks.py
python3 -m unittest discover -s scripts -p test_review_leftover_records.py
```

설치된 CLI의 동작을 수집하려면 `probe_native_hooks.py`를 사용합니다. 이 명령은 계정에 설정된 모델을 호출합니다.

```sh
python3 scripts/probe_native_hooks.py run \
  --agent agy --scenario background --out /tmp/samcheonpo-probe
```

`basic`, `background`, `interrupt` 시나리오를 사용할 수 있습니다. Claude Code와 Codex에는 `subagent` 시나리오도 있습니다. 공유할 입력은 `export`로 식별자와 MCP 내용을 제거한 뒤 확인합니다.

## 평가 도구

| 명령 | 용도 |
| --- | --- |
| `samcheonpo bench` | 내장 기록 사례 재생 |
| `samcheonpo bench ab` | 에이전트 실행과 독립 채점 |
| `samcheonpo bench report` | 군별 결과와 완료당 비용 집계 |
| `samcheonpo bench probe` | 후보 규칙별 결과 비교 |
| `samcheonpo eval prepare-review` | 사용자 검토용 표본 추출 |
| `samcheonpo eval compare` | 검토 답변에 따른 탐지 결과 비교 |

실행 인자는 각 명령의 `--help`로 확인합니다. 모델 비교 실행의 설정 예시는 [bench/ab.example.yml](../bench/ab.example.yml)에 있습니다. `bench ab`는 모델 사용량을 발생시킵니다.

평가할 때는 같은 과제와 실행 조건으로 비교하고, 과제 채점·사용량·경고 뒤 행동을 함께 기록합니다. 검토 자료는 `eval prepare-review`로 만들고, 답변을 모은 뒤 `eval compare`로 비교합니다. 세부 옵션은 각 명령의 `--help`를 확인하세요.

## 릴리즈 절차

1. 변경 이력과 한국어·영문 설치 예시의 버전을 갱신합니다. `internal/cli/cli.go`와 `internal/hookclient/executable.go`의 버전도 맞춥니다.
2. 변경을 커밋하고 CI를 확인합니다. 탐지 규칙을 바꿨다면 `scripts/replay-diff.sh [이전 태그] [기간]`으로 기존 기록의 판정 차이를 확인합니다.
3. `release-candidate` 워크플로에서 플랫폼별 빌드·설치·제거·기록 분석·첫 훅 검사를 실행합니다.
4. 생성한 압축 파일과 체크섬을 확인하고, 검사한 커밋에 태그를 붙여 GitHub Release에 게시합니다.
5. 게시한 태그·플랫폼 파일·체크섬을 확인합니다.

현재 플랫폼의 패키지만 만들 때는 버전과 새 출력 폴더를 지정합니다.

```sh
sh scripts/package-release.sh 0.11.1 ./release-out
```

패키지에는 실행 파일과 배포용 자료를 담습니다. 개인 원장과 인증 자료는 작업 환경에 보관합니다.

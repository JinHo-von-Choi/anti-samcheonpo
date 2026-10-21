# Cursor 입력 처리와 연결 검증

## 구현 범위

현재 소스는 Cursor의 범용 도구 훅을 받아 실제 호출 ID·명시적 종료 코드·실패 유형을 기록한다. 같은 명령의 다른 호출은 분리하고, 같은 호출의 신구 훅·재전송·늦은 결과는 한 사건으로 결합한다. `exitCode`가 없으면 성공을 추정하지 않는다.

파일의 수정 전 내용은 실제 실행 전 훅에서만 보존한다. 사후 편집 알림은 결과만 기록한다. 입력 없는 실패 훅은 같은 호출 ID의 이전 입력을 이용하며, 이전 입력도 없으면 형식 불완전을 표시한다. 권한 거부·시간 초과·사용자 중단은 정상 프로세스 종료와 구분된다.

설치는 범용 도구 훅을 포함한 8개 이벤트를 등록한다. 재설치 시 이전 설치 기록과 정확히 일치하는 항목만 교체하며, 사용자가 수정한 훅과 다른 설정은 보존한다. `doctor --agent cursor`는 설치 기록, 필수 훅, 실행 파일과 마지막 수신 기록을 구분해서 표시한다.

**실제 도구 차단·모델 안내·종료 후속 실행은 미검증이며 비활성 상태다.** 입력 캡처가 있는 날짜+빌드 문자열만 시험 버전으로 표시한다. 토큰·비용, IDE·Cloud Agent·다른 운영체제의 지원은 확인하지 않았다.

## 근거 수준

| 검사 | 근거 | 결과 |
| --- | --- | --- |
| 성공 셸·읽기 입력 | 2026-10-21 실제 CLI 입력 10개 | 현재 변환과 필수 형식 검사 통과, native ID와 종료 코드 0 보존 |
| 실패 7·미상·시간 초과·거부·중단 | 합성 입력 + 임시 SQLite | 종료 상태·실패 유형 저장, 미상은 null |
| 중복·역순·늦은 결과·동시 요청 | 합성 입력 + 원장·pairing 대조 | 다른 호출 2건 유지, 중복 결과로 건수 증가 없음 |
| 파일 변경 | 임시 실제 파일 + 합성 훅 | 실행 전 원본 보존, 사후 입력만으로 baseline 생성 안 함 |
| 장애 대응 | 데몬 없는 연결·잘못된 응답·지연 소켓 | 권한 훅에 유효한 허용 응답, 입력 형식 오류 진단 |
| 설치·재설치·제거 | 새 빌드 + 격리 설정 | 관리 훅 8개, 사용자 설정 보존, 설치 후 미수신 상태 경고 |
| 실제 Cursor 새 빌드 연결 | CLI `2026.10.19-6bbf2c4` | sessionStart·sessionEnd(error) 2건 전달. 모델 실행 불가로 도구 호출 없음 |

실제 Cursor 실행은 `Named models unavailable` 오류로 종료 코드 1을 반환했다. 시험용 모델·인증·권한 설정은 변경하지 않았다. 이 결과는 생명주기 연결 확인이며, 도구 실행·개입 성공으로 세지 않는다. [실행 상태](../../bench/cursor/2026-10-22/live-status.json)

회귀 입력의 출처와 익명화 규칙은 [fixture 안내](../../testdata/hooks/cursor/README.md), 이전 결함의 재현은 [기초 실측](2026-10-21-cursor-support.md)에 있다. 실패 종료 7과 파일 편집의 실제 Cursor 캡처는 여전히 필요하다.

## 전달 경로 지연

Linux x86-64에서 새 `samcheonpo`·`samcheonpo-hook`을 임시 디렉토리에 빌드해 사용했다. 합성 Cursor 입력을 경량 클라이언트 → 로컬 소켓 → 데몬 → SQLite 순서로 처리했다. 실제 셸 명령이나 모델은 실행하지 않았다.

| 조건 | 표본 | p50 | p95 | 시간 초과 / 응답 누락 |
| --- | --- | --- | --- | --- |
| warm 실행 전 훅 | 100 | 2.82ms | 4.73ms | 0 / 0 |
| warm 실행 후 훅 | 100 | 2.78ms | 5.71ms | 0 / 0 |
| cold 세션 시작 | 10 | 49.22ms | 59.22ms | 0 / 0 |

측정값은 경량 클라이언트의 `handling_ns`다. 클라이언트 자체의 프로세스 기동 시간과 모델 시간은 제외한다. cold 값에는 데몬 기동·연결 대기가 포함되므로 warm p95 10ms 목표와 분리한다. 실행 전 판정의 15ms 기한과 별도로 cold 연결 대기에는 기존 500ms 상한이 적용된다.

합성 실행 100건 모두 원장에 한 번씩 저장됐고, 명시적으로 전달한 종료 코드 7도 100건 일치했다. 이 측정으로 비용 절감이나 실제 Cursor 개입 효과를 주장하지 않는다. [측정 원문](../../bench/cursor/2026-10-22/transport.json), [설치 결과](../../bench/cursor/2026-10-22/install.json)

## 검사와 재현

```sh
go test ./... -count=1 -timeout=5m
go test -race ./internal/live ./internal/hookclient -run '^TestCursor' -count=1 -timeout=180s
```

[전체 시험 결과](../../bench/cursor/2026-10-22/all-tests.log), [경쟁 조건 검사](../../bench/cursor/2026-10-22/race.log).

설치 형식의 `version=1` 검사와 실행 권한 진단을 추가한 뒤 설치·CLI의 Cursor 관련 시험도 통과했다. 잘못된 버전은 사용자 파일과 설치 기록을 바꾸지 않고 거부한다. [설치 검증](../../bench/cursor/2026-10-22/install-validation.log)

두 실행 파일을 같은 임시 폴더에 빌드한 뒤 모델 없이 전달 경로를 검사한다.

```sh
go build -o /tmp/cursor-build/samcheonpo ./cmd/samcheonpo
go build -o /tmp/cursor-build/samcheonpo-hook ./cmd/samcheonpo-hook
python3 scripts/probe_cursor_transport.py \
  --binary /tmp/cursor-build/samcheonpo --samples 100 --cold 10 \
  --output /tmp/cursor-transport.json
```

실제 Cursor 도구 호출을 다시 수집하려면 사용 가능한 모델 환경에서 다음을 실행한다.

```sh
python3 scripts/probe_cursor_hooks.py --mode bridge \
  --binary /tmp/cursor-build/samcheonpo --timeout 120
```

시험 자료는 별도 임시 폴더에 저장된다. 입력 원문은 개인 정보나 도구 인자를 포함할 수 있으므로 공개 fixture로 옮길 때 익명화한다. `--prepare-only`는 프로젝트와 훅만 만들며 모델을 호출하지 않는다. `--mode host`의 결정론적 거부·안내는 호스트 프로토콜 검사이며 제품 탐지 성공과 구분한다.

## 남은 확인

1. 실제 실패 종료·파일 편집·MCP 실패 입력 수집.
2. 실제 거부 뒤 미실행, 모델의 안내 수신, 종료 후속 실행과 취소 존중 확인.
3. 확인한 경로의 capability만 개별 활성화.
4. 사용량·효과 및 IDE·Cloud Agent·다른 OS의 별도 평가.

현재 코드는 배포된 v0.9.3에 포함되지 않는다.

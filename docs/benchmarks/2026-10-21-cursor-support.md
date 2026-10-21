# Cursor 지원 실측

## 판정

**v0.9.3은 Cursor에 대한 신뢰할 수 있는 관찰·개입 지원을 아직 제공하지 않는다.** 설치·제거와 일부 훅 변환은 동작하지만, 범용 도구 사건과 명시적 종료 코드가 빠진다. 실행 전 차단·결과 뒤 안내·종료 보류는 현재 프로필에서 꺼져 있다.

Cursor 자체에는 도구 실행 전 허용/거부와 실행 후 문맥 전달 인터페이스가 있다. 제품의 미지원 상태와 호스트의 기능 부재를 구분해야 한다. [Cursor 공식 Hooks 문서](https://cursor.com/docs/agent/hooks#pretooluse)

## 조건과 자료

- 기준 코드: `29975eafb6a19678a0c98b39282346c1a89e5fec`, 설치판 `samcheonpo version 0.9.3`.
- 호스트: Linux x86-64, `cursor-agent 2026.10.19-6bbf2c4`.
- 실제 Cursor 실행: 2026-10-21 23:32–23:35 KST의 프로젝트 훅 수집 두 건. 사용자 홈 인증과 기존 지시·도구 환경을 사용한 CLI 실행이며, IDE·Cloud Agent 측정은 아니다.
- 수집용 훅은 입력을 저장하고 허용을 반환했다. 삼천포를 연결한 실시간 실행은 아니므로, 이 자료만으로 삼천포의 차단·안내 전달을 입증할 수 없다.
- 원본에는 사용자 식별 정보가 있어, 관계를 유지한 식별자·경로 치환과 이메일 제거를 적용한 10개 입력만 [측정 자료](../../bench/cursor/2026-10-21/manifest.json)에 보존했다. 생각 내용·MCP 응답 본문은 포함하지 않았다.

| 실제 실행 | 남아 있는 훅 입력 | 확인한 결과 | 한계 |
| --- | --- | --- | --- |
| `python3 hello.py` | 10개: sessionStart/End 각 1, pre/postToolUse 각 1, before/afterShellExecution 각 1, afterAgentThought 4 | 셸 출력 `hi\n`, 범용 결과의 `exitCode=0`, 동일 호출의 네 도구 훅에 같은 `tool_use_id` | 연결 재시도 발생, 출력은 `hihihi`; 기록상 셸 실행은 1건. stop·beforeSubmitPrompt 미수집 |
| `a.py` 편집 후 실행 요청 | 16개: sessionStart/End 각 1, preToolUse 5, postToolUse 4, beforeReadFile 2, afterAgentThought 3 | 파일 읽기와 범용 도구 사건 수집, sessionEnd의 `final_status=error` | `ActionRequiredError: You've hit your usage limit`, CLI 종료 1. 파일은 `x=1` 그대로이며 afterFileEdit·차단·후속 안내 미검증 |

`stop`이나 `beforeSubmitPrompt`가 수집되지 않은 이유는 확정하지 않았다. 단 두 실행으로 해당 이벤트가 CLI에서 지원되지 않는다고 판단하지 않는다. 두 번째 실행의 모델 사용 제한은 측정 중단 원인이며 삼천포 오류로 분류하지 않는다.

## 코드 재생 결과

실제 수집 입력을 현재 `translateIn`에 넣고, 셸 전용 전후 훅은 새 Cursor 세션의 핸들러와 임시 SQLite 원장까지 통과시켰다. 운영 원장은 사용하지 않았다. 별도 표시한 호출 ID 변형과 장애 조건은 합성 검사다.

| 항목 | 결과 | 근거 |
| --- | --- | --- |
| 범용 사건 | 보존한 preToolUse 2건·postToolUse 2건 모두 변환 결과 `[]` | 설치 목록 9종과 `translateIn`에 범용 훅 없음 |
| 셸 종료 코드 | `afterShellExecution`에는 `exit_code`가 없고, `postToolUse.tool_output`의 JSON 문자열에는 `exitCode=0` 존재 | 후자는 현재 무시됨 |
| 미상 결과의 저장 | 셸 전용 훅만 재생하면 원장에 셸 1건·`exit_code=0` 저장 | 일반 파서가 오류 표시 없는 결과를 성공으로 간주. 이 샘플 자체는 성공했지만, 사용한 입력에는 이를 확정할 종료 코드가 없음 |
| 호출 식별자 | 실제 ID 대신 대화·generation·명령·파일의 해시 사용 | 같은 입력에서 native ID만 바꾼 **합성 두 번째 호출**도 같은 내부 ID. 반복 호출 충돌 위험 재현 |
| 결과 뒤 안내 | `additionalContext`가 Cursor 출력으로 변환되지 않음 | `translateOut(cursor, postToolUse, …)` 출력 없음. 실제 모델 수신 검사 아님 |
| 장애 시 허용 | beforeShellExecution은 `permission=allow`, preToolUse는 빈 출력 | 데몬 없는 임시 홈에서 경량 클라이언트 호출. 범용 훅을 등록할 때 함께 고쳐야 하는 잠재 결함 |
| 개입 프로필 | `tested=false`, block_pre/inject_post/block_stop 모두 false | `adapter.Resolve` 실호출 |

[재생 로그](../../bench/cursor/2026-10-21/replay.log), [재생 코드](../../bench/cursor/2026-10-21/replay_test.go.txt).

기록의 종료 코드 0을 잘못된 실패 판정으로 단정하지 않는다. 재현된 문제는 **명시 근거 없는 성공 확정**이다. 실제 비정상 종료·시간 초과·권한 거부 캡처는 추가로 필요하다.

## 설치·진단·기존 시험

- `samcheonpo install --agent cursor --hooks-file <임시 경로>`와 제거를 실제 설치판으로 실행했다. 9개 항목 추가, 종료 코드 각각 0, 기존 `stop` 훅과 최상위 사용자 키 보존을 확인했다. `HOME` 변경 없이 `SAMCHEONPO_HOME`과 훅 경로만 격리했다. [결과](../../bench/cursor/2026-10-21/install.json)
- 사용자 `~/.cursor/hooks.json`은 없다. 이 상태에서도 `doctor --agent cursor`는 설치 누락 오류 없이 관찰 전용 경고와 비활성 capabilities를 출력한다. `internal/cli/doctor.go`에는 Cursor 설치 상태·등록 이벤트·최근 수신 여부를 확인하는 분기가 없다. 데몬 응답이나 실행 파일 존재는 Cursor 연결 증거가 아니다.
- 기존 `TestCursorTranslation`, 설치·제거 보존, 데몬 장애 허용, 종료 대기, 버전 탐색 관련 검사는 통과했다. [Cursor 변환 시험](../../bench/cursor/2026-10-21/cursor-translation-test.log), [관련 시험](../../bench/cursor/2026-10-21/related-tests.log)
- 기존 Cursor 변환 시험은 문서 기반 합성 입력에 `exit_code=1`을 직접 넣는다. 이번 실캡처의 범용 결과 형식과 다르므로 이 시험의 통과만으로 실제 지원을 판정할 수 없다.
- 앞선 `go test ./internal/live ./internal/install ./internal/hookclient -count=1 -timeout 60s`에서는 live 패키지가 60초 시간 초과로 실패했다. 파이프 뒤 `tail`의 종료 코드 0은 시험 성공이 아니다. 이번 검사는 관련 항목으로 한정했으며, 전체 live 패키지 통과 여부와 이전 시간 초과의 원인은 미확정이다.

## 재현

저장소 루트에서 Go와 Python 3로 실행한다.

```sh
python3 bench/cursor/2026-10-21/run-replay.py
go test ./internal/live -run '^TestCursorTranslation$' -count=1 -timeout=120s -v
go test ./internal/live ./internal/install ./internal/hookclient ./internal/adapter \
  -run 'Test(Cursor|CopilotTranslation|ClientExplicitAllowWhenDaemonDown|HookUninstallPreservesUserAddedEntryAndRootKey|HookInstallRejectsMalformedHooks|SessionEndWaitMatchesHostBudget|VersionProbeBoundsTimeAndOutput)$|TestCursorAndCopilotFiles' \
  -count=1 -timeout=120s -v
```

Go가 PATH에 없으면 재생 명령에 `--go /절대경로/go`를 지정한다. 재생은 임시 Go overlay를 이용하므로 제품 소스를 수정하지 않는다. **진단 시험의 PASS는 당시 결함이 재현됐다는 뜻**이다. 해당 결함을 수정한 뒤에는 기대값을 그대로 회귀 기준으로 사용하지 않는다.

## 다음 확인 범위

실제 비정상 종료·반복 명령·파일 편집·MCP 실패·차단 후 미실행·안내 수신·stop 후속 실행·cold/warm 지연을 확인해야 한다. 토큰·비용·완료율 효과, IDE와 Cloud Agent, 다른 OS는 미검증이다. 단계별 변경과 승격 기준은 [Cursor 지원 개선 계획](../plans/2026-10-21-cursor-support-improvement-plan.md)에 둔다.

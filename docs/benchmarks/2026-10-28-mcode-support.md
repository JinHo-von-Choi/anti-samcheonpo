# MiniMax Code(mcode) 지원 실측

`2026-10-28`, Linux x86-64, MiniMax Code `0.6.13`에서 실측한 결과다. 수집은
`scripts/probe_mcode_hooks.py`가 `~/.minimax/hooks.json`을 잠시 만들고 임시 작업 폴더에서
`mcode exec --permission full`을 실행한 뒤 파일을 지우는 방식으로 했다. 훅은 임시 작업 폴더에서 온
호출만 기록했다.

## 훅이 실행되는 조건

| 위치 | 결과 |
| --- | --- |
| `~/.minimax/hooks.json`, 최상위 `"enabled": true` | 실행됨 |
| `~/.minimax/hooks.json`, `enabled` 없음 | 진단 없이 실행되지 않음 |
| `<작업 폴더>/.minimax/hooks.json` | 별도 활성화 기록이 없어 실행되지 않음 |
| `~/.minimax/plugins/<이름>` (Claude 형식 플러그인) | 스킬이 있어야 목록에 오르고, 목록에 올라도 훅은 실행되지 않음 |

handler의 시간 제한 필드는 `timeoutSeconds`다. 이미 떠 있던 세션은 새로 만든 훅 파일을 읽지 않는다.
`samcheonpo install --agent mcode`는 사용자 파일에 훅을 넣는다. 사용자가 꺼 둔 파일(`"enabled": false`)과,
`enabled` 없이 다른 훅이 들어 있는 파일은 켜지 않고 설치를 거부한다.

## payload

입력 47건을 익명화해 `testdata/hooks/mcode/`에 넣었다.
[입력 자료 설명](../../testdata/hooks/mcode/README.md)

- 도구 이름은 소문자(`bash`, `write`, `edit`, `read`, `task`, `task_output`)이고 호출 ID(`tool_use_id`)가 있다.
- 셸 결과는 `details.execution.exitCode`로 종료 코드를 명시한다. 시간 초과는 `exitCode: null`, `reason: command_timeout`이다.
- 백그라운드로 넘어간 셸은 `<bash_background task_id=...>` 자리표시로 답하고, 최종 결과는 같은 task의
  `task_output` 결과로 온다. 삼천포는 시작 사건을 결과 없는 실행으로 두고, 최종 결과가 오면 새 완료
  사건을 더한다. 끝까지 오지 않으면 시작 사건에 `background_result_missing`을 남긴다.
- 백그라운드 완료 알림은 `UserPromptSubmit`으로 오며 사용자 요청으로 세지 않는다.
- 하위 에이전트 훅은 자식의 세션 ID로 오고 부모를 가리키는 필드가 없다. 부모의 `task` 결과에 자식 세션 ID가
  나오면 그때 부모·자식을 잇는다.
- 훅 프로세스는 걸러진 환경 변수(`HOME`, `PATH`, `MINIMAX_HOOKS_*`, `MINIMAX_PROJECT_DIR`)만 받는다.
  `SAMCHEONPO_HOME`은 전달되지 않는다.
- 훅은 토큰 사용량을 주지 않는다. 실시간 비용은 미상이다. 사후 감사는 세션 기록의 사용량을 읽는다.

## 전달을 확인한 것

훅이 고정 응답을 돌려주게 해 CLI가 실제로 무엇을 모델에 전달하는지 확인했다.

| 응답 | 결과 |
| --- | --- |
| PreToolUse `{"decision":"block","reason":...}` | 명령이 실행되지 않고 사유가 도구 결과로 모델에 보임. 그 호출의 PostToolUse는 오지 않음 |
| PostToolUse `hookSpecificOutput.additionalContext` | 모델에 `<plugin-hook-context>`로 전달됨 |
| Stop `{"decision":"block","reason":...}` | 사유가 다음 턴 요청이 되어 작업이 이어짐 |

프롬프트 주입(UserPromptSubmit의 additionalContext)은 확인하지 않아 꺼 두었다.

## 삼천포 연결

훅을 `samcheonpo-hook hook <Event> mcode`로 넘기고, 같은 실패 시험(`python3 -m pytest -q test_probe.py`)을
5회까지 그대로 반복하라고 지시했다. 3회째 결과 뒤에 `s1.identical_rerun` 안내가 전달됐고, 모델은 4회째에서
반복을 멈추고 안내를 그대로 인용했다. [에이전트 출력](../../bench/mcode/2026-10-28/advice-agent.stdout) ·
[사건 목록](../../bench/mcode/2026-10-28/advice-events.json)

처음 연결에서는 안내가 전달되지 않았다. 데몬의 PostToolUse 응답이 약 60ms 걸렸는데 전송기의 기본 대기
시간이 50ms였기 때문이다. mcode는 훅 프로세스를 기다리므로 전송기의 mcode 대기 시간을 PreToolUse 250ms,
PostToolUse 500ms로 정했다.

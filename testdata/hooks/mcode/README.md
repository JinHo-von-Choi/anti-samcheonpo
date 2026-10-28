# MiniMax Code(mcode) 훅 입력

`0.6.13`, Linux x86-64에서 2026-10-28 수집한 입력 47개다. 시나리오는 넷이다.

| 접두어 | 내용 |
| --- | --- |
| `shell-` | 동기 셸 성공·실패(종료 코드 7), 파일 쓰기·편집·읽기 |
| `background-` | 40초 시간 초과로 끝난 셸, `run_in_background` 셸, `task_query`, `task_output`, 완료 알림 |
| `subagent-` | `task` 도구로 띄운 하위 에이전트 |
| `deliver-` | 실행 전 차단, 도구 뒤 안내, 종료 보류에 대한 응답 전달 |

`manifest.json`은 파일별 SHA-256, 익명화 규칙, `deliver-` 시나리오에서 훅이 돌려준 응답을 담는다.

## 수집 방법

`scripts/probe_mcode_hooks.py run --install user`가 `~/.minimax/hooks.json`을 잠시 만들고,
`mcode exec --permission full`을 임시 작업 폴더에서 실행한 뒤 파일을 지웠다. 훅은 임시 작업
폴더에서 온 호출만 기록했다. 복사한 HOME은 로그인이 되지 않고 갱신 토큰을 돌려쓰다가 실제 로그인을
끊을 수 있어서 쓰지 않았다.

## 수집된 사실

설치:
- `~/.minimax/hooks.json`은 최상위에 `"enabled": true`가 있어야 실행된다. 시간 제한 필드는
  `timeoutSeconds`다. 이 값이 없으면 진단 없이 아무 훅도 불리지 않는다.
- 프로젝트 파일(`<작업 폴더>/.minimax/hooks.json`)은 별도 활성화 기록이 필요해 이번 수집에서 불리지 않았다.
- 로컬 marketplace(`~/.minimax/plugins/<이름>`)의 Claude 형식 플러그인은 스킬이 하나 이상 있어야
  목록에 오른다. 목록에 오른 뒤에도 훅은 불리지 않았다. `mcode plugin add <이름>@local`은
  `LOCAL_PLUGIN_INSTALL_UNSUPPORTED`를 낸다.
- 이미 떠 있던 세션은 새로 만든 훅 파일을 읽지 않았다.

payload:
- 공통 필드는 `hook_event_name`, `session_id`, `turn_id`, `transcript_path`, `cwd`, `model`,
  `permission_mode`다. `transcript_path`는 `/tmp/minimax-plugin-hooks/transcripts/` 아래를 가리키지만
  수집 직후 그 폴더는 비어 있었다.
- 도구 이름은 소문자다: `bash`, `write`, `edit`, `read`, `task`, `task_query`, `task_output`.
- `write`와 `read`는 `path`를, `edit`는 `file_path`·`old_string`·`new_string`을 쓴다.
- 셸 결과(`tool_response.details.execution`)는 `status`, `reason`, `exitCode`를 명시한다. 시간 초과는
  `reason: command_timeout`, `exitCode: null`이다. 출력은 `processOutput.stdout`·`stderr`에 있다.
- `PostToolUseFailure`는 없다. 실패한 셸도 `PostToolUse`로 온다.
- `run_in_background: true` 셸의 결과는 `<bash_background task_id="bg_...">`로 시작하는 자리표시다.
  최종 상태와 종료 코드는 같은 `task_id`의 `task_output` 결과 `details.status`·`details.execution`에 온다.
  `running`인 동안 여러 번 올 수 있다.
- 백그라운드 완료 알림은 `UserPromptSubmit`의 `prompt`로 온다. 본문은 `<background-task-finished>`로
  시작하고 task 상태는 있지만 종료 코드는 없다.
- 하위 에이전트의 훅은 자식의 `session_id`와 같은 `agent_id`, `agent_type`을 담는다. 부모 세션을
  가리키는 필드는 없다. 부모의 `task` 결과 본문에 자식의 `session_id`가 나온다.
- 이번 수집에서 `SessionEnd`, `PreCompact`, `PostCompact`, `PermissionRequest`는 불리지 않았다.

응답 전달(`deliver-`):
- PreToolUse에 `{"decision": "block", "reason": ...}`을 주면 명령은 실행되지 않고 사유가 도구 결과로
  모델에 보인다. 그 호출의 PostToolUse는 오지 않는다.
- PostToolUse의 `hookSpecificOutput.additionalContext`는 모델에 `<plugin-hook-context>`로 전달된다.
- Stop에 `{"decision": "block", "reason": ...}`을 주면 사유가 다음 턴 요청이 되어 작업이 이어진다.

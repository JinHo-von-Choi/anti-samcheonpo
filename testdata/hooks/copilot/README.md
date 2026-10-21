# Copilot CLI 훅 입력

`1.0.101`, Linux x86-64에서 2026-10-22 수집한 입력 21개다.
`manifest.json`은 각 파일의 SHA-256과 익명화 규칙을 담는다.
세션 ID와 경로는 대응 관계를 유지해 치환했다.

수집은 `scripts/probe_copilot_hooks.py`가 임시 작업 디렉터리와 임시 `COPILOT_HOME`에서
`--allow-all-tools`와 `COPILOT_ALLOW_ALL=true`로 실행해 얻었다.
사용자의 실제 Copilot 설치·인증·감시 설정은 건드리지 않았다.

수집된 사실이 이 자료의 전부다.

- 런타임 이벤트 이름(`sessionStart`, `preToolUse`, `postToolUse`, `agentStop`,
  `userPromptSubmitted`, `sessionEnd`)과 PascalCase 별칭(`SessionStart`, `PreToolUse`,
  `PostToolUse`, `Stop`, `SessionEnd`)이 **둘 다** 발화한다. `UserPromptSubmitted` 별칭은
  발화하지 않았고 `permissionRequest`와 `PermissionRequest`는 같은 camelCase payload를 준다.
- 런타임 이름은 camelCase payload(`sessionId`, `toolName`, `toolArgs`, `toolResult`)를,
  PascalCase 별칭은 snake_case payload(`session_id`, `tool_name`, `tool_input`, `tool_result`)를 준다.
- **어느 payload에도 도구 호출 ID가 없다.** 시작과 결과는 세션 안에서 도구 이름과 도착 순서로만
  이어진다.
- `timestamp`는 런타임 이름에서 epoch 밀리초, PascalCase 별칭에서 ISO 8601 문자열이다.
- 셸 종료 코드는 payload 필드가 아니라 `textResultForLlm` 안의
  `<shellId: N completed with exit code N>` 문장으로만 온다. 종료 코드 7이어도
  `resultType`은 `success`다. `postToolUseFailure`는 발화하지 않았다.
- `apply_patch`의 `toolArgs`는 객체가 아니라 patch 본문 문자열이다.
- `view`는 `file_path`가 아니라 `path`를 쓴다.
- `errorOccurred`, `preCompact`, `notification`, `subagentStart`, `subagentStop`는 이번 수집에서
  발화하지 않았다. payloads도 없다.

차단·거부·추가 문맥 주입의 전달은 이 자료로 입증되지 않는다.
입증은 [Copilot 지원 실측](../../../docs/benchmarks/2026-10-22-copilot-support.md)에서 별도로 다룬다.

원본 측정: [Copilot 지원 실측](../../../docs/benchmarks/2026-10-22-copilot-support.md).

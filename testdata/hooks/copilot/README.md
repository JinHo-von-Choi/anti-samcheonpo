# Copilot CLI 훅 입력

Copilot CLI 1.0.101과 1.0.104의 입력을 재생하는 테스트 자료입니다. `manifest.json`에 파일별 SHA-256과 익명화 규칙을 기록합니다. 세션 ID와 경로는 자료 안의 대응 관계를 유지해 치환했습니다.

런타임 이름의 파일은 camelCase 필드(`sessionId`, `toolName`, `toolArgs`, `toolResult`)를 사용합니다. `vscode-`로 시작하는 파일은 PascalCase 이벤트와 snake_case 필드를 사용합니다. 파일명은 대소문자를 구별하지 않는 파일 시스템에서도 충돌하지 않도록 나누었습니다.

- 도구 호출 ID가 없어 시작과 결과를 세션·도구 이름·도착 순서로 연결합니다.
- `timestamp`는 런타임 형식에서 epoch 밀리초, 별칭 형식에서 ISO 8601 문자열입니다.
- 셸 종료 코드는 `textResultForLlm`의 `<shellId: N completed with exit code N>`에서 읽습니다.
- `apply_patch`의 `toolArgs`는 patch 문자열이고, `view`는 `path` 필드를 사용합니다.

현재 연결 방법과 기능은 [지원 안내](../../../docs/support-matrix.md#github-copilot-cli)에 있습니다.

# 반복 작업과 완료 검사

삼천포는 요청, 파일 변경, 명령 결과를 함께 살펴봅니다. 같은 실패를 반복하면 다음 행동을 안내하고, 작업을 끝내려 할 때는 변경한 내용에 맞는 검사를 확인합니다.

## 요청이 바뀌면

조사·문서 작성·구현 요청을 구분합니다. 조사에서 구현으로 넘어가면 새 작업을 기준으로 진행 상황을 살펴봅니다. 상태 질문은 기존 작업에 이어지고, 취소하거나 바꾼 요구사항은 그 상태로 보존됩니다.

후속 요청과 요구사항은 세션을 재시작해도 이어집니다. 화면에는 요약을 보여 주며, 전체 요청은 로컬 기록에 저장합니다.

## 변경한 내용 검사하기

검사 결과는 실행한 폴더, 검사 대상, 파일 변경 시점을 기준으로 연결합니다. 예를 들어 인증 코드를 고쳤다면 인증 관련 검사를 확인합니다. 다른 패키지의 통과 결과나 수정 전 결과로는 해당 변경의 검사를 마칠 수 없습니다.

다음 기록은 검사를 더 진행할 때 참고합니다.

- 실제로 실행한 테스트의 통과·실패와 종료 코드
- 검사가 다루는 파일과 마지막 변경 시점
- 백그라운드로 실행한 명령의 최종 결과
- 요청한 요구사항별 진행 상태

현재 세션의 검사와 요구사항을 보려면 다음을 실행하세요.

```sh
samcheonpo evidence --session SESSION_ID
```

완료 조건을 직접 지정하려면 [작업 계약](reference.md#작업-계약)을 사용합니다.

## 문서·화면·데이터를 완료 조건에 연결하기

v2 계약에서는 검사 명령에 결과물 파일을 함께 지정할 수 있습니다. `evidence.kind`에는 `document`, `ui`, `data`, `api`, `deployment` 중 하나를 씁니다.

```yaml
spec: progress-contract/2
goal: 설정 안내 문서 작성
done:
  - id: setup-guide
    check: python scripts/check_setup_guide.py
    pure: true
    evidence:
      kind: document
      artifacts: [docs/setup.md]
```

`check`에는 프로젝트에서 실행할 검사 명령을 적습니다. 위 예시는 `scripts/check_setup_guide.py`가 문서 내용을 검사하는 경우입니다. `artifacts`에는 프로젝트 안의 결과물 파일을 지정합니다.

검사가 통과하고 지정한 파일이 존재해야 완료 조건을 충족합니다. 파일이 바뀌면 이전 결과를 다시 확인합니다. API·배포처럼 매번 새 결과가 필요한 검사는 계약에서 재사용 여부를 설정합니다.

## 경고와 차단 기록 보기

```sh
samcheonpo interventions --session SESSION_ID --json
samcheonpo interventions --session SESSION_ID --verdict VERDICT_ID --json
samcheonpo interventions --since 7d --by-agent
```

기록에는 어떤 행동을 권고했는지, 실행 전에 막았는지, 종료를 보류했는지가 구분되어 나옵니다. 관련 명령과 파일, 적용한 규칙도 함께 확인할 수 있습니다. 같은 대상에 같은 안내를 반복하는 횟수에는 상한이 있습니다.

경고에 대응하는 명령과 차단 설정은 [설정·동작 상세](reference.md#귀띔에서-차단까지)에 있습니다.

## 여러 에이전트가 함께 작업하기

Claude Code와 Codex의 하위 에이전트는 각자의 도구 호출을 따로 기록합니다. 수집한 사용량은 공유 예산에 합산하며, 부모와 자식의 연결은 재시작 후에도 이어집니다. 자식 작업의 결과는 부모의 인수 조건에 따라 확인합니다.

Claude Code는 자식의 종료도 보류할 수 있습니다. Codex는 자식의 도구 실행 전에 차단하며, 종료 검사는 부모 세션에서 처리합니다.

```sh
samcheonpo lineage --agent claude --session SESSION_ID --format json
```

세션 사이에 작업을 넘길 때는 담당 파일과 인수 조건을 지정할 수 있습니다.

```sh
samcheonpo handoff link PARENT_SESSION_ID \
  --from claude --agent codex --session CHILD_SESSION_ID \
  --owns 'src/auth/**' --acceptance auth-tests
```

연결은 새 세션을 시작할 때 적용됩니다. 공유 빌드 폴더·데이터베이스·브라우저 세션을 다루는 연동 도구는 자원 식별자를 보내 동시 쓰기 충돌도 추적할 수 있습니다.

Python에서 Deep Agents를 사용할 때는 [SDK 연결 안내](../plugins/deepagents/README.md)를 참고하세요.

## 작업 뒤 남은 프로세스

Linux에서는 작업 폴더에서 시작되어 남아 있는 프로세스를 알립니다. PID와 시작 시각, 부모 프로세스, 수신 포트를 함께 기록하므로 개발 서버인지 정리가 필요한 작업인지 살펴볼 수 있습니다.

설치된 Codex 호스트와 삼천포의 감시 프로세스는 대상에서 제외합니다. 알림을 받은 프로세스는 사용자가 용도를 확인한 뒤 정리합니다.

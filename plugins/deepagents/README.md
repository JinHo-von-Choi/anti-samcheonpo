# Deep Agents 연결

Python 애플리케이션의 Deep Agents에 삼천포를 연결합니다. 도구 실행 전에 보호 경로와 예산을 확인하고, 검사할 일이 남아 있으면 에이전트가 작업을 이어가도록 합니다.

## 연결하기

`samcheonpo` 실행 파일을 PATH에 두고, 이 폴더를 애플리케이션의 Python 모듈 경로에 추가합니다.

```sh
PYTHONPATH=/path/to/anti-samcheonpo/plugins/deepagents python app.py
```

실행 파일 경로를 직접 지정할 때는 `SAMCHEONPO_BIN`을 사용합니다. 아래 예시의 `model`, `tools`, `backend`, `subagents`에는 애플리케이션에서 쓰는 값을 전달하세요.

```python
from middleware import create_monitored_agent

agent = create_monitored_agent(
    session_id="session-001",
    root="/workspace/project",
    model=model,
    tools=tools,
    backend=backend,
    subagents=subagents,
    max_stop_continuations=2,
)
try:
    result = agent.invoke({
        "messages": [{"role": "user", "content": request}]
    })
finally:
    agent.close()
```

`session_id`는 대화를 구분하는 고유 값이고, `root`는 프로젝트의 절대 경로입니다. 여러 차례 이어지는 대화에는 같은 세션 ID를 사용합니다. 비동기 호출은 `ainvoke`, 세션 종료는 `aclose`를 사용합니다.

## 도구 실행과 종료 검사

도구를 실행하기 전에 삼천포의 판정을 받습니다. 거부된 호출에는 사유를 돌려주고, 실행한 도구의 결과에는 필요한 안내를 덧붙입니다.

종료 검사가 남아 있으면 모델을 다시 호출합니다. `max_stop_continuations`의 기본값은 2이며 0~8로 설정할 수 있습니다. 이 횟수까지 진행한 뒤에도 검사가 남으면 `CompletionHeld`가 발생합니다. 모니터와 연결되지 않으면 `MonitorUnavailable`이 발생하므로 연결을 복구한 뒤 다시 실행하세요. 도구 자체의 오류는 호출자에게 그대로 전달됩니다.

보류 횟수 소진 등으로 검증을 마치기 전에 종료하면 반환 상태의
`samcheonpo_completion_notice`에 사유가 담깁니다. 호스트는 이 값을 사용자에게 표시할 수 있습니다.

## 하위 에이전트와 사용량

`create_monitored_agent`는 기본 `general-purpose`와 `subagents`에 지정한 하위 에이전트를 함께 연결합니다. 병렬 호출은 각자의 세션으로 기록되고, 사용량은 공유 예산에 합산됩니다. 원격·사전 컴파일 그래프는 각 그래프에 미들웨어를 연결해 사용합니다.

토큰 사용량은 모델 응답의 `usage_metadata`에서 수집합니다. 일반 입력, 캐시 읽기·쓰기, 출력을 나누어 기록합니다. 모델에서 제공한 사용량과 단가를 기준으로 비용을 계산합니다.

## 기존 그래프에 연결하기

직접 구성한 그래프에는 `create_middleware`를 전달합니다. 하위 그래프에도 같은 방식으로 각각 연결하세요.

```python
from deepagents import create_deep_agent
from middleware import create_middleware

monitor = create_middleware("session-001", "/workspace/project")
agent = create_deep_agent(model=model, tools=tools, middleware=[monitor])
```

대화가 끝나면 `monitor.close()` 또는 `monitor.aclose()`를 호출합니다.

Deep Agents 0.7.32, LangChain 1.4.8, LangGraph 1.2.19 기준입니다. 개발용 검사 실행 방법은 [개발 안내](../../docs/development.md#deep-agents-검사)에 있습니다.

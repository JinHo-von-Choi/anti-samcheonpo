---
description: AI가 바꾼 파일을 마지막 진척 시점으로 되돌리기 (인자 없음: 미리보기, apply <계획 ID>: 미리본 계획대로 실행)
allowed-tools: Bash(samcheonpo cmd:*)
---
!`samcheonpo cmd rollback $ARGUMENTS`

위 결과를 사용자에게 그대로 알린다. 미리보기였다면 사용자가 apply를 원하는지 기다리고, 되돌렸다면 되돌린 상태에서 다음 지시를 기다린다.

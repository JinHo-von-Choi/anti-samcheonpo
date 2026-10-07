# 지원·검증 범위

아래 상태는 자동 배포 약속이 아니라 실제 검증 기록을 구분하기 위한 표다.

| 환경 | 현재 확인한 범위 | 미확인/제약 |
| --- | --- | --- |
| Linux amd64 | 실제 훅 수신·지연, 작업 인수인계, 2026-10-06 네이티브 후보의 키 없는 설치·제거/감사/첫 훅 및 체크섬 통과. 2026-10-07 격리 홈에서 실제 실행 파일로 설치·제거 시 사용자 설정 보존, 안내 후 실행 전 차단, 편집 뒤 허용, 상태줄, 되돌리기, 영수증 확인 | 2026-10-07 측정(load average 약 69)에서 훅 p95 13.7–18.1ms로 10ms 목표 미달. 같은 조건의 이전 버전도 12.8–16.3ms |
| Linux arm64 | 네이티브 릴리스 후보 워크플로 정의 | 이 작업 환경에서 실제 실행하지 않음 |
| macOS amd64/arm64 | 네이티브 릴리스 후보 워크플로 정의 | 실제 설치·제거·훅 실행 결과 대기 |
| WSL | Linux 실행 파일 사용을 검증할 대상 | 실제 WSL 환경 smoke 대기; 일반 Linux 결과로 대체하지 않음 |
| Windows 네이티브 | 지원하지 않음 | Unix 소켓·프로세스 처리 의존 |

실제 A/B 실행의 감시 프로세스 종료 확인은 현재 Linux의 프로세스 소유 홈 확인 경로를 사용한다. 다른 OS에서 판정 데몬을 사용한 시행은 종료/정산 완료를 확정하지 못하면 감시 비용을 미확인으로 남긴다. 단순 소켓 소멸을 최종 원장 정산의 증거로 취급하지 않는다.

에이전트별 캡처 fixture와 시험 버전 범위는 `internal/adapter/caps.go`가 기준이다. 범위 안이라는 사실은 모든 버전의 실제 제품 동작이나 개입 효과를 인증하지 않는다. `doctor --agent <이름>`은 현재 바이너리의 버전 문자열과 설정을 확인하며, 에이전트에 모델 작업을 요청하지 않는다.

## 릴리스 후보 만들기

```sh
sh scripts/package-release.sh 0.1.0-dev /tmp/samcheonpo-release-new
```

기존 출력 디렉터리를 덮어쓰지 않는다. 네이티브 플랫폼에서 두 실행 파일을 만들고, API 키 없는 분리 환경의 설치/제거·감사·기본 훅 검사를 실행한 뒤 tar.gz와 SHA256SUMS를 만든다. 교차 컴파일만으로 플랫폼 검증을 주장하지 않도록 호스트와 타깃이 다르면 거절한다. BUILD.txt에 버전·커밋·도구 체인·추적 파일 변경 여부를 기록한다. 산출물은 후보이며 스크립트는 게시하지 않는다.

`.github/workflows/release.yml`은 수동 `workflow_dispatch`만 제공한다. 원격 실행 전에 `release-candidate` 환경의 필수 검토자를 설정해야 한다. 워크플로는 후보 artifact만 올리고 태그·GitHub Release·배포를 만들지 않는다. 코드 라이선스는 MIT(`LICENSE`)다. 배포 정책은 실제 공개 릴리스 전에 별도로 확정해야 한다. 현재 로컬 실행을 원격 CI 성공으로 표시하지 않는다.

구성은 GitHub의 [수동 실행 문서](https://docs.github.com/en/actions/how-tos/manage-workflow-runs/manually-run-a-workflow), [Go 설정 액션](https://github.com/actions/setup-go), [artifact 액션](https://github.com/actions/upload-artifact), [호스티드 러너 표](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)를 확인해 작성했다. 액션은 확인한 v7 커밋으로 고정했으며, 버전 변경 시 다시 검증해야 한다.

package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/install"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
)

func copilotChecks(dbPath string) []diagnostic {
	items := install.CopilotInstallations()
	checks := []diagnostic{}
	if len(items) == 0 {
		checks = append(checks, diagnostic{"copilot_install", "warning", "Copilot 연결 설치 기록 없음; samcheonpo install --agent copilot로 연결하세요"})
	}
	scopes := []string{}
	for _, item := range items {
		switch {
		case item.Broken != "":
			checks = append(checks, diagnostic{"copilot_install", "error", item.Path + ": " + item.Broken})
		case len(item.Missing) > 0:
			checks = append(checks, diagnostic{"copilot_install", "warning", "필수 Copilot 훅 누락·수정: " + item.Path + " (" + strings.Join(item.Missing, ", ") + "); 같은 경로로 다시 설치하세요"})
		default:
			checks = append(checks, diagnostic{"copilot_install", "ok", fmt.Sprintf("Copilot 훅 %d종과 설치 기록 일치: %s", len(install.CopilotEvents), item.Path)})
		}
		if !item.Executable {
			checks = append(checks, diagnostic{"copilot_executable", "error", "설치 기록의 Copilot 훅 실행 파일을 찾을 수 없습니다: " + item.Path})
		}
		scopes = append(scopes, item.Scope)
	}
	if len(scopes) > 0 && !containsString(scopes, "user") && containsString(scopes, "repository") {
		checks = append(checks, diagnostic{"copilot_scope", "warning", "저장소 수준 훅만 설치됨; Copilot은 신뢰된 작업 폴더에서만 저장소 훅을 불러오므로 그 외 작업에서는 훅이 발화하지 않습니다"})
	}
	received := ""
	if db, err := ledger.OpenReadOnly(dbPath); err == nil {
		_ = db.QueryRow(`SELECT COALESCE(MAX(e.ts),'') FROM event e JOIN session s ON s.id=e.session_id WHERE s.agent='copilot'`).Scan(&received)
		_ = db.Close()
	}
	if received == "" {
		checks = append(checks, diagnostic{"copilot_events", "warning", "Copilot 사건 수신 기록 없음; 설치 후 새 Copilot 세션에서 확인하세요"})
	} else {
		status := "warning"
		if ts, err := time.Parse(time.RFC3339Nano, received); err == nil && time.Since(ts) >= 0 && time.Since(ts) < 24*time.Hour {
			status = "ok"
		}
		checks = append(checks, diagnostic{"copilot_events", status, fmt.Sprintf("마지막 Copilot 사건: %s (현재 연결·안내 전달 증거는 아님)", received)})
	}
	checks = append(checks, diagnostic{"copilot_call_id", "warning", "Copilot CLI는 도구 호출 ID를 보내지 않음; 시작과 결과는 도구 이름과 도착 순서로만 이어지므로 관측 공백으로 남습니다"})
	checks = append(checks, diagnostic{"copilot_delivery", "ok", "실행 전 차단과 도구 뒤 안내 전달은 실제 Copilot CLI로 확인함. 종료 후속 실행과 프롬프트 주입은 미검증이라 꺼 둠"})
	return checks
}

// containsString reports whether list holds want.
func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

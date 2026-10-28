package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/install"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
)

func mcodeChecks(dbPath string) []diagnostic {
	state, err := install.InspectHooks(install.Mcode)
	if err != nil {
		return []diagnostic{{ID: "mcode_install", State: "error", Message: err.Error()}}
	}
	checks := []diagnostic{}
	switch {
	case !state.Installed:
		checks = append(checks, diagnostic{"mcode_install", "warning", "mcode 연결 설치 기록 없음; samcheonpo install --agent mcode로 연결하세요"})
	case len(state.Missing) > 0:
		checks = append(checks, diagnostic{"mcode_install", "warning", "필수 mcode 훅 누락·수정·중복: " + strings.Join(state.Missing, ", ") + "; samcheonpo uninstall --agent mcode 뒤 다시 설치하세요"})
	default:
		checks = append(checks, diagnostic{"mcode_install", "ok", "mcode 필수 훅과 설치 기록 일치: " + state.Path})
	}
	if state.Installed && state.Off {
		checks = append(checks, diagnostic{"mcode_enabled", "error", state.Path + `의 최상위 "enabled"가 true가 아니라 mcode가 어떤 훅도 실행하지 않습니다`})
	}
	if state.Installed && !state.Executable {
		checks = append(checks, diagnostic{"mcode_executable", "error", "설치 기록의 mcode 훅 실행 파일을 찾을 수 없습니다"})
	}
	received := ""
	if db, err := ledger.OpenReadOnly(dbPath); err == nil {
		_ = db.QueryRow(`SELECT COALESCE(MAX(e.ts),'') FROM event e JOIN session s ON s.id=e.session_id WHERE s.agent='mcode'`).Scan(&received)
		_ = db.Close()
	}
	if received == "" {
		checks = append(checks, diagnostic{"mcode_events", "warning", "mcode 사건 수신 기록 없음; mcode는 시작할 때 훅 파일을 읽으므로 설치 뒤 새 세션에서 확인하세요"})
	} else {
		status := "warning"
		if ts, err := time.Parse(time.RFC3339Nano, received); err == nil && time.Since(ts) >= 0 && time.Since(ts) < 24*time.Hour {
			status = "ok"
		}
		checks = append(checks, diagnostic{"mcode_events", status, fmt.Sprintf("마지막 mcode 사건: %s (현재 연결·안내 전달 증거는 아님)", received)})
	}
	checks = append(checks, diagnostic{"mcode_delivery", "ok", "실행 전 차단, 도구 뒤 안내, 종료 보류 전달은 실제 mcode 0.6.13으로 확인함. 프롬프트 주입은 미검증이라 꺼 둠"})
	checks = append(checks, diagnostic{"mcode_usage", "warning", "mcode 훅은 토큰 사용량을 주지 않아 비용은 미상으로 표시됩니다"})
	return checks
}

package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/install"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
)

func cursorChecks(dbPath string) []diagnostic {
	state, err := install.InspectCursor()
	if err != nil {
		return []diagnostic{{ID: "cursor_install", State: "error", Message: err.Error()}}
	}
	checks := []diagnostic{}
	switch {
	case !state.Installed:
		checks = append(checks, diagnostic{"cursor_install", "warning", "Cursor 연결 설치 기록 없음; samcheonpo install --agent cursor로 연결하세요"})
	case len(state.Missing) > 0:
		checks = append(checks, diagnostic{"cursor_install", "warning", "필수 Cursor 훅 누락·수정·중복: " + strings.Join(state.Missing, ", ") + "; 같은 --hooks-file 경로로 다시 설치하세요"})
	default:
		checks = append(checks, diagnostic{"cursor_install", "ok", "Cursor 필수 훅과 설치 기록 일치: " + state.Path})
	}
	if state.Installed && !state.Executable {
		checks = append(checks, diagnostic{"cursor_executable", "error", "설치 기록의 Cursor 훅 실행 파일을 찾을 수 없습니다"})
	}
	received := ""
	if db, err := ledger.OpenReadOnly(dbPath); err == nil {
		_ = db.QueryRow(`SELECT COALESCE(MAX(e.ts),'') FROM event e JOIN session s ON s.id=e.session_id WHERE s.agent='cursor'`).Scan(&received)
		_ = db.Close()
	}
	if received == "" {
		checks = append(checks, diagnostic{"cursor_events", "warning", "Cursor 사건 수신 기록 없음; 설치 후 새 Cursor 세션에서 확인하세요"})
	} else {
		status := "warning"
		if ts, err := time.Parse(time.RFC3339Nano, received); err == nil && time.Since(ts) >= 0 && time.Since(ts) < 24*time.Hour {
			status = "ok"
		}
		checks = append(checks, diagnostic{"cursor_events", status, fmt.Sprintf("마지막 Cursor 사건: %s (현재 연결·안내 전달 증거는 아님)", received)})
	}
	checks = append(checks, diagnostic{"cursor_delivery", "warning", "현재 삼천포 Cursor 어댑터는 관찰 전용; 실제 차단·안내·종료 보류 전달은 미검증"})
	return checks
}

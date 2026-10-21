package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/install"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
)

func opencodeChecks(dbPath string) []diagnostic {
	checks := []diagnostic{}
	path := install.OpencodePluginPath()
	if _, err := os.Stat(path); err != nil {
		checks = append(checks, diagnostic{"opencode_install", "warning", "opencode 플러그인 파일 없음; samcheonpo install --agent opencode로 연결하세요"})
	} else {
		registry, err := install.LoadRegistry()
		switch {
		case err != nil:
			checks = append(checks, diagnostic{"opencode_install", "warning", "무결성 기록을 읽지 못함: " + err.Error()})
		default:
			found := false
			for _, b := range registry.Bundles {
				if b.Plugin != "opencode" {
					continue
				}
				found = true
				problems := install.VerifyRegistry(registry)
				opencodeProblems := []install.ManifestProblem{}
				for _, p := range problems {
					if p.Plugin == "opencode" {
						opencodeProblems = append(opencodeProblems, p)
					}
				}
				if len(opencodeProblems) > 0 {
					checks = append(checks, diagnostic{"opencode_install", "warning", "opencode 플러그인이 설치 이후 바뀜 (" + fmt.Sprintf("%d건", len(opencodeProblems)) + "); 같은 경로로 다시 설치하세요"})
				} else {
					checks = append(checks, diagnostic{"opencode_install", "ok", "opencode 플러그인과 무결성 기록 일치: " + path})
				}
				break
			}
			if !found {
				checks = append(checks, diagnostic{"opencode_install", "warning", "플러그인 파일은 있으나 설치 기록 없음; 재설치 전 파일 출처를 직접 확인하세요"})
			}
		}
	}
	received := ""
	if db, err := ledger.OpenReadOnly(dbPath); err == nil {
		_ = db.QueryRow(`SELECT COALESCE(MAX(e.ts),'') FROM event e JOIN session s ON s.id=e.session_id WHERE s.agent='opencode'`).Scan(&received)
		_ = db.Close()
	}
	if received == "" {
		checks = append(checks, diagnostic{"opencode_events", "warning", "opencode 사건 수신 기록 없음; 설치 후 새 opencode 세션에서 확인하세요"})
	} else {
		status := "warning"
		if ts, err := time.Parse(time.RFC3339Nano, received); err == nil && time.Since(ts) < 24*time.Hour {
			status = "ok"
		}
		checks = append(checks, diagnostic{"opencode_events", status, fmt.Sprintf("마지막 opencode 사건: %s (현재 연결·안내 전달 증거는 아님)", received)})
	}
	observed := opencodeDeliveryObserved(dbPath)
	if observed == "" {
		checks = append(checks, diagnostic{"opencode_delivery", "warning", "최근 30일 opencode 안내 전달 실측 기록 없음; interventions --agent opencode로 확인 필요"})
	} else {
		checks = append(checks, diagnostic{"opencode_delivery", "ok", "최근 30일 opencode 안내 전달 실측 최근 기록: " + observed})
	}
	return checks
}

// opencodeDeliveryObserved returns when an opencode prescription was last
// delivered, or "" when the ledger holds no delivery in the last 30 days.
func opencodeDeliveryObserved(dbPath string) string {
	db, err := ledger.OpenReadOnly(dbPath)
	if err != nil {
		return ""
	}
	defer db.Close()
	attempts, err := db.RecoveryAttemptsSince(time.Now().Add(-30 * 24 * time.Hour))
	if err != nil {
		return ""
	}
	latest := time.Time{}
	for _, a := range attempts {
		if a.Agent != "opencode" || a.DeliveredAt == nil {
			continue
		}
		if a.DeliveredAt.After(latest) {
			latest = *a.DeliveredAt
		}
	}
	if latest.IsZero() {
		return ""
	}
	return latest.Format("2006-01-02 15:04")
}

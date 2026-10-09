package live

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lazyre"
)

// extendArgRe reads one grant: hours (2h, 2시간), minutes (30m, 30min, 30분)
// or tokens (500K, 20M, 1G, upper case only so 20m stays minutes).
var extendArgRe = lazyre.New(`^(\d+(?:\.\d+)?)(h|시간|m|min|분|K|M|G)$`)

const extendUsage = "사용법: /samcheonpo:extend 2h | 30m | 20M (시간은 h·m, 토큰은 대문자 K·M·G)"

// parseExtend reads the time and tokens a user grants past the ceiling.
func parseExtend(arg string) (hours float64, tokens int64, err error) {
	fields := strings.Fields(arg)
	if len(fields) == 0 {
		return 0, 0, errors.New(extendUsage)
	}
	for _, f := range fields {
		m := extendArgRe.FindStringSubmatch(f)
		if m == nil {
			return 0, 0, fmt.Errorf("%q를 읽지 못했다. %s", f, extendUsage)
		}
		n, perr := strconv.ParseFloat(m[1], 64)
		if perr != nil || n <= 0 {
			return 0, 0, fmt.Errorf("%q를 읽지 못했다. %s", f, extendUsage)
		}
		switch m[2] {
		case "h", "시간":
			hours += n
		case "m", "min", "분":
			hours += n / 60
		case "K":
			tokens += int64(n * 1_000)
		case "M":
			tokens += int64(n * 1_000_000)
		case "G":
			tokens += int64(n * 1_000_000_000)
		}
	}
	return hours, tokens, nil
}

// extend raises the session ceiling by the user's grant and lifts a stop the
// ceiling caused. The grant is recorded with the session.
func (d *Daemon) extend(s *Session, arg string) (string, error) {
	hours, tokens, err := parseExtend(arg)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	limit, ok := s.eng.ExtendCeiling(hours, tokens)
	verdict := ""
	if ok && s.unresolved != nil && s.unresolved.Rule == "s8.session_ceiling" {
		verdict = s.unresolved.ID
		s.unresolved = nil
	}
	if ok {
		s.broadcastHUD()
	}
	s.mu.Unlock()
	if !ok {
		return "설정된 세션 상한이 없다. 상한은 설정의 detectors.s8_cost.ceiling_hours·ceiling_tokens나 계약의 budget.minutes로 정한다.", nil
	}
	if err := d.db.AddFeedback(verdict, s.ID, "s8.session_ceiling", "extend", strings.TrimSpace(arg), "", s.Root); err != nil {
		return "", err
	}
	return fmt.Sprintf("세션 상한을 늘렸다. 지금 상한: %s. 다시 닿으면 같은 방식으로 멈춘다.", limit), nil
}

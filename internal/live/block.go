package live

import (
	"fmt"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
)

// hardBlockReason states the refusal and the cause behind it.
//
// The agent reads a denied call as a failed tool call, so the reason names
// the rule that stopped it: a refusal the agent cannot explain is one it
// works around. The finding that follows the sentence says what was seen
// and what the contract asked for, so the agent can correct the work
// instead of retrying the same call.
func hardBlockReason(v detect.Signal, detail string) string {
	r := fmt.Sprintf("[삼천포 차단: %s] 악성 행동이 감지되어 실행이 거부되었습니다.", v.Rule)
	if detail != "" {
		r += "\n" + detail
	}
	return r
}

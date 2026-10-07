// Package policy maps desired advice onto actually supported capabilities.
// It does not execute commands, switch models, or raise privileges.
package policy

type Capabilities struct{ Inject, UserMessage, Block bool }
type Decision struct {
	Requested string `json:"requested"`
	Route     string `json:"route"`
	CanBlock  bool   `json:"can_block"`
	Reason    string `json:"reason"`
}

func Decide(request string, c Capabilities) Decision {
	d := Decision{Requested: request, Route: "observe", Reason: "지원되는 전달 수단이 없어 관찰만 기록한다"}
	if request == "block" && c.Block {
		d.Route = "block"
		d.CanBlock = true
		d.Reason = "이 훅은 차단을 지원한다"
		return d
	}
	if c.Inject {
		d.Route = "advice"
		d.Reason = "권고만 전달하며 실행이나 차단 성공을 가정하지 않는다"
	} else if c.UserMessage {
		d.Route = "user"
		d.Reason = "사용자 메시지만 지원한다"
	}
	return d
}

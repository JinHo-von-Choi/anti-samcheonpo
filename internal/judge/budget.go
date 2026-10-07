package judge

import "fmt"

// Budget is a session-local reservation ledger, guarded by its caller's lock.
// Estimates constrain dispatch; they are not invoice guarantees. Unknown
// charges retain their reservation and prohibit further automatic calls.
type Budget struct {
	Calls         int             `json:"calls"`
	SpentMicro    int64           `json:"spent_micro"`
	ReservedMicro int64           `json:"reserved_micro"`
	Unknown       bool            `json:"unknown"`
	Overrun       bool            `json:"overrun"`
	Pending       bool            `json:"pending"`
	Seen          map[string]bool `json:"seen"`
}

func (b Budget) Validate() error {
	if b.Calls < 0 || b.Calls > 128 || b.SpentMicro < 0 || b.ReservedMicro < 0 || len(b.Seen) != b.Calls || (b.Pending && (b.Unknown || b.ReservedMicro == 0)) {
		return fmt.Errorf("invalid judge budget state")
	}
	for key, seen := range b.Seen {
		if key == "" || !seen {
			return fmt.Errorf("invalid judge request identity")
		}
	}
	return nil
}

func (b *Budget) Reserve(key string, estimate, limit int64, maxCalls int) error {
	if b.Unknown {
		return fmt.Errorf("이전 판정 비용이 미확인이라 추가 호출을 중단했다")
	}
	if b.Overrun {
		return fmt.Errorf("계측 비용이 예약을 넘어 추가 호출을 중단했다")
	}
	if b.Pending {
		return fmt.Errorf("진행 중인 판정이 있어 추가 요청을 병합했다")
	}
	if b.Seen[key] {
		return fmt.Errorf("같은 목표 개정과 입력은 이미 판정했다")
	}
	if key == "" || estimate <= 0 || limit <= 0 || maxCalls <= 0 || maxCalls > 128 {
		return fmt.Errorf("판정 예산 설정 또는 예약액을 확인할 수 없다")
	}
	if b.Calls >= maxCalls {
		return fmt.Errorf("판정 호출 횟수 상한에 도달했다")
	}
	if b.SpentMicro < 0 || b.ReservedMicro < 0 || b.SpentMicro >= limit || b.ReservedMicro > limit-b.SpentMicro || estimate > limit-b.SpentMicro-b.ReservedMicro {
		return fmt.Errorf("판정 비용 예약이 남은 예산을 넘는다")
	}
	if b.Seen == nil {
		b.Seen = map[string]bool{}
	}
	b.Seen[key] = true
	b.Calls++
	b.Pending = true
	b.ReservedMicro += estimate
	return nil
}

func (b *Budget) Settle(actual int64, known bool) {
	if !b.Pending {
		return
	}
	b.Pending = false
	if !known || actual < 0 {
		b.Unknown = true
		return
	}
	if actual > b.ReservedMicro {
		b.Overrun = true
	} // estimate was not a safe bound
	if actual > int64(^uint64(0)>>1)-b.SpentMicro {
		b.Unknown = true
		return
	}
	b.SpentMicro += actual
	b.ReservedMicro = 0
}

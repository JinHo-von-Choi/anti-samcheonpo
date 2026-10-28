package live

import (
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

func TestHostRegistryGrowth(t *testing.T) {
	var h hostRegistry
	now := time.Now()
	if !h.take("k", now) || h.take("k", now) {
		t.Fatal("one measurement at a time")
	}
	h.done("k", 5*gb, true, now)
	if _, _, ok := h.growth("k"); !ok {
		t.Fatal("the first complete measurement is the reference")
	}
	if h.take("k", now.Add(time.Minute)) {
		t.Fatal("measured at most every five minutes")
	}
	if !h.take("k", now.Add(6*time.Minute)) {
		t.Fatal("measured again after five minutes")
	}
	h.done("k", 9*gb, false, now.Add(6*time.Minute))
	if _, _, ok := h.growth("k"); ok {
		t.Fatal("an incomplete measurement judges nothing")
	}
	h.take("k", now.Add(12*time.Minute))
	h.done("k", 27*gb, true, now.Add(12*time.Minute))
	if total, grown, ok := h.growth("k"); !ok || total != 27*gb || grown != 22*gb {
		t.Fatalf("total=%d grown=%d", total, grown)
	}
}

func hostVerify(s *Session, seq int64, d time.Duration) *event.Event {
	exit := 0
	return &event.Event{Seq: seq, Kind: event.KindTool, Tool: event.ToolShell, Category: event.CatVerify, Cmd: "cargo test --workspace",
		CmdNorm: "cargo test --workspace", ExitCode: &exit, DurationMS: d.Milliseconds(), TS: time.Now()}
}

func TestHostAdviceOnCheckTimeAndGrowth(t *testing.T) {
	_, ss := heavyDaemon(t, t.TempDir())
	s := ss[0]
	s.mu.Lock()
	s.Cfg.Detectors.S8.HostVerifyMinutes = 60
	s.Cfg.Detectors.S8.HostFreePct, s.Cfg.Detectors.S8.HostFreeGB = 0, 0
	s.noteVerify(hostVerify(s, 1, 50*time.Minute))
	none := len(s.pending)
	s.noteVerify(hostVerify(s, 2, 15*time.Minute))
	once := len(s.pending)
	s.noteVerify(hostVerify(s, 3, 10*time.Minute))
	again := len(s.pending)
	s.mu.Unlock()
	if none != 0 || once != 1 || again != 1 {
		t.Fatalf("advice at 60 minutes, then every 30: %d %d %d", none, once, again)
	}
	// growth past 10GB since the first measurement
	d := s.daemon
	key := s.heavyKey()
	d.host.take(key, time.Now())
	d.host.done(key, 2*gb, true, time.Now())
	d.host.ws[key].at = time.Time{}
	d.host.take(key, time.Now())
	d.host.done(key, 15*gb, true, time.Now())
	s.mu.Lock()
	s.judgeHost(hostVerify(s, 4, time.Second))
	s.judgeHost(hostVerify(s, 5, time.Second))
	var growth []string
	for _, v := range s.eng.Verdicts {
		if v.Facts["kind"] == "artifacts" && v.Primary && !v.Suppressed {
			growth = append(growth, v.Facts["target_hash"].(string))
		}
	}
	s.mu.Unlock()
	// a verdict of its own (not held by the check-time cooldown), once per
	// 10GB step; its delivery waits like any advice while an earlier
	// prescription's effect is unknown (recovery.Select)
	if len(growth) != 1 || growth[0] != "host:artifacts:1" {
		t.Fatalf("growth verdicts %v", growth)
	}
}

func TestHostBudgetStopsHeavyChecks(t *testing.T) {
	_, ss := heavyDaemon(t, t.TempDir())
	s := ss[0]
	s.mu.Lock()
	defer s.mu.Unlock()
	c := &contract.Contract{Goal: "x"}
	c.Budget.VerifyMinutes = 30
	s.c, s.acc.State = c, contract.StateAccepted
	ev := hostVerify(s, 1, 0)
	if sig := s.hostBudgetPre(ev); sig != nil {
		t.Fatalf("within budget: %+v", sig)
	}
	s.host.verifyMS = (31 * time.Minute).Milliseconds()
	sig := s.hostBudgetPre(ev)
	if sig == nil || sig.Rule != "s8.host_budget" || sig.Facts["kind"] != "verify_time" {
		t.Fatalf("over budget: %+v", sig)
	}
	light := hostVerify(s, 2, 0)
	light.Cmd, light.CmdNorm = "cargo test --test todo_test", "cargo test --test todo_test"
	if sig := s.hostBudgetPre(light); sig != nil {
		t.Fatalf("a light check stays possible: %+v", sig)
	}
}

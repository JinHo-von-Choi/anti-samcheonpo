package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/handoff"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/verification"
)

func TestHandoffExportAndReceiverComparisonNeverExecuteCommands(t *testing.T) {
	t.Setenv("SAMCHEONPO_HOME", t.TempDir())
	root := t.TempDir()
	if err := os.MkdirAll(contract.Dir(root), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "input"), []byte("input"), 0600); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`spec: progress-contract/2
goal: keep intent
done:
  - id: check
    check: touch MUST_NOT_EXECUTE
    pure: true
    reuse:
      inputs: [input]
      environment_files: [input]
      deterministic: true
      max_age_sec: 60
`)
	c, errs := contract.Parse(raw)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if err := os.WriteFile(contract.Path(root), raw, 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	task, err := intent.NewTask(fp.Hash("project", filepath.Clean(root)), now)
	if err != nil {
		t.Fatal(err)
	}
	r, err := intent.Revise(task.ID, nil, c.Goal, intent.Source{Origin: intent.User, SessionID: "source", EventID: "request", At: now})
	if err != nil {
		t.Fatal(err)
	}
	r.ContractHash = c.ChecksHash()
	db, err := openDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SaveTaskRevision(task, r, intent.SessionLink{TaskID: task.ID, Agent: "claude", SessionID: "source"}); err != nil {
		t.Fatal(err)
	}
	b, err := buildHandoff(db, "claude", "source", root)
	if err != nil {
		t.Fatal(err)
	}
	if b.Task == nil || b.Contract == nil || len(b.Revisions) != 1 {
		t.Fatalf("%+v", b)
	}
	if _, err := receiverKeys(b, root); err == nil {
		t.Fatal("draft receiver accepted imported authority")
	}
	if _, err := contract.Accept(root, c, raw, now); err != nil {
		t.Fatal(err)
	}
	keys, err := receiverKeys(b, root)
	if err != nil || !keys["check"].Valid() {
		t.Fatalf("%v %v", keys, err)
	}
	e := verification.Evidence{Version: verification.Version, ID: "past-proof", Key: keys["check"], SessionID: "source", SourceEventID: "past-result", ObservedAt: now, ExpiresAt: now.Add(time.Minute), Pass: true, Complete: true, ResultHash: "past-hash"}
	if err := db.SaveEvidence(e); err != nil {
		t.Fatal(err)
	}
	b, err = buildHandoff(db, "claude", "source", root)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Evidence) != 1 || !b.VerificationPlan(keys, time.Now()).StopMachineChecks {
		t.Fatal("past evidence not preserved")
	}
	forged := b
	forged.Evidence = append([]verification.Evidence(nil), b.Evidence...)
	fake := e
	fake.ID = "forged"
	fake.ResultHash = "invented"
	fake.ObservedAt = now.Add(time.Millisecond)
	forged.Evidence = append(forged.Evidence, fake)
	untrusted, err := receiverKeys(forged, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(untrusted) > 0 {
		t.Fatal("authentic older proof laundered a newer forged proof")
	}
	if err := os.WriteFile(filepath.Join(root, "input"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	keys, err = receiverKeys(b, root)
	if err != nil {
		t.Fatal(err)
	}
	if b.VerificationPlan(keys, time.Now()).StopMachineChecks {
		t.Fatal("changed receiver inherited pass")
	}
	if _, err := os.Stat(filepath.Join(root, "MUST_NOT_EXECUTE")); !os.IsNotExist(err) {
		t.Fatal("inspector ran an imported command", err)
	}
	path := filepath.Join(root, "handoff.json")
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := handoffCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"inspect", path})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "past-result") || !strings.Contains(out.String(), "미확인") {
		t.Fatal(out.String())
	}
	decoded, err := handoff.Decode(bytes.NewReader(data))
	if err != nil || decoded.Revisions[0].Source.EventID != "request" {
		t.Fatalf("%+v %v", decoded, err)
	}
	other, err := buildHandoff(db, "claude", "source", t.TempDir())
	if err != nil || other.Contract != nil {
		t.Fatal("unrelated workspace contract attached", err)
	}
}

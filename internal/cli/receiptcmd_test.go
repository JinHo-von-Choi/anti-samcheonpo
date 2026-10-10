package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/receipt"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/sources"
)

func TestReceiptCLIJSONPreservesBillingAndShareValidity(t *testing.T) {
	t.Setenv("SAMCHEONPO_HOME", t.TempDir())
	path, err := filepath.Abs("../../testdata/claude/basic.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	prices, err := cost.Load("")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	r, err := analyzeFile(sources.File{Path: path, Agent: "claude"}, cfg, config.Hash(cfg), prices)
	if err != nil {
		t.Fatal(err)
	}
	db, err := openDB()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.SaveAnalysis(r, 1, 2, nil); err != nil {
		t.Fatal(err)
	}
	db.Close()
	for _, share := range []bool{false, true} {
		cmd := receiptCmd()
		var output bytes.Buffer
		cmd.SetOut(&output)
		cmd.SetErr(&output)
		args := []string{r.Session.ID, "--billing", "subscription", "--format", "json"}
		if share {
			args = append(args, "--share")
		}
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		var got receipt.Receipt
		if err := json.Unmarshal(output.Bytes(), &got); err != nil {
			t.Fatalf("JSON output includes non-JSON text: %v", err)
		}
		if got.Billing.Kind != cost.BillingSubscription || got.Billing.KindSource != "user_declared" || got.Billing.ActualChargeMicro != nil || got.Billing.VerifiedSavingsMicro != nil || got.Billing.APIEquivalentMicro == nil {
			t.Fatalf("%+v", got.Billing)
		}
		if strings.Contains(got.Total.Label, "지출") {
			t.Fatal("equivalent labeled as cash")
		}
	}
}

func TestReceiptCLIFallsBackWhenSourceIsGone(t *testing.T) {
	t.Setenv("SAMCHEONPO_HOME", t.TempDir())
	path, err := filepath.Abs("../../testdata/claude/basic.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	prices, err := cost.Load("")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	r, err := analyzeFile(sources.File{Path: path, Agent: "claude"}, cfg, config.Hash(cfg), prices)
	if err != nil {
		t.Fatal(err)
	}
	r.Session.SourcePath = ""
	db, err := openDB()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.SaveAnalysis(r, 1, 2, nil); err != nil {
		t.Fatal(err)
	}
	db.Close()
	cmd := receiptCmd()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{r.Session.ID, "--format", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("receipt without a source transcript: %v", err)
	}
	var got receipt.Receipt
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatalf("JSON output includes non-JSON text: %v", err)
	}
	if got.Grade != r.Grade || got.Seal == "" {
		t.Errorf("stored fallback grade=%q seal=%q, want grade %q", got.Grade, got.Seal, r.Grade)
	}
}

func TestReceiptAndVerifyStoredAnalysisAfterSourceDeletion(t *testing.T) {
	t.Setenv("SAMCHEONPO_HOME", t.TempDir())
	data, err := os.ReadFile("../../testdata/claude/basic.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "source.jsonl")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	prices, err := cost.Load("")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	r, err := analyzeFile(sources.File{Path: path, Agent: "claude"}, cfg, config.Hash(cfg), prices)
	if err != nil {
		t.Fatal(err)
	}
	db, err := openDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sl, err := db.SaveAnalysis(r, 1, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	for _, share := range []bool{false, true} {
		cmd := receiptCmd()
		var output bytes.Buffer
		cmd.SetOut(&output)
		args := []string{r.Session.ID, "--format", "json", "--evidence"}
		if share {
			args = append(args, "--share")
		}
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		var got receipt.Receipt
		if err := json.Unmarshal(output.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Seal != sl.Short() || got.Total.Tokens != r.Totals.Tokens {
			t.Fatalf("receipt %+v", got)
		}
		want := cost.Measure(cost.ObserveSession(r.Session, r.PriceVersion), r.Totals.Micro, r.Totals.Tokens, r.Totals.UnpricedTokens)
		if got.Billing.UsageComplete != want.UsageComplete || got.Billing.UsageObserved != want.UsageObserved {
			t.Fatalf("billing %+v want %+v", got.Billing, want)
		}
	}
	verify := verifyCmd()
	var output bytes.Buffer
	verify.SetOut(&output)
	verify.SetArgs([]string{r.Session.ID})
	if err := verify.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "저장 분석 봉인 "+sl.Short()) {
		t.Fatal(output.String())
	}
	if _, err := db.Exec(`DELETE FROM receipt_snapshot`); err != nil {
		t.Fatal(err)
	}
	legacy := receiptCmd()
	output.Reset()
	legacy.SetOut(&output)
	legacy.SetArgs([]string{r.Session.ID, "--share"})
	if err := legacy.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "samcheonpo verify로") || !strings.Contains(output.String(), "구버전 저장 기록") {
		t.Fatal(output.String())
	}
	if err := verify.Execute(); err == nil {
		t.Fatal("legacy receipt verification succeeded")
	}
}

func TestReceiptStoredFallbackRejectsCorruptAnalysis(t *testing.T) {
	t.Setenv("SAMCHEONPO_HOME", t.TempDir())
	prices, err := cost.Load("")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	r, err := analyzeFile(sources.File{Path: "../../testdata/claude/basic.jsonl", Agent: "claude"}, cfg, config.Hash(cfg), prices)
	if err != nil {
		t.Fatal(err)
	}
	r.Session.SourcePath = ""
	db, err := openDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.SaveAnalysis(r, 1, 2, nil); err != nil {
		t.Fatal(err)
	}
	sl, _ := ledger.BuildSeal(r)
	sl.TotalTokens++
	sealed, err := json.Marshal(sl)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE session_seal SET seal=? WHERE session_id=?`, string(sealed), r.Session.ID); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []*cobra.Command{receiptCmd(), verifyCmd()} {
		var output bytes.Buffer
		cmd.SetOut(&output)
		cmd.SetErr(&output)
		cmd.SetArgs([]string{r.Session.ID})
		if err := cmd.Execute(); err == nil {
			t.Fatal("corrupt analysis accepted")
		}
	}
}

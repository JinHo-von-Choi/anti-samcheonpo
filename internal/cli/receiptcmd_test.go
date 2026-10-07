package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
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

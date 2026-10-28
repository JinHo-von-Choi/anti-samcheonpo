package contract

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProductionKeepsEarlierDigests: a contract without a production
// section serializes as before, so acceptances made before it existed stay
// valid; one with it parses and changes the digest.
func TestProductionKeepsEarlierDigests(t *testing.T) {
	base := "goal: x\ndone:\n  - id: t\n    check: go test ./...\n"
	c, errs := Parse([]byte(base))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	b, _ := json.Marshal(c)
	if strings.Contains(string(b), "production") {
		t.Fatalf("an absent production section is not serialized: %s", b)
	}
	p, errs := Parse([]byte(base + "production:\n  env_files: [.env.live]\n  hosts: [db.internal]\n  services: [app.service]\n  deploy_commands: [bash scripts/deploy.sh]\n"))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if p.Production == nil || p.Production.Hosts[0] != "db.internal" || p.ProductionEnvFiles()[0] != ".env.live" {
		t.Fatalf("parsed %+v", p.Production)
	}
	if AuthorityDigest(c) == AuthorityDigest(p) {
		t.Fatal("accepting production limits is part of what an acceptance authorizes")
	}
	if got := (*Contract)(nil).ProductionEnvFiles(); len(got) != 2 || got[0] != ".env.production" {
		t.Fatalf("defaults without a contract: %v", got)
	}
}

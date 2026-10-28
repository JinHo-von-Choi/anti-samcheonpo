package detect

import (
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
)

const glPipeline = `curl -s -H "PRIVATE-TOKEN: $T" "https://git.example/api/v4/projects/59/pipelines/1627"`

// TestDeployAfterFailedCI replays the Vellum sequence: CI read through the
// REST API failed, then the deploy script ran without a local check.
func TestDeployAfterFailedCI(t *testing.T) {
	b := newB(t, "배포까지 해", nil)
	b.write("server/src/main.rs", "h", []string{"a"}, []string{"b"})
	b.run("cargo test --workspace", 0, "test result: ok", 10*time.Minute)
	b.run(glPipeline, 0, `{"id":1627,"status":"failed"}`, time.Second)
	s := b.pre("bash scripts/deploy.sh")
	if s == nil || s.Rule != "s5.deploy_without_preflight" || s.Facts["kind"] != "ci_failed" {
		t.Fatalf("a deploy after a failed CI result is judged: %+v", s)
	}
	// CI named no failing test, so only a whole-suite run reproduces it
	b.write("web/src/Datasource.test.tsx", "h2", []string{"retry"}, nil)
	b.run("npx vitest run src/Datasource.test.tsx", 0, "1 passed", 5*time.Second)
	if s := b.pre("bash scripts/deploy.sh"); s == nil || s.Facts["kind"] != "ci_failed" {
		t.Fatalf("a selected test does not cover a failure nobody located: %+v", s)
	}
	b.run("npm test", 0, "212 passed", 6*time.Minute)
	if s := b.pre("bash scripts/deploy.sh"); s != nil && s.Rule == "s5.deploy_without_preflight" {
		t.Fatalf("a whole-suite run on a changed workspace reproduces the failure: %+v", s)
	}
}

func TestDeployNeedsACheckAfterTheLastChange(t *testing.T) {
	b := newB(t, "배포", nil)
	b.write("src/a.go", "h", []string{"a"}, nil)
	if s := b.pre("make deploy"); s == nil || s.Facts["kind"] != "unverified_change" {
		t.Fatalf("the first deploy also needs a passing check: %+v", s)
	}
	b.run("go test ./...", 0, "ok", time.Minute)
	if s := b.pre("make deploy"); s != nil {
		t.Fatalf("checked: %+v", s)
	}
	// a database restarted for tests is not a deploy
	b.write("src/a.go", "h2", []string{"b"}, nil)
	if s := b.pre("sudo systemctl restart postgresql"); s != nil && s.Rule == "s5.deploy_without_preflight" {
		t.Fatalf("service control is not a deploy: %+v", s)
	}
}

func TestDeployOfAContractService(t *testing.T) {
	c := &contract.Contract{Goal: "x", Production: &contract.Production{Services: []string{"vellum.service"}}}
	b := newB(t, "배포", c)
	b.write("src/a.go", "h", []string{"a"}, nil)
	if s := b.pre("sudo -n systemctl restart vellum.service"); s == nil || s.Rule != "s5.deploy_without_preflight" {
		t.Fatalf("restarting a production unit deploys the new build: %+v", s)
	}
}

const vellumRevert = "cd /w && set -a && source .env && set +a && psql \"${DATABASE_URL%%\\?*}\" <<'SQL'\nBEGIN;\nDELETE FROM vellum._sqlx_migrations WHERE version = 22;\nCOMMIT;\nSQL"

func TestMigrationHistoryWrite(t *testing.T) {
	b := newB(t, "배포", nil)
	if s := b.pre(vellumRevert); s == nil || s.Rule != "s3.migration_history_write" || s.Level != L1 {
		t.Fatalf("a hand-written migration history change is judged: %+v", s)
	}
	if s := b.pre(`psql postgres://app@127.0.0.1/app_test -c "DELETE FROM _sqlx_migrations"`); s != nil {
		t.Fatalf("a database on this machine named outright is a local one: %+v", s)
	}
	if s := b.pre(`sqlx migrate revert`); s != nil {
		t.Fatalf("the migration tool's own revert is a normal local step: %+v", s)
	}
}

func TestProductionWrite(t *testing.T) {
	prod := `set -a && source .env.production && set +a && psql "$DATABASE_URL" -c "UPDATE users SET plan='x'"`
	b := newB(t, "x", nil)
	if s := b.pre(prod); s == nil || s.Rule != "s3.production_write" || s.Level != L1 || s.Facts["where"] != ".env.production" {
		t.Fatalf("a write with the production settings is advised: %+v", s)
	}
	if s := b.pre(`set -a && source .env.production && set +a && psql postgres://app@localhost/app_test -c "UPDATE t SET a=1"`); s != nil {
		t.Fatalf("a command that names its own target does not use the production settings: %+v", s)
	}
	if s := b.pre(`set -a && source .env.production && set +a && psql "$DATABASE_URL" -c "SELECT count(*) FROM users"`); s != nil {
		t.Fatalf("a read is not a write: %+v", s)
	}
	c := &contract.Contract{Goal: "x", Production: &contract.Production{Hosts: []string{"db.internal"}}}
	g := newB(t, "x", c)
	s := g.pre(prod)
	if s == nil || s.Rule != RuleProductionGuard || s.Level != L3 {
		t.Fatalf("a contract that names production blocks the write: %+v", s)
	}
	if s := g.pre(`psql -h db.internal -c "DELETE FROM sessions"`); s == nil || s.Rule != RuleProductionGuard || s.Facts["where"] != "db.internal" {
		t.Fatalf("a contract host is production: %+v", s)
	}
	c.Production.AllowWrite = true
	if s := g.pre(prod); s != nil {
		t.Fatalf("allowed by the contract: %+v", s)
	}
}

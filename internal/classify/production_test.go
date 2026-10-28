package classify

import "testing"

func TestDeploy(t *testing.T) {
	services := []string{"vellum.service"}
	for cmd, want := range map[string]bool{
		"bash scripts/deploy.sh":                   true,
		"cd /w && bash scripts/deploy.sh --no-e2e": true,
		"./scripts/deploy.sh":                      true,
		"make deploy":                              true,
		"kubectl apply -f k8s/":                    true,
		"kubectl rollout restart deploy/api":       true,
		"helm upgrade api ./chart":                 true,
		"terraform apply -auto-approve":            true,
		"fly deploy":                               true,
		"vercel --prod":                            true,
		"sudo -n systemctl restart vellum.service": true,
		"sudo systemctl stop vellum":               true,
		"systemctl restart postgresql":             false, // not a production unit
		"systemctl status vellum.service":          false,
		"systemctl --user restart vellum.service":  false,
		"terraform plan":                           false,
		"kubectl apply --dry-run=client -f k8s/":   false,
		"helm template ./chart":                    false,
		"cat scripts/deploy.sh":                    false,
		"bash scripts/e2e_prod.sh":                 false,
		"sh -c 'bash scripts/deploy.sh'":           true,
		"git log --oneline -- scripts/deploy.sh":   false,
		"kubectl get pods":                         false,
		"bash -c \"echo deploying\"":               false,
	} {
		if got := Deploy(cmd, nil, services); got != want {
			t.Errorf("%q: %v want %v", cmd, got, want)
		}
	}
	if !Deploy("npm run ship -- --env prod", []string{"npm run ship"}, nil) {
		t.Error("a deploy command the contract names")
	}
}

func TestDataWrite(t *testing.T) {
	vellum := "cd /home/u/w && set -a && source .env && set +a && psql \"${DATABASE_URL%%\\?*}\" -v ON_ERROR_STOP=1 <<'SQL'\nBEGIN;\nDELETE FROM vellum._sqlx_migrations WHERE version = 22;\nCOMMIT;\nSQL"
	info := DataWrite(vellum)
	if !info.Write || !info.MigrationHistory || info.Explicit || info.Local || len(info.EnvFiles) != 1 || info.EnvFiles[0] != ".env" {
		t.Fatalf("vellum %+v", info)
	}
	for _, tc := range []struct {
		cmd                     string
		write, history, explict bool
		local                   bool
	}{
		{`psql -c "SELECT * FROM users"`, false, false, false, false},
		{`psql -c "SELECT 'DELETE FROM x'"`, false, false, false, false},
		{`psql -c "-- DROP TABLE x
SELECT 1"`, false, false, false, false},
		{`psql -h db.prod -c "UPDATE users SET a=1"`, true, false, true, false},
		{`psql postgres://app@127.0.0.1/app_test -c "TRUNCATE schema_migrations"`, true, true, true, true},
		{`mysql -e "ALTER TABLE t ADD c int"`, true, false, false, false},
		{`redis-cli -h cache FLUSHALL`, true, false, true, false},
		{`redis-cli GET key`, false, false, false, false},
		{`mongosh --eval 'db.users.deleteMany({})'`, true, false, false, false},
		{`mongosh --eval 'db.users.find()'`, false, false, false, false},
		{`sqlx migrate revert`, true, false, false, false},
		{`npx prisma migrate reset --force`, true, false, false, false},
		{`grep DELETE notes.txt`, false, false, false, false},
	} {
		got := DataWrite(tc.cmd)
		if got.Write != tc.write || got.MigrationHistory != tc.history || got.Explicit != tc.explict || got.Local != tc.local {
			t.Errorf("%q: %+v", tc.cmd, got)
		}
	}
}

func TestCIResultFromTheRESTAPI(t *testing.T) {
	gl := `curl -s -H "PRIVATE-TOKEN: $T" "https://git.example/api/v4/projects/59/pipelines/1627"`
	for _, tc := range []struct{ cmd, out, want string }{
		{gl, `{"id":1627,"status":"failed","ref":"main"}`, CIFailed},
		{gl, `{"id":1630,"status":"success"}`, CIPassed},
		{gl, `{"id":1631,"status":"running"}`, CIUnknown},
		{gl + ` | python3 -c "import json,sys; ..."`, "audit success\nweb failed\nserver success", CIFailed},
		{gl + ` | python3 -c "..."`, "audit success\nweb success\nserver success", CIPassed},
		{gl + ` | python3 -c "..."`, "audit success\nweb running", CIUnknown},
		{"python3 - <<'PY'\nreq = api('/projects/59/pipelines/1627/jobs')  # https://git.example/api/v4/\nPY", "FAILED JOB: 1848 server", CIFailed},
		{`curl -s https://api.github.com/repos/o/r/actions/runs?per_page=1`, `{"workflow_runs":[{"status":"completed","conclusion":"failure"}]}`, CIFailed},
		{`curl -s https://example.com/health`, "success", CIUnknown},
	} {
		if got := CIResult(tc.cmd, 0, tc.out); got != tc.want {
			t.Errorf("%q / %q: %s want %s", tc.cmd, tc.out, got, tc.want)
		}
	}
}

// TestCIResultFromGHFields covers run states gh prints as bare words, as
// in a wait loop: for r in A B; do echo "== $r $(gh run view $r --json
// conclusion --jq .conclusion)"; done.
func TestCIResultFromGHFields(t *testing.T) {
	loop := `until [ "$(gh run list --limit 2 --json status --jq '[.[]|select(.status=="completed")]|length')" = "2" ]; do sleep 30; done; for r in 40708489120 40708515892; do echo "== $r $(gh run view $r --json conclusion --jq .conclusion)"; done`
	for _, tc := range []struct{ out, want string }{
		{"== 40708489120 success\n== 40708515892 success", CIPassed},
		{"== 40708489120 success\n== 40708515892 failure", CIFailed},
		{"== 40708489120 success\n== 40708515892 in_progress", CIUnknown},
		{"", CIUnknown},
	} {
		if got := CIResult(loop, 0, tc.out); got != tc.want {
			t.Errorf("%q: %s want %s", tc.out, got, tc.want)
		}
	}
	if got := CIResult(`gh run list --limit 3 --json conclusion,headSha --jq '.[]|"\(.conclusion) \(.headSha)"'`, 0, "success 439c1a9\nsuccess 2a2a314"); got != CIPassed {
		t.Errorf("list: %s", got)
	}
	if got := CIRunID(`gh run view 40705689294 --log-failed | grep FAIL`); got != "40705689294" {
		t.Errorf("run id %q", got)
	}
}

package detect

import (
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
)

func TestFlakeMaskingVellumChanges(t *testing.T) {
	b := newB(t, "배포까지", nil)
	b.run("bash scripts/deploy.sh", 1, "e2e/team.spec.ts:40:3 › 팀 초대\n  Test timeout of 90000ms exceeded.", 3*time.Minute)
	b.verify("npx playwright test e2e/team.spec.ts", 1, []string{"e2e/team.spec.ts"})
	v := fired(b.write("web/e2e/team.spec.ts", "h1", []string{"  test.setTimeout(180_000);"}, []string{"  test.setTimeout(90_000);"}), "s5.flake_masking")
	if v == nil || v.Level != L1 || v.Facts["kind"] != "timeout" || v.Facts["timeout_to"] != 180000 {
		t.Fatalf("a raised time limit after the test failed on time: %+v", v)
	}
	v = fired(b.write("scripts/e2e_prod.sh", "h2", []string{`npx playwright test --config playwright.prod.config.ts --retries=1`}, []string{`npx playwright test --config playwright.prod.config.ts`}), "s5.flake_masking")
	if v == nil || v.Level != L1 || v.Facts["kind"] != "retries" {
		t.Fatalf("retries added to a test script after a timing failure: %+v", v)
	}
	// no failure of this file in the session: recorded, not advised
	v = fired(b.write("web/src/Datasource.test.tsx", "h3", []string{"  for (let attempt = 0; attempt < 3; attempt++) {", "    try { await openMenu(); break } catch {}"}, nil), "s5.flake_masking")
	if v == nil || v.Level != L0 || v.Facts["kind"] != "retry_loop" {
		t.Fatalf("a retry loop without a failure of the file is recorded only: %+v", v)
	}
}

func TestFlakeMaskingLeavesOtherChangesAlone(t *testing.T) {
	b := newB(t, "x", nil)
	b.verify("npx vitest run src/cells.test.tsx", 1, []string{"src/cells.test.tsx"})
	for name, added := range map[string][]string{
		"table cases": {"  for (const c of cases) {", "    expect(render(c.in)).toBe(c.out)", "  }"},
		"lower limit": {"  test.setTimeout(30_000);"},
	} {
		removed := []string(nil)
		if name == "lower limit" {
			removed = []string{"  test.setTimeout(60_000);"}
		}
		if v := fired(b.write("web/src/cells.test.tsx", "h-"+name, added, removed), "s5.flake_masking"); v != nil {
			t.Fatalf("%s: %+v", name, v)
		}
	}
	if v := fired(b.write("server/src/http.rs", "h4", []string{"const TIMEOUT: u64 = 60_000;"}, []string{"const TIMEOUT: u64 = 30_000;"}), "s5.flake_masking"); v != nil {
		t.Fatalf("a product time limit is not a test change: %+v", v)
	}
	c := &contract.Contract{Goal: "x", SimplifyTests: true}
	s := newB(t, "x", c)
	s.verify("npx vitest run src/cells.test.tsx", 1, []string{"src/cells.test.tsx"})
	if v := fired(s.write("web/src/cells.test.tsx", "h5", []string{"  retry: 2,"}, nil), "s5.flake_masking"); v != nil {
		t.Fatalf("a contract that allows simplifying tests: %+v", v)
	}
}

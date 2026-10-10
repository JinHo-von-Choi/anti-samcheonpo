package detect

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"testing"
)

func TestExplicitExternalFileDoesNotGrantItsParent(t *testing.T) {
	b := newB(t, "src/job.py와 /home/example/skills/job/SKILL.md를 수정해", nil)
	if in, known := b.e.InScope("/home/example/skills/job/SKILL.md"); !in || !known {
		t.Fatal("explicit file missing from guessed context")
	}
	if in, _ := b.e.InScope("/home/example/skills/job/other.py"); in {
		t.Fatal("one file granted its parent directory")
	}
	if in, _ := b.e.InScope("/home/example/skills/job/SKILL.md.bak"); in {
		t.Fatal("prefix accepted as exact file")
	}
	if inGuess(b.e.St.allowGuess, "/home/example/skills/job/SKILL.md/child.py") {
		t.Fatal("stale scope matching broadened exact file")
	}
	b.e.Retarget("src/next.py를 수정해")
	if in, _ := b.e.InScope("/home/example/skills/job/SKILL.md"); in {
		t.Fatal("old goal still granted external path")
	}
}

func TestExternalPathMentionCannotOverrideAcceptedScope(t *testing.T) {
	c := &contract.Contract{}
	c.Scope.Allow = []string{"src/**"}
	c.Scope.Protect = []string{"src/private.py"}
	b := newB(t, "/home/example/skills/job/SKILL.md와 src/private.py를 수정해", c)
	if in, _ := b.e.InScope("/home/example/skills/job/SKILL.md"); in {
		t.Fatal("guessed path overrode contract scope")
	}
}

func TestProtectedExternalReferenceIsNotGuessedPermission(t *testing.T) {
	b := newB(t, "src/job.py를 수정해. /home/example/private.py는 수정하지 마", nil)
	if in, _ := b.e.InScope("/home/example/private.py"); in {
		t.Fatal("negative reference guessed as allowed")
	}
}

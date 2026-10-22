package sockpath

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// pathVector is one row of vectors.json. The Rust transport reads the same
// file, so a change in one implementation that the other does not follow
// fails the other.
type pathVector struct {
	Home   string `json:"home"`
	Tmp    string `json:"tmp"`
	User   string `json:"user"`
	Expect string `json:"expect"`
}

type vectorSpec struct {
	Version int          `json:"version"`
	Vectors []pathVector `json:"vectors"`
}

func loadVectors(t *testing.T) vectorSpec {
	t.Helper()
	raw, err := os.ReadFile("vectors.json")
	if err != nil {
		t.Fatalf("벡터 읽기 실패: %v", err)
	}
	var spec vectorSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("벡터 해석 실패: %v", err)
	}
	if len(spec.Vectors) == 0 {
		t.Fatal("벡터가 없다")
	}
	return spec
}

func TestSocketPathVectors(t *testing.T) {
	for _, v := range loadVectors(t).Vectors {
		got := ForHome(v.Home, v.Tmp, v.User)
		if got != v.Expect {
			t.Errorf("home=%q tmp=%q user=%q: 경로 %q, 기대 %q", v.Home, v.Tmp, v.User, got, v.Expect)
		}
	}
}

// The limit is a byte limit, not a character limit. A socket path of
// exactly 100 bytes stays; 101 bytes moves to the short per-user path.
func TestSocketPathLimitCountsBytes(t *testing.T) {
	atLimit := "/h/" + strings.Repeat("x", 77)
	if len(atLimit)+len("/run/samcheonpo.sock") != maxLen {
		t.Fatalf("시험 경로 길이 %d가 경계가 아니다", len(atLimit)+len("/run/samcheonpo.sock"))
	}
	got := ForHome(atLimit, "/tmp", "1000")
	if got != atLimit+"/run/samcheonpo.sock" {
		t.Errorf("경계 길이 경로 %q가 대체 경로로 바뀌었다", got)
	}
	overLimit := "/h/" + strings.Repeat("x", 78)
	got = ForHome(overLimit, "/tmp", "1000")
	if got == overLimit+"/run/samcheonpo.sock" {
		t.Errorf("제한을 넘긴 경로 %q가 그대로 남았다", got)
	}
	if len(got) > maxLen {
		t.Errorf("대체 경로 길이 %d가 제한을 넘는다: %q", len(got), got)
	}
}

package dashboard

import (
	"bytes"
	"encoding/json"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/analyze"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/eval"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/notify"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func dashboardFixture(t *testing.T) (*Server, string) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "ledger.db")
	db, err := ledger.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	prices, _ := cost.Load("")
	session := &event.Session{ID: "fixture-session", Agent: "claude", Mode: "audit", FirstPrompt: "자료 조사", StartedAt: time.Now(), Events: []*event.Event{{Seq: 0, Kind: event.KindTool, Tool: event.ToolRead, Category: event.CatExplore, Paths: []string{"a"}, Text: "same"}, {Seq: 1, Kind: event.KindTool, Tool: event.ToolRead, Category: event.CatExplore, Paths: []string{"a"}, Text: "same"}, {Seq: 2, Kind: event.KindTool, Tool: event.ToolRead, Category: event.CatExplore, Paths: []string{"a"}, Text: "same"}}}
	result, err := analyze.Run(session, analyze.Options{Config: config.Default(), Prices: prices})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.SaveAnalysis(result, 0, 0, nil); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := New(Options{DBPath: path, LabelsDir: filepath.Join(root, "labels"), AccessCode: strings.Repeat("test", 16)})
	if err != nil {
		t.Fatal(err)
	}
	return s, root
}
func perform(s *Server, method, path, body string, authorized bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:18773"+path, bytes.NewBufferString(body))
	if method == http.MethodPost {
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://127.0.0.1:18773")
	}
	if authorized {
		r.AddCookie(&http.Cookie{Name: "samcheonpo_access", Value: s.AccessCode})
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func TestDashboardProtectsLedgerAndPersistsRealInterval(t *testing.T) {
	s, root := dashboardFixture(t)
	if w := perform(s, "GET", "/api/sessions", "", false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	login := perform(s, "POST", "/api/login", `{"code":"`+s.AccessCode+`"}`, false)
	if login.Code != 200 || !strings.Contains(login.Header().Get("Set-Cookie"), "HttpOnly") {
		t.Fatal(login.Code)
	}
	w := perform(s, "POST", "/api/label", `{"session":"fixture-session","labeler":"human-a","start":0,"end":2,"symptom":"S4","justified":"yes","outcome":"unknown","note":"same evidence"}`, true)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	ls, err := eval.LoadLabels(filepath.Join(root, "labels"))
	if err != nil || len(ls) != 1 || ls[0].Labeler != "human-a" || ls[0].Meta != nil {
		t.Fatalf("%+v %v", ls, err)
	}
	files, _ := filepath.Glob(filepath.Join(root, "labels", "human-a", "*"))
	stat, err := os.Stat(files[0])
	if err != nil || stat.Mode().Perm() != 0600 {
		t.Fatal("label privacy")
	}
	bad := perform(s, "POST", "/api/label", `{"session":"fixture-session","labeler":"../escape","start":0,"end":2,"symptom":"S4","justified":"yes","outcome":"unknown"}`, true)
	if bad.Code == 200 {
		t.Fatal("path traversal allowed")
	}
	bad = perform(s, "POST", "/api/label", `{"session":"fixture-session","labeler":"human-a","start":0,"end":99,"symptom":"S4","justified":"yes","outcome":"unknown"}`, true)
	if bad.Code == 200 {
		t.Fatal("unobserved interval accepted")
	}
}
func TestIndependentReviewDoesNotLeakDetectorOpinion(t *testing.T) {
	s, _ := dashboardFixture(t)
	normal := perform(s, "GET", "/api/session?id=fixture-session", "", true)
	review := perform(s, "GET", "/api/session?id=fixture-session&review=1", "", true)
	var a, b Detail
	if json.Unmarshal(normal.Body.Bytes(), &a) != nil || json.Unmarshal(review.Body.Bytes(), &b) != nil {
		t.Fatal("response")
	}
	if a.Assessment == nil || len(a.Findings) == 0 || b.Assessment != nil || len(b.Findings) != 0 || b.Live != nil || len(b.Events) != 3 {
		t.Fatalf("review leaks opinions: %+v", b)
	}
	for _, e := range b.Events {
		if e.Progress.Knowledge || len(e.Changes) > 0 {
			t.Fatal("derived signal leaked into independent review")
		}
	}
}
func TestDashboardCommandUsesExistingSessionAndRejectsCrossOrigin(t *testing.T) {
	s, _ := dashboardFixture(t)
	var id, name, arg string
	s.Command = func(i, n, a string) (string, error) { id, name, arg = i, n, a; return "direction updated", nil }
	s.HUD = func(string) (*notify.HUDState, error) {
		return &notify.HUDState{SessionID: "fixture-session", Requested: "latest request"}, nil
	}
	w := perform(s, "POST", "/api/command", `{"session":"fixture-session","name":"steer","arg":"새 목표 구현"}`, true)
	if w.Code != 200 || id != "fixture-session" || name != "steer" || arg != "새 목표 구현" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	r := httptest.NewRequest("POST", "http://127.0.0.1:18773/api/command", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://untrusted.example")
	r.AddCookie(&http.Cookie{Name: "samcheonpo_access", Value: s.AccessCode})
	out := httptest.NewRecorder()
	s.ServeHTTP(out, r)
	if out.Code != 403 {
		t.Fatal("cross origin allowed")
	}
	r = httptest.NewRequest("GET", "http://untrusted.example/api/sessions", nil)
	out = httptest.NewRecorder()
	s.ServeHTTP(out, r)
	if out.Code != 403 {
		t.Fatal("DNS rebinding allowed")
	}
	w = perform(s, "POST", "/api/command", `{"session":"fixture-session","name":"accept","arg":""}`, true)
	if w.Code == 200 {
		t.Fatal("unexpected authority action")
	}
}
func TestDemoCannotWriteOrCommand(t *testing.T) {
	s, err := New(Options{Demo: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/api/command", "/api/label"} {
		if w := perform(s, "POST", p, `{}`, false); w.Code != 403 {
			t.Fatal(w.Code)
		}
	}
}

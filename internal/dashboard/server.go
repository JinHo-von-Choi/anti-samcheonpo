// Package dashboard serves the local user interface over the existing ledger and daemon.
package dashboard

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/assessment"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/eval"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/notify"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/research"
)

//go:embed web/*
var assets embed.FS

type Options struct {
	DBPath, LabelsDir, AccessCode string
	Demo                          bool
	HUD                           func(string) (*notify.HUDState, error)
	Command                       func(string, string, string) (string, error)
	ReviewQueuePath               string
}
type Server struct {
	Options
	handler http.Handler
	queue   *eval.ReviewQueue
}
type Session struct {
	ID      string `json:"id"`
	Agent   string `json:"agent"`
	Goal    string `json:"goal"`
	Started string `json:"started"`
	Mode    string `json:"mode"`
}
type EventView struct {
	Seq      int64                     `json:"seq"`
	At       time.Time                 `json:"at"`
	Kind     event.Kind                `json:"kind"`
	Tool     string                    `json:"tool"`
	Category event.Category            `json:"category"`
	Summary  string                    `json:"summary"`
	Exit     *int                      `json:"exit,omitempty"`
	Progress event.WorkProgress        `json:"progress"`
	Changes  []event.RequirementChange `json:"changes,omitempty"`
}
type Detail struct {
	Session       Session             `json:"session"`
	Live          *notify.HUDState    `json:"live,omitempty"`
	Assessment    *assessment.Summary `json:"assessment,omitempty"`
	Events        []EventView         `json:"events"`
	Findings      []detect.Signal     `json:"findings,omitempty"`
	Work          intent.Work         `json:"work"`
	Research      *research.State     `json:"research,omitempty"`
	Review        bool                `json:"review"`
	Demo          bool                `json:"demo"`
	TotalEvents   int                 `json:"total_events"`
	Page          int                 `json:"page"`
	Pages         int                 `json:"pages"`
	WindowLimited bool                `json:"window_limited"`
}

func New(opts Options) (*Server, error) {
	if !opts.Demo && (opts.DBPath == "" || len(opts.AccessCode) < 32) {
		return nil, errors.New("원장 경로와 접근 코드가 필요하다")
	}
	s := &Server{Options: opts}
	if opts.ReviewQueuePath != "" {
		if opts.Demo {
			return nil, errors.New("데모에는 실제 검토 큐를 연결할 수 없다")
		}
		q, err := eval.LoadReviewQueue(opts.ReviewQueuePath)
		if err != nil {
			return nil, err
		}
		s.queue = &q
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/login", s.login)
	mux.HandleFunc("/api/sessions", s.sessions)
	mux.HandleFunc("/api/session", s.detail)
	mux.HandleFunc("/api/label", s.label)
	mux.HandleFunc("/api/command", s.command)
	mux.HandleFunc("/api/review-queue", s.reviewQueue)
	mux.HandleFunc("/api/info", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, map[string]any{"demo": opts.Demo}) })
	web, _ := fs.Sub(assets, "web")
	mux.Handle("/", http.FileServer(http.FS(web)))
	s.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.Demo {
			host, _, err := net.SplitHostPort(r.Host)
			ip := net.ParseIP(host)
			if err != nil || host != "localhost" && (ip == nil || !ip.IsLoopback()) {
				http.Error(w, "허용되지 않는 호스트", http.StatusForbidden)
				return
			}
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'")
		if r.Method == http.MethodPost && (r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Origin") != "http://"+r.Host && r.Header.Get("Origin") != "https://"+r.Host) {
			http.Error(w, "같은 화면에서 JSON 요청으로 실행해 주세요", http.StatusForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/info" && r.URL.Path != "/api/login" && !s.Demo {
			cookie, err := r.Cookie("samcheonpo_access")
			if err != nil || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(s.AccessCode)) != 1 {
				http.Error(w, "접근 코드 확인 필요", http.StatusUnauthorized)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
	return s, nil
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }
func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		return
	}
}
func readJSON(w http.ResponseWriter, r *http.Request, value any) error {
	if r.Method != http.MethodPost {
		return errors.New("POST 요청 필요")
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return errors.New("중복 JSON")
	}
	return nil
}
func fail(w http.ResponseWriter, err error) { http.Error(w, err.Error(), http.StatusBadRequest) }
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
	}
	if err := readJSON(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	if !s.Demo && subtle.ConstantTimeCompare([]byte(in.Code), []byte(s.AccessCode)) != 1 {
		http.Error(w, "접근 코드가 맞지 않습니다", http.StatusUnauthorized)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "samcheonpo_access", Value: s.AccessCode, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: 8 * 60 * 60})
	writeJSON(w, map[string]bool{"ok": true})
}
func (s *Server) sessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET 요청 필요", 405)
		return
	}
	if s.Demo {
		writeJSON(w, []Session{demoDetail().Session})
		return
	}
	db, err := ledger.OpenReadOnly(s.DBPath)
	if err != nil {
		fail(w, err)
		return
	}
	defer db.Close()
	rows, err := db.QueryContext(r.Context(), `SELECT id,agent,COALESCE(first_prompt_summary,''),COALESCE(started_at,''),mode FROM session ORDER BY started_at DESC LIMIT 100`)
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		var v Session
		if err := rows.Scan(&v.ID, &v.Agent, &v.Goal, &v.Started, &v.Mode); err != nil {
			fail(w, err)
			return
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, out)
}
func (s *Server) load(id string, review bool) (Detail, []*event.Event, error) {
	var d Detail
	d.Review = review
	d.Demo = s.Demo
	if s.Demo {
		d = demoDetail()
		d.Review = review
		if review {
			d.Assessment = nil
			d.Findings = nil
			d.Live = nil
		}
		return d, nil, nil
	}
	db, err := ledger.OpenReadOnly(s.DBPath)
	if err != nil {
		return d, nil, err
	}
	defer db.Close()
	if err = db.QueryRow(`SELECT id,agent,COALESCE(first_prompt_summary,''),COALESCE(started_at,''),mode FROM session WHERE id=?`, id).Scan(&d.Session.ID, &d.Session.Agent, &d.Session.Goal, &d.Session.Started, &d.Session.Mode); err != nil {
		return d, nil, err
	}
	if _, revision, _, err := db.SessionIntent(d.Session.Agent, id); err == nil {
		d.Session.Goal = revision.Goal
	}
	events, err := db.EventsOf(id)
	if err != nil {
		return d, nil, err
	}
	d.Work = intent.DescribeWork(d.Session.Goal, events)
	visible := events
	if len(visible) > 500 {
		visible = visible[len(visible)-500:]
		d.WindowLimited = true
	}
	d.Events = []EventView{}
	for _, e := range visible {
		summary := e.Summary
		if len(summary) > 512 {
			summary = summary[:512]
		}
		v := EventView{Seq: e.Seq, At: e.TS, Kind: e.Kind, Tool: e.Tool, Category: e.Category, Summary: summary, Exit: e.ExitCode}
		if !review {
			v.Progress = e.WorkProgress
			v.Changes = e.RequirementChanges
		}
		d.Events = append(d.Events, v)
	}
	if !review {
		if intent.ResearchRequest(d.Session.Goal) {
			tracker := research.New(d.Session.Goal, "recorded")
			for _, e := range events {
				tracker.Observe(e)
			}
			state := tracker.Snapshot()
			d.Research = &state
		}
		vs, err := db.VerdictsOf(id)
		if err != nil {
			return d, nil, err
		}
		channels, err := db.DeliveryChannels(id)
		if err != nil {
			return d, nil, err
		}
		a := assessment.BuildRecorded(events, vs, channels)
		d.Assessment = &a
		d.Findings = vs
		if s.HUD != nil {
			if h, err := s.HUD(id); err == nil && h != nil && h.SessionID == id {
				d.Live = h
				d.Session.Goal = h.Requested
				if h.Work != nil {
					d.Work = *h.Work
				}
			}
		}
	}
	return d, events, nil
}
func (s *Server) detail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET 요청 필요", 405)
		return
	}
	review := r.URL.Query().Get("review") == "1"
	d, events, err := s.load(r.URL.Query().Get("id"), review)
	if err != nil {
		fail(w, err)
		return
	}
	page := 0
	if value := r.URL.Query().Get("page"); value != "" {
		page, err = strconv.Atoi(value)
		if err != nil || page < 0 || page > 1000000 {
			fail(w, errors.New("페이지 확인 필요"))
			return
		}
	}
	if s.Demo {
		d.TotalEvents = len(d.Events)
		d.Pages = 1
		d.Page = 0
	} else {
		visible := []EventView{}
		for _, e := range events {
			if review && e.Category == event.CatWatch {
				continue
			}
			summary := e.Summary
			if len(summary) > 512 {
				summary = summary[:512]
			}
			v := EventView{Seq: e.Seq, At: e.TS, Kind: e.Kind, Tool: e.Tool, Category: e.Category, Summary: summary, Exit: e.ExitCode}
			if !review {
				v.Progress = e.WorkProgress
				v.Changes = e.RequirementChanges
			}
			visible = append(visible, v)
		}
		d.TotalEvents = len(visible)
		d.Pages = max(1, (len(visible)+99)/100)
		d.Page = min(page, d.Pages-1)
		start := min(d.Page*100, len(visible))
		end := min(start+100, len(visible))
		d.Events = visible[start:end]
		d.WindowLimited = d.Pages > 1
	}
	writeJSON(w, d)
}

var labelerPattern = regexp.MustCompile(`^[\p{L}\p{N}_-]{1,64}$`)

func (s *Server) label(w http.ResponseWriter, r *http.Request) {
	if s.Demo {
		http.Error(w, "데모는 저장하지 않습니다", http.StatusForbidden)
		return
	}
	var in struct {
		Session     string `json:"session"`
		Labeler     string `json:"labeler"`
		Start       int64  `json:"start"`
		End         int64  `json:"end"`
		Symptom     string `json:"symptom"`
		Justified   string `json:"justified"`
		Outcome     string `json:"outcome"`
		Note        string `json:"note"`
		QueueDigest string `json:"queue_digest"`
	}
	if err := readJSON(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	if !labelerPattern.MatchString(in.Labeler) || in.Start < 0 || in.End < in.Start || len(in.Note) > 2000 {
		fail(w, errors.New("라벨러·구간·메모 확인 필요"))
		return
	}
	if in.Symptom != "none" && (len(in.Symptom) != 2 || in.Symptom[0] != 'S' || in.Symptom[1] < '1' || in.Symptom[1] > '8') {
		fail(w, errors.New("증상은 S1~S8 또는 none"))
		return
	}
	if in.Justified != "yes" && in.Justified != "no" && in.Justified != "unsure" || in.Outcome != "resolved" && in.Outcome != "unresolved" && in.Outcome != "unknown" {
		fail(w, errors.New("타당성·결과 값 확인 필요"))
		return
	}
	d, events, err := s.load(in.Session, true)
	if err != nil {
		fail(w, err)
		return
	}
	first, last := false, false
	for _, e := range events {
		first = first || e.Seq == in.Start
		last = last || e.Seq == in.End
	}
	if !first || !last {
		fail(w, errors.New("관측에 없는 구간"))
		return
	}
	if in.QueueDigest != "" {
		if s.queue == nil {
			fail(w, errors.New("연결된 고정 검토 큐가 없다"))
			return
		}
		matched := false
		for _, item := range s.queue.Items {
			if item.Session == in.Session && item.Start == in.Start && item.End == in.End && item.SourceDigest == eval.ReviewSourceDigest(events) {
				if item.NativeSourceSHA256 != "" {
					db, e := ledger.OpenReadOnly(s.DBPath)
					if e != nil {
						fail(w, e)
						return
					}
					var source string
					e = db.QueryRow(`SELECT COALESCE(source_path,'') FROM session WHERE id=?`, in.Session).Scan(&source)
					db.Close()
					if e != nil {
						fail(w, e)
						return
					}
					sha, e := eval.NativeSourceDigest(source)
					if e != nil || sha != item.NativeSourceSHA256 {
						fail(w, errors.New("고정된 원본 자료가 변경되었거나 접근할 수 없음"))
						return
					}
				}
				matched = true
				break
			}
		}
		if in.QueueDigest != s.queue.Digest || !matched {
			fail(w, errors.New("고정된 검토 구간 또는 원장이 변경됨"))
			return
		}
	}
	l := eval.Label{Session: d.Session.ID, Labeler: in.Labeler, Start: in.Start, End: in.End, Symptom: in.Symptom, Justified: in.Justified, Outcome: in.Outcome, Note: in.Note}
	l.ReviewSourceDigest = eval.ReviewSourceDigest(events)
	l.ReviewQueueDigest = in.QueueDigest
	b, err := json.Marshal(l)
	if err != nil {
		fail(w, err)
		return
	}
	unlock, err := eval.LockLabels(s.LabelsDir)
	if err != nil {
		if errors.Is(err, eval.ErrLabelsBusy) {
			http.Error(w, err.Error(), http.StatusConflict)
		} else {
			fail(w, err)
		}
		return
	}
	defer unlock()
	// CLI legacy files may use a different filename. A reviewer still has
	// only one vote for the same observed interval across both interfaces.
	if all, err := eval.LoadLabels(s.LabelsDir); err == nil {
		for _, old := range all {
			if old.Meta == nil && old.Session == in.Session && old.Labeler == in.Labeler && old.Start == in.Start && old.End == in.End {
				fail(w, errors.New("이 검토자는 같은 구간을 이미 제출했습니다"))
				return
			}
		}
	} else if !os.IsNotExist(err) {
		fail(w, err)
		return
	}
	path := filepath.Join(s.LabelsDir, in.Labeler)
	if err = os.MkdirAll(path, 0700); err != nil {
		fail(w, err)
		return
	}
	filePath := filepath.Join(path, hex.EncodeToString([]byte(d.Session.ID))+".jsonl")
	raw, err := os.ReadFile(filePath)
	if err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			var old eval.Label
			if strings.TrimSpace(line) == "" {
				continue
			}
			if json.Unmarshal([]byte(line), &old) != nil {
				fail(w, errors.New("기존 라벨 파일을 확인할 수 없다"))
				return
			}
			if old.Meta == nil && old.Start == in.Start && old.End == in.End {
				fail(w, errors.New("이 검토자는 같은 구간을 이미 제출했습니다"))
				return
			}
		}
	} else if !os.IsNotExist(err) {
		fail(w, err)
		return
	}
	// Publish whole JSONL snapshots so concurrent readers never see a
	// partially appended row. The common lock covers duplicate checking too.
	if len(raw) > 0 && raw[len(raw)-1] != '\n' {
		raw = append(raw, '\n')
	}
	raw = append(raw, b...)
	raw = append(raw, '\n')
	if err := eval.WriteLabelFile(filePath, raw); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "reviewed_entire_session": false})
}
func (s *Server) command(w http.ResponseWriter, r *http.Request) {
	if s.Demo {
		http.Error(w, "데모는 실행하지 않습니다", 403)
		return
	}
	var in struct {
		Session string `json:"session"`
		Name    string `json:"name"`
		Arg     string `json:"arg"`
	}
	if err := readJSON(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	if in.Name != "keep" && in.Name != "steer" && in.Name != "summary" {
		fail(w, errors.New("지원하지 않는 화면 동작"))
		return
	}
	if in.Name == "keep" && in.Arg != "now" && in.Arg != "normal" || in.Name == "steer" && (strings.TrimSpace(in.Arg) == "" || len(in.Arg) > 4096) {
		fail(w, errors.New("동작 인자 확인 필요"))
		return
	}
	if _, _, err := s.load(in.Session, true); err != nil {
		fail(w, err)
		return
	}
	if s.Command == nil {
		fail(w, errors.New("실행 중 데몬 연결 없음"))
		return
	}
	if s.HUD == nil {
		fail(w, errors.New("대상 실행 세션 확인 불가"))
		return
	}
	h, err := s.HUD(in.Session)
	if err != nil || h == nil || h.SessionID != in.Session {
		fail(w, errors.New("대상 실행 세션 확인 불가; 다른 세션에는 명령을 전달하지 않습니다"))
		return
	}
	message, err := s.Command(in.Session, in.Name, in.Arg)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, map[string]string{"message": message})
}
func AccessCode(path string) (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	code := hex.EncodeToString(raw[:])
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", fmt.Errorf("새 접근 코드 파일 경로가 필요합니다: %w", err)
	}
	_, err = f.WriteString(code + "\n")
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return code, err
}

// Package uicheck presses the click targets of a page in a headless browser
// and reports which of them do nothing. It speaks the DevTools protocol over
// the browser's debugging pipe, so it needs a browser and no other library.
package uicheck

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lang"
)

// What pressing a target did.
const (
	EffectChanged  = "changed"  // the page changed
	EffectNavigate = "navigate" // the browser went to another address
	EffectLink     = "link"     // leaves the page; recorded, not followed
	EffectNone     = "none"     // nothing happened
	EffectSkipped  = "skipped"  // the label reads as irreversible
	EffectError    = "error"    // the target could not be pressed
)

// Target is one click target and what pressing it did.
type Target struct {
	Tag    string `json:"tag"`
	Label  string `json:"label"`
	Href   string `json:"href,omitempty"`
	Effect string `json:"effect"`
}

// Report is the result for one page.
type Report struct {
	URL     string   `json:"url"`
	Targets []Target `json:"targets"`
	// Repeated lists labels offered three or more times with one destination.
	Repeated []string `json:"repeated,omitempty"`
	// Order is what a reader meets from the top of the screen.
	Order []string `json:"order"`
	// OutOfOrder counts targets whose document position disagrees with
	// where the screen shows them, which keyboard users meet in the wrong
	// sequence.
	OutOfOrder int `json:"out_of_order"`
}

// Dead returns the targets that did nothing.
func (r Report) Dead() []Target {
	var out []Target
	for _, t := range r.Targets {
		if t.Effect == EffectNone {
			out = append(out, t)
		}
	}
	return out
}

// irreversibleRe names actions that are not pressed even on a local page.
var irreversibleRe = regexp.MustCompile(`(?i)삭제|지우기|제거|초기화|결제|구매|탈퇴|로그아웃|전송|보내기|\b(delete|remove|reset|purge|pay|buy|checkout|logout|log out|sign out|send|submit)\b`)

const selector = `a[href],button,[role=button],input[type=submit],input[type=button],[onclick]`

const visible = `(e=>{const r=e.getBoundingClientRect();const s=getComputedStyle(e);return r.width>0&&r.height>0&&s.visibility!=='hidden'&&s.display!=='none'&&!e.disabled})`

const collect = `JSON.stringify((()=>{
const vis=` + visible + `;
const label=e=>(e.innerText||e.value||e.getAttribute('aria-label')||e.title||'').trim().replace(/\s+/g,' ').slice(0,60);
const targets=[...document.querySelectorAll('` + selector + `')].filter(vis);
const out=targets.map((e,i)=>({index:i,tag:e.tagName.toLowerCase(),label:label(e),href:e.tagName==='A'?e.getAttribute('href'):'',blank:e.target==='_blank',y:Math.round(e.getBoundingClientRect().top+scrollY),x:Math.round(e.getBoundingClientRect().left)}));
const items=[...document.querySelectorAll('h1,h2,h3,label,'+'` + selector + `')].filter(vis).map(e=>({t:e.tagName.toLowerCase(),l:label(e),y:e.getBoundingClientRect().top+scrollY,x:e.getBoundingClientRect().left}));
items.sort((a,b)=>Math.abs(a.y-b.y)<4?a.x-b.x:a.y-b.y);
return {targets:out,order:items.map(i=>i.t+': '+i.l).slice(0,40)};
})())`

const press = `(async()=>{
const vis=` + visible + `;
const e=[...document.querySelectorAll('` + selector + `')].filter(vis)[%d];
if(!e)return 'missing';
let changed=false;
const o=new MutationObserver(()=>{changed=true});
o.observe(document.documentElement,{subtree:true,childList:true,attributes:true,characterData:true});
const before=location.href.replace(/#$/,'');
e.click();
await new Promise(r=>setTimeout(r,250));
o.disconnect();
if(location.href.replace(/#$/,'')!==before)return 'navigate';
return changed?'changed':'none';
})()`

// FindBrowser returns a Chromium-family browser, or "" when there is none.
func FindBrowser() string {
	if p := os.Getenv("SAMCHEONPO_BROWSER"); p != "" {
		return p
	}
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome", "msedge"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	const mac = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	if _, err := os.Stat(mac); err == nil {
		return mac
	}
	return ""
}

// Local reports an address whose targets may be pressed: a file or this
// machine. Pressing buttons on a site somebody else runs is not a check.
func Local(address string) bool {
	u, err := url.Parse(address)
	if err != nil {
		return false
	}
	if u.Scheme == "file" {
		return true
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	if u.Hostname() == "localhost" {
		return true
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback()
}

// Check loads a page and presses each click target on a fresh load.
func Check(ctx context.Context, browser, address string) (Report, error) {
	if !Local(address) {
		return Report{}, errors.New(lang.T("파일 또는 이 컴퓨터의 주소만 검사한다"))
	}
	c, err := start(ctx, browser)
	if err != nil {
		return Report{}, err
	}
	defer c.close()
	var page struct {
		TargetID string `json:"targetId"`
	}
	if err := c.call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank"}, &page); err != nil {
		return Report{}, err
	}
	var attached struct {
		SessionID string `json:"sessionId"`
	}
	if err := c.call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": page.TargetID, "flatten": true}, &attached); err != nil {
		return Report{}, err
	}
	s := attached.SessionID
	if err := c.load(ctx, s, address); err != nil {
		return Report{}, err
	}
	raw, err := c.eval(ctx, s, collect, false)
	if err != nil {
		return Report{}, err
	}
	var seen struct {
		Targets []struct {
			Index int    `json:"index"`
			Tag   string `json:"tag"`
			Label string `json:"label"`
			Href  string `json:"href"`
			Blank bool   `json:"blank"`
			Y     int    `json:"y"`
			X     int    `json:"x"`
		} `json:"targets"`
		Order []string `json:"order"`
	}
	if err := json.Unmarshal([]byte(raw), &seen); err != nil {
		return Report{}, fmt.Errorf("page result: %w", err)
	}
	r := Report{URL: address, Order: seen.Order}
	base, _ := url.Parse(address)
	counts := map[string]int{}
	for i, t := range seen.Targets {
		out := Target{Tag: t.Tag, Label: t.Label, Href: t.Href}
		key := t.Label + "\x00" + t.Href
		if t.Label != "" {
			counts[key]++
		}
		if i > 0 && (seen.Targets[i-1].Y-t.Y > 8) {
			r.OutOfOrder++
		}
		switch {
		case irreversibleRe.MatchString(t.Label):
			out.Effect = EffectSkipped
		case leaves(base, t.Href, t.Blank):
			out.Effect = EffectLink
		default:
			out.Effect = c.pressTarget(ctx, s, address, t.Index)
		}
		r.Targets = append(r.Targets, out)
	}
	for key, n := range counts {
		if n >= 3 {
			r.Repeated = append(r.Repeated, strings.SplitN(key, "\x00", 2)[0])
		}
	}
	return r, nil
}

// leaves reports a link that opens elsewhere, which is recorded as having a
// destination without being followed.
func leaves(base *url.URL, href string, blank bool) bool {
	if href == "" || strings.HasPrefix(href, "#") || strings.HasPrefix(strings.ToLower(href), "javascript:") {
		return false
	}
	if blank {
		return true
	}
	u, err := base.Parse(href)
	return err == nil && (u.Scheme != base.Scheme || u.Host != base.Host)
}

func (c *conn) pressTarget(ctx context.Context, session, address string, index int) string {
	if err := c.load(ctx, session, address); err != nil {
		return EffectError
	}
	got, err := c.eval(ctx, session, fmt.Sprintf(press, index), true)
	switch {
	case err != nil && strings.Contains(err.Error(), "navigat"), err != nil && strings.Contains(err.Error(), "context"):
		return EffectNavigate
	case err != nil, got == "missing":
		return EffectError
	}
	return got
}

// conn is a browser process and its debugging pipe.
type conn struct {
	cmd     *exec.Cmd
	dir     string
	in      *os.File
	mu      sync.Mutex
	next    int
	waiting map[int]chan reply
}

type reply struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func start(ctx context.Context, browser string) (*conn, error) {
	dir, err := os.MkdirTemp("", "samcheonpo-uicheck-")
	if err != nil {
		return nil, err
	}
	toBrowser, write, err := os.Pipe()
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	read, fromBrowser, err := os.Pipe()
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	cmd := exec.CommandContext(ctx, browser, "--headless=new", "--disable-gpu", "--no-first-run", "--no-default-browser-check", "--remote-debugging-pipe", "--user-data-dir="+dir, "about:blank")
	cmd.ExtraFiles = []*os.File{toBrowser, fromBrowser}
	if err := cmd.Start(); err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("%s: %w", browser, err)
	}
	toBrowser.Close()
	fromBrowser.Close()
	c := &conn{cmd: cmd, dir: dir, in: write, waiting: map[int]chan reply{}}
	go c.readLoop(read)
	return c, nil
}

func (c *conn) readLoop(r *os.File) {
	defer r.Close()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<16), 1<<26)
	sc.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		for i, b := range data {
			if b == 0 {
				return i + 1, data[:i], nil
			}
		}
		if atEOF {
			return len(data), nil, nil
		}
		return 0, nil, nil
	})
	for sc.Scan() {
		var m struct {
			ID int `json:"id"`
			reply
		}
		if json.Unmarshal(sc.Bytes(), &m) != nil || m.ID == 0 {
			continue
		}
		c.mu.Lock()
		ch := c.waiting[m.ID]
		delete(c.waiting, m.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- m.reply
		}
	}
}

func (c *conn) call(ctx context.Context, session, method string, params map[string]any, out any) error {
	ch := make(chan reply, 1)
	c.mu.Lock()
	c.next++
	id := c.next
	c.waiting[id] = ch
	c.mu.Unlock()
	msg := map[string]any{"id": id, "method": method, "params": params}
	if session != "" {
		msg["sessionId"] = session
	}
	b, _ := json.Marshal(msg)
	if _, err := c.in.Write(append(b, 0)); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(20 * time.Second):
		return fmt.Errorf("%s: no response", method)
	case r := <-ch:
		if r.Error != nil {
			return errors.New(r.Error.Message)
		}
		if out != nil {
			return json.Unmarshal(r.Result, out)
		}
		return nil
	}
}

func (c *conn) eval(ctx context.Context, session, expr string, await bool) (string, error) {
	var res struct {
		Result struct {
			Value any `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	err := c.call(ctx, session, "Runtime.evaluate", map[string]any{"expression": expr, "awaitPromise": await, "returnByValue": true}, &res)
	if err != nil {
		return "", err
	}
	if res.ExceptionDetails != nil {
		return "", errors.New(res.ExceptionDetails.Text)
	}
	return fmt.Sprint(res.Result.Value), nil
}

func (c *conn) load(ctx context.Context, session, address string) error {
	if err := c.call(ctx, session, "Page.navigate", map[string]any{"url": address}, nil); err != nil {
		return err
	}
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if s, err := c.eval(ctx, session, "document.readyState", false); err == nil && s == "complete" {
			time.Sleep(100 * time.Millisecond)
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return errors.New("page did not finish loading")
}

func (c *conn) close() {
	_ = c.in.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	_ = c.cmd.Wait()
	_ = os.RemoveAll(c.dir)
}

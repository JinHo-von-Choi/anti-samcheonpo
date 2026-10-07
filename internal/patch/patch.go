// Package patch reads the apply_patch format used by Codex (and accepted by
// other agents) into event write fields.
package patch

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lazyre"
)

var fileRe = lazyre.New(`^\*\*\* (Add|Update|Delete) File: (.+)$`)

// HashContent hashes file content (first 16 bytes of SHA-256, hex).
func HashContent(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:16])
}

// Apply fills ev from an apply_patch body; rel maps a path to the project
// relative form.
func Apply(ev *event.Event, body string, rel func(string) string) {
	ev.Tool = event.ToolEdit
	if ev.WriteHashes == nil {
		ev.WriteHashes = map[string]string{}
	}
	if ev.AddedLines == nil {
		ev.AddedLines = map[string]int{}
	}
	if ev.RemovedLines == nil {
		ev.RemovedLines = map[string]int{}
	}
	var cur *event.PatchFile
	var kind string
	var lines []string
	finish := func() {
		if cur == nil {
			return
		}
		rp := cur.Path
		switch kind {
		case "Add":
			ev.WriteHashes[rp] = HashContent(strings.Join(cur.Added, "\n"))
			ev.Created = append(ev.Created, rp)
			ev.Tool = event.ToolWrite
		case "Delete":
			ev.WriteHashes[rp] = "deleted"
			ev.Deleted = append(ev.Deleted, rp)
			cur.Deleted = true
		default:
			ev.WriteHashes[rp] = "patch:" + fp.Hash(rp, strings.Join(lines, "\n"))
			ev.Edits = append(ev.Edits, event.EditRef{Path: rp, OldHash: HashContent(strings.Join(cur.Removed, "\n")), NewHash: HashContent(strings.Join(cur.Added, "\n"))})
		}
		ev.AddedLines[rp] = len(cur.Added)
		ev.RemovedLines[rp] = len(cur.Removed)
		ev.Patch = append(ev.Patch, *cur)
		ev.Paths = append(ev.Paths, rp)
	}
	for _, ln := range strings.Split(body, "\n") {
		if m := fileRe.FindStringSubmatch(ln); m != nil {
			finish()
			cur = &event.PatchFile{Path: rel(strings.TrimSpace(m[2]))}
			kind = m[1]
			lines = nil
			continue
		}
		if cur == nil || strings.HasPrefix(ln, "*** ") {
			continue
		}
		lines = append(lines, ln)
		if strings.HasPrefix(ln, "+") {
			cur.Added = append(cur.Added, ln[1:])
		} else if strings.HasPrefix(ln, "-") {
			cur.Removed = append(cur.Removed, ln[1:])
		}
	}
	finish()
	ev.Summary = "edit " + strings.Join(ev.Paths, ",")
}

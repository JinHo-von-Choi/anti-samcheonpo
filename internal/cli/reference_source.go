package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/eval"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/sources"
	"io"
	"os"
	"path/filepath"
)

// Only immutable, preregistered source bytes supply missing native arguments.
// An unavailable or mismatched source stays unknown; no guessed call identity.
func nativeReferenceEvents(db *ledger.DB, item eval.ReviewItem, recorded []*event.Event) ([]*event.Event, string) {
	if item.NativeSourceSHA256 == "" {
		return recorded, "native_source_not_frozen"
	}
	if item.Agent != "claude" && item.Agent != "codex" {
		return recorded, "native_format_unsupported"
	}
	var path string
	if err := db.QueryRow(`SELECT COALESCE(source_path,'') FROM session WHERE id=?`, item.Session).Scan(&path); err != nil {
		return recorded, "native_source_missing"
	}
	f, err := os.Open(path)
	if err != nil {
		return recorded, "native_source_missing"
	}
	raw, err := io.ReadAll(io.LimitReader(f, (32<<20)+1))
	f.Close()
	if err != nil || len(raw) > 32<<20 {
		return recorded, "native_source_unreadable"
	}
	h := sha256.Sum256(raw)
	if hex.EncodeToString(h[:]) != item.NativeSourceSHA256 {
		return recorded, "native_source_changed"
	}
	temp, err := os.MkdirTemp("", "samcheonpo-reference-")
	if err != nil {
		return recorded, "native_snapshot_unavailable"
	}
	defer os.RemoveAll(temp)
	copyPath := filepath.Join(temp, "source.jsonl")
	if os.WriteFile(copyPath, raw, 0600) != nil {
		return recorded, "native_snapshot_unavailable"
	}
	parsed, err := sources.Parse(sources.File{Path: copyPath, Agent: item.Agent})
	if err != nil {
		return recorded, "native_parse_failed"
	}
	if len(parsed.Events) != len(recorded) {
		return recorded, "native_sequence_mismatch"
	}
	for i, e := range parsed.Events {
		old := recorded[i]
		if e.Seq != old.Seq || e.Kind != old.Kind || e.Tool != old.Tool || e.CmdFP != old.CmdFP {
			return recorded, "native_sequence_mismatch"
		}
		if e.Tool == event.ToolRead || e.Tool == event.ToolWrite || e.Tool == event.ToolEdit {
			if len(e.Paths) != len(old.Paths) {
				return recorded, "native_sequence_mismatch"
			}
			for j, p := range e.Paths {
				if p != old.Paths[j] {
					return recorded, "native_sequence_mismatch"
				}
			}
		}
	}
	return parsed.Events, "native_frozen_bytes_verified"
}

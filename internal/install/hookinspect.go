package install

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// HookInstallation describes a recorded hooks.json connection without
// executing any hook.
type HookInstallation struct {
	Path       string
	Installed  bool
	Missing    []string // events whose recorded entry is gone, changed or doubled
	Executable bool
	// Off is true when the file's hooks do not run because its top level
	// does not say "enabled": true (dialects with RootEnabled only).
	Off bool
}

// InspectHooks compares a dialect's hooks.json with what InstallHooks recorded.
func InspectHooks(d HookDialect) (HookInstallation, error) {
	var result HookInstallation
	raw, err := os.ReadFile(hookStatePath(d.Agent))
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return result, errors.New(d.Agent + " 설치 기록을 읽을 수 없다")
	}
	var state hookState
	if json.Unmarshal(raw, &state) != nil || state.Agent != d.Agent || state.Path == "" {
		return result, errors.New(d.Agent + " 설치 기록 형식 오류")
	}
	result.Installed = true
	result.Path = state.Path
	root, file, err := readSettings(state.Path)
	if err != nil || file == nil {
		return result, errors.New(state.Path + " 훅 파일이 없거나 읽을 수 없다")
	}
	if d.RootEnabled {
		var on bool
		result.Off = json.Unmarshal(root["enabled"], &on) != nil || !on
	}
	var hooks map[string][]json.RawMessage
	if h, ok := root["hooks"]; ok && json.Unmarshal(h, &hooks) != nil {
		return result, errors.New(state.Path + "의 hooks 형식 오류")
	}
	events := make([]string, 0, len(d.Events))
	for ev := range d.Events {
		events = append(events, ev)
	}
	sort.Strings(events)
	for _, ev := range events {
		count := 0
		for _, entry := range hooks[ev] {
			if jsonEqual(entry, state.Entries[ev]) {
				count++
			}
		}
		if count != 1 {
			result.Missing = append(result.Missing, ev)
		}
	}
	bin, err := strconv.Unquote(strings.TrimSuffix(state.Marker, " hook "))
	if err == nil && filepath.IsAbs(bin) {
		if info, err := os.Stat(bin); err == nil {
			result.Executable = info.Mode().IsRegular() && (runtime.GOOS == "windows" || info.Mode().Perm()&0111 != 0)
		}
	}
	return result, nil
}

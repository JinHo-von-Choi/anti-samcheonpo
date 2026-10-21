package install

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// CursorInstallation describes the recorded connection without executing hooks.
type CursorInstallation struct {
	Path       string
	Installed  bool
	Missing    []string
	Executable bool
}

func InspectCursor() (CursorInstallation, error) {
	var result CursorInstallation
	raw, err := os.ReadFile(hookStatePath("cursor"))
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return result, errors.New("Cursor 설치 기록을 읽을 수 없습니다")
	}
	var state hookState
	if json.Unmarshal(raw, &state) != nil || state.Agent != "cursor" || state.Path == "" {
		return result, errors.New("Cursor 설치 기록 형식 오류")
	}
	result.Installed = true
	result.Path = state.Path
	root, raw, err := readSettings(state.Path)
	if err != nil || raw == nil {
		return result, errors.New("Cursor 훅 파일이 없거나 읽을 수 없습니다")
	}
	var version int
	if json.Unmarshal(root["version"], &version) != nil || version != 1 {
		return result, errors.New("Cursor 훅 version은 1이어야 합니다")
	}
	var hooks map[string][]json.RawMessage
	if json.Unmarshal(root["hooks"], &hooks) != nil || hooks == nil {
		return result, errors.New("Cursor hooks 형식 오류")
	}
	for _, name := range CursorEvents {
		count := 0
		for _, entry := range hooks[name] {
			if jsonEqual(entry, state.Entries[name]) {
				count++
			}
		}
		if count != 1 {
			result.Missing = append(result.Missing, name)
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

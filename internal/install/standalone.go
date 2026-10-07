package install

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
)

type standaloneRecord struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

func standaloneState(agent, path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256([]byte(abs))
	return filepath.Join(config.Home(), fmt.Sprintf("install-%s-%x.json", agent, hash[:8])), nil
}

func installStandalone(agent, path string, body []byte) error {
	state, err := standaloneState(agent, path)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(state); err == nil {
		return errors.New("설치 기록이 이미 있습니다; 먼저 연결을 제거하세요")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return fmt.Errorf("기존 연결 파일을 덮어쓰지 않습니다: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(body); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	record, _ := json.Marshal(standaloneRecord{abs, fmt.Sprintf("%x", sha256.Sum256(body))})
	if err := writeAtomic(state, record, 0600); err != nil {
		return err
	}
	committed = true
	return nil
}

func removeStandalone(agent, path string) error {
	state, err := checkStandalone(agent, path)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Remove(state)
}

// checkStandalone confirms that path still holds what installStandalone
// wrote (or is gone) and returns the install record's path.
func checkStandalone(agent, path string) (string, error) {
	state, err := standaloneState(agent, path)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(state)
	if err != nil {
		return "", errors.New("생성 파일 해시 기록이 없어 연결 파일을 보존했습니다; 파일 내용을 직접 확인하세요")
	}
	var record standaloneRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return "", err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if record.Path != abs || record.SHA256 == "" {
		return "", errors.New("연결 파일 기록 불일치")
	}
	st, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return state, nil
	}
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() {
		return "", errors.New("교체된 연결 경로를 보존했습니다")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if fmt.Sprintf("%x", sha256.Sum256(body)) != record.SHA256 {
		return "", errors.New("설치 이후 수정된 연결 파일을 보존했습니다; 원복하거나 수동으로 제거하세요")
	}
	return state, nil
}

package bench

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
)

// ScheduledTrial randomizes arm order within a task/repetition block. Seeds
// describe ordering only: they cannot make a remote model deterministic.
type ScheduledTrial struct {
	Task  LiveTask
	Arm   string
	Trial int
}

func Schedule(tasks []LiveTask, cfg ABConfig, only string) ([]ScheduledTrial, error) {
	var arms []string
	for arm := range cfg.Arms {
		if only == "" || arm == only {
			arms = append(arms, arm)
		}
	}
	if len(arms) == 0 {
		return nil, fmt.Errorf("알 수 없는 실행 방식: %s", only)
	}
	sort.Strings(arms)
	rng := rand.New(rand.NewSource(cfg.Seed))
	var out []ScheduledTrial
	for trial := 0; trial < cfg.Trials; trial++ {
		for _, task := range tasks {
			for _, index := range rng.Perm(len(arms)) {
				out = append(out, ScheduledTrial{task, arms[index], trial})
			}
		}
	}
	return out, nil
}

// TreeDigest rejects links and special files: following them would let a task
// silently depend on files outside the recorded benchmark. It is a tamper
// detector, not a security boundary against another process of the same user.
func TreeDigest(root string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported benchmark entry: %s", rel)
		}
		size := info.Size()
		if info.IsDir() {
			size = 0
		}
		fmt.Fprintf(h, "%d:%s:%s:%d:", len(rel), rel, info.Mode(), size)
		if info.IsDir() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		_, err = io.Copy(h, f)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ValidateRows prevents silently counting an imported run twice, combining
// incompatible protocols, and presenting negative or internally inconsistent
// costs. Legacy rows remain readable but cannot be mixed with versioned runs.
func ValidateRows(rows []TrialResult) error {
	seen := map[string]bool{}
	sessions := map[string]bool{}
	protocol := ""
	priceVersion, evaluatorVersion, costSource := "", "", ""
	costSourceSet := false
	for i, r := range rows {
		if i == 0 {
			protocol = r.ProtocolVersion
			priceVersion, evaluatorVersion = r.PriceVersion, r.EvaluatorVersion
		}
		if r.PriceVersion != priceVersion || r.EvaluatorVersion != evaluatorVersion {
			return fmt.Errorf("mixed price or evaluator versions")
		}
		if r.CostKnown {
			if costSourceSet && r.CostSource != costSource {
				return fmt.Errorf("mixed cost sources")
			}
			costSource = r.CostSource
			costSourceSet = true
		}
		if r.ProtocolVersion != protocol {
			return fmt.Errorf("mixed benchmark protocol versions")
		}
		if r.ProtocolVersion != "" && r.ProtocolVersion != "2" {
			return fmt.Errorf("unsupported benchmark protocol: %s", r.ProtocolVersion)
		}
		if r.Arm == "" || r.Task == "" || r.Trial < 0 {
			return fmt.Errorf("row %d: missing or invalid trial identity", i+1)
		}
		if r.TotalKRW < 0 || r.WasteKRW < 0 || r.WasteKRW > r.TotalKRW {
			return fmt.Errorf("row %d: invalid cost", i+1)
		}
		if r.MonitorKRW != nil && *r.MonitorKRW < 0 {
			return fmt.Errorf("row %d: negative monitoring cost", i+1)
		}
		key := fmt.Sprintf("%q/%q/%q/%d", r.RunID, r.Arm, r.Task, r.Trial)
		if seen[key] {
			return fmt.Errorf("duplicate trial: %s", key)
		}
		seen[key] = true
		if r.CostKnown && r.SessionID != "" {
			key = r.Agent + "/" + r.SessionID
			if sessions[key] {
				return fmt.Errorf("session cost counted twice: %s", key)
			}
			sessions[key] = true
		}
	}
	return nil
}

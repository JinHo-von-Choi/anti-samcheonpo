// Package surface says where each part of the harness lives.
//
// The external harness vocabulary (internal and external harness, guide and
// sensor) is mapped onto samcheonpo's own places, so a reader who knows one
// can find the other.
//
// The mapping is data, and the data is checked against the filesystem: a
// path that is not there is a mistake in the mapping, not something to
// discover later.
package surface

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Version is the format version of the surface spec.
const Version = "harness-surface/1"

//go:embed harness-surface.json
var raw []byte

// Component is one part of the harness.
type Component struct {
	// Name is the component's own name.
	Name string `json:"name"`
	// Role is what the component is: sensor, guide, editable,
	// external_harness.
	Role string `json:"role"`
	// ExternalTerm is the word the outside world uses for it.
	ExternalTerm string `json:"external_term"`
	// Paths are the places the component lives in.
	Paths []string `json:"paths"`
	// Note says what the component is for.
	Note string `json:"note,omitempty"`
}

// Surface is the whole mapping.
type Surface struct {
	Version    string            `json:"version"`
	Vocabulary map[string]string `json:"vocabulary"`
	Components []Component       `json:"components"`
}

// Load returns the surface mapping.
func Load() (Surface, error) {
	var s Surface
	if err := json.Unmarshal(raw, &s); err != nil {
		return Surface{}, err
	}
	if s.Version != Version {
		return Surface{}, fmt.Errorf("하네스 성분 위치 규격 버전 %q을 지원하지 않는다", s.Version)
	}
	return s, nil
}

// Verify returns the paths of the mapping that are not there.
//
// The root is the repository root. Every path of every component has to
// exist, or the mapping is describing something that moved.
func (s Surface) Verify(root string) []string {
	var missing []string
	for _, c := range s.Components {
		for _, p := range c.Paths {
			full := filepath.Join(root, filepath.FromSlash(p))
			if _, err := os.Stat(full); err != nil {
				missing = append(missing, c.Name+": "+p)
			}
		}
	}
	sort.Strings(missing)
	return missing
}

// Component returns one component by name.
func (s Surface) Component(name string) (Component, bool) {
	for _, c := range s.Components {
		if c.Name == name {
			return c, true
		}
	}
	return Component{}, false
}

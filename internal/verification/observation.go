package verification

// Observation records a tool result without upgrading it to an accepted check
// or reusable evidence. Coverage means related paths, not satisfied features.
type Observation struct {
	Artifacts       []Artifact `json:"artifacts,omitempty"`
	MissingReason   string     `json:"missing_reason,omitempty"`
	Version         string     `json:"version"`
	Kind            string     `json:"kind"`
	State           string     `json:"state"`
	Source          string     `json:"source"`
	CommandHash     string     `json:"command_hash,omitempty"`
	InputHash       string     `json:"input_hash,omitempty"`
	EnvironmentHash string     `json:"environment_hash,omitempty"`
	CheckID         string     `json:"check_id,omitempty"`
	RelatedPaths    []string   `json:"related_paths,omitempty"`
	Independent     bool       `json:"independent"`
}

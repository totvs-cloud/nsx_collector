package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// ManagersFile holds the list of NSX managers.
type ManagersFile struct {
	Managers []Manager `yaml:"managers"`
}

// Manager holds NSX Manager connection details for one site.
type Manager struct {
	Site        string `yaml:"site"`
	URL         string `yaml:"url"`
	UserEnv     string `yaml:"user_env"`
	PasswordEnv string `yaml:"password_env"`
	TLSSkipVerify bool `yaml:"tls_skip_verify"`
	Enabled     bool   `yaml:"enabled"`

	// HAWatch controls how the HA collector chooses which T1s to observe
	// per T0 edge cluster. See HAWatchConfig.
	HAWatch HAWatchConfig `yaml:"ha_watch"`

	// StateDir is where the collector persists per-site state files
	// (inventário de T1s observados, etc.). Default: /home/nsx_collector/state.
	StateDir string `yaml:"state_dir"`

	// Resolved at load time from env vars
	Username string `yaml:"-"`
	Password string `yaml:"-"`
}

// HAWatchConfig selects how many T1s per T0 edge cluster the HA collector
// observes, and how they are picked. The collector persists the effective
// list in <state_dir>/ha-watch-<site>.json and self-heals when an observed
// T1 disappears.
type HAWatchConfig struct {
	// Mode: "auto" (random pick on first run), "pinned" (only names in
	// T1Names that exist), or "hybrid" (pinned first, fill with auto).
	// Default: "auto".
	Mode string `yaml:"mode"`
	// Size is the target number of T1s observed per T0 cluster. Default: 10.
	Size int `yaml:"size"`
	// T1Names is a list of T1 display_names used in pinned/hybrid modes.
	T1Names []string `yaml:"t1_names"`
}

// LoadManagers reads and parses the managers inventory file.
func LoadManagers(path string) ([]Manager, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading managers file %s: %w", path, err)
	}

	var f ManagersFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parsing managers file: %w", err)
	}

	var enabled []Manager
	for _, m := range f.Managers {
		if !m.Enabled {
			continue
		}
		if m.UserEnv != "" {
			m.Username = strings.TrimSpace(os.Getenv(m.UserEnv))
		}
		if m.PasswordEnv != "" {
			m.Password = strings.TrimSpace(os.Getenv(m.PasswordEnv))
		}
		if m.Username == "" {
			return nil, fmt.Errorf("manager %s: env var %s not set", m.Site, m.UserEnv)
		}
		if m.Password == "" {
			return nil, fmt.Errorf("manager %s: env var %s not set", m.Site, m.PasswordEnv)
		}
		// Defaults for HA watch / state_dir
		if m.HAWatch.Mode == "" {
			m.HAWatch.Mode = "auto"
		}
		if m.HAWatch.Size <= 0 {
			m.HAWatch.Size = 10
		}
		if m.StateDir == "" {
			m.StateDir = "/home/nsx_collector/state"
		}
		enabled = append(enabled, m)
	}

	if len(enabled) == 0 {
		return nil, fmt.Errorf("no enabled managers found in %s", path)
	}

	return enabled, nil
}

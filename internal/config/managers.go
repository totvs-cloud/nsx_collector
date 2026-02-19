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

	// Resolved at load time from env vars
	Username string `yaml:"-"`
	Password string `yaml:"-"`
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
		enabled = append(enabled, m)
	}

	if len(enabled) == 0 {
		return nil, fmt.Errorf("no enabled managers found in %s", path)
	}

	return enabled, nil
}

// Package t1watch detects newly created (and deleted) NSX Tier-1 gateways
// across collector cycles by diffing the live Policy API inventory against a
// per-site snapshot persisted on disk. The first cycle after a fresh install
// (no snapshot file) only baselines and emits no events — same pattern as
// internal/collector/ha.go to avoid flooding Slack on restart.
package t1watch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// T1Info is one entry in the persisted snapshot.
type T1Info struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	ParentT0ID      string `json:"parent_t0_id"`
	ParentT0Name    string `json:"parent_t0_name"`
	ParentKind      string `json:"parent_kind"` // "vrf" or "t0"
	EdgeClusterID   string `json:"edge_cluster_id,omitempty"`
	EdgeClusterName string `json:"edge_cluster_name,omitempty"`
	FirstSeen       int64  `json:"first_seen"` // unix seconds
}

// Snapshot is the on-disk state for one site.
type Snapshot struct {
	Site    string             `json:"site"`
	Updated time.Time          `json:"updated"`
	Known   map[string]T1Info  `json:"known"` // keyed by T1 ID
}

// SnapshotPath returns the file path for the snapshot of a given site.
func SnapshotPath(stateDir, site string) string {
	dir := stateDir
	if dir == "" {
		dir = "/home/nsx_collector/state"
	}
	safeSite := strings.ToLower(strings.ReplaceAll(site, "/", "_"))
	return filepath.Join(dir, "t1watch-"+safeSite+".json")
}

// LoadSnapshot reads the snapshot for a site. Returns a fresh (empty) Snapshot
// with HasFile=false when the file does not exist — callers use that flag to
// suppress event emission on first run.
func LoadSnapshot(stateDir, site string) (*Snapshot, bool, error) {
	path := SnapshotPath(stateDir, site)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Snapshot{Site: site, Known: map[string]T1Info{}}, false, nil
		}
		return nil, false, fmt.Errorf("read snapshot %s: %w", path, err)
	}
	var s Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, false, fmt.Errorf("parse snapshot %s: %w", path, err)
	}
	if s.Known == nil {
		s.Known = map[string]T1Info{}
	}
	if s.Site == "" {
		s.Site = site
	}
	return &s, true, nil
}

// SaveSnapshot writes the snapshot to disk atomically (tmp + rename).
func SaveSnapshot(stateDir string, s *Snapshot) error {
	path := SnapshotPath(stateDir, s.Site)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir state_dir: %w", err)
	}
	s.Updated = time.Unix(s.Updated.Unix(), 0).UTC()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

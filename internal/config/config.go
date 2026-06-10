package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds the nsx-collector configuration.
type Config struct {
	InfluxDB        InfluxConfig                `yaml:"influxdb"`
	Logging         LoggingConfig               `yaml:"logging"`
	Telemetry       TelemetryConfig             `yaml:"telemetry"`
	Intervals       IntervalConfig              `yaml:"intervals"`
	Slack           SlackConfig                 `yaml:"slack"`
	T1Watch         T1WatchConfig               `yaml:"t1_watch"`
	Capacity        CapacityConfig              `yaml:"capacity"`
	// InterfaceSpeeds overrides link_speed_mbps for interfaces where the NSX API
	// returns 0 (common for DPDK/fastpath fp-* interfaces on bare-metal Edge nodes).
	// Format: node_name -> interface_id -> speed in Mbps.
	InterfaceSpeeds map[string]map[string]int64 `yaml:"interface_speed_overrides"`
}

// T1WatchConfig controls the "new T1 detected" Slack bot.
// The detector runs every collector cycle against the persisted snapshot and
// emits one Slack message per newly observed T1.
type T1WatchConfig struct {
	Enabled bool   `yaml:"enabled"`
	// SlackChannel overrides slack.channel for T1 events (so you can route
	// capacity events to a different channel than bandwidth alerts).
	SlackChannel       string            `yaml:"slack_channel"`
	StateDir           string            `yaml:"state_dir"`
	VRFT1LimitDefault  int64             `yaml:"vrf_t1_limit_default"`
	T0T1LimitDefault   int64             `yaml:"t0_t1_limit_default"`
	VRFT1Limits        map[string]int64  `yaml:"vrf_t1_limits"`
	T0T1Limits         map[string]int64  `yaml:"t0_t1_limits"`
}

// CapacityConfig controls extended Capacity NSX collection (segments, NAT
// per T1, gateway-policies per T1, groups inventory). When extras are off we
// still collect /capacity/usage, LB credits, T1-per-VRF and T1-per-T0.
type CapacityConfig struct {
	// CollectSegments runs the segments inventory at slow cadence (for B3/D1).
	CollectSegments bool `yaml:"collect_segments"`
	// CollectNATPerT1 fetches /tier-1s/{id}/nat/USER/nat-rules?page_size=1 per T1.
	// On TESP3 (2272 T1s) this means ~2272 extra requests per slow cycle (5min);
	// paced by NATPerT1PaceMS to avoid rate-limits.
	CollectNATPerT1   bool `yaml:"collect_nat_per_t1"`
	NATPerT1PaceMS   int `yaml:"nat_per_t1_pace_ms"`
	NATPerT1Parallel int `yaml:"nat_per_t1_parallel"`
	// CollectGWPolicies runs gateway-policies?include_rule_count=true (B4).
	CollectGWPolicies bool `yaml:"collect_gw_policies"`
	// CollectGroups runs the groups inventory at slow cadence (D2).
	CollectGroups bool `yaml:"collect_groups"`
}

// SlackConfig holds Slack alerting settings.
type SlackConfig struct {
	Enabled     bool   `yaml:"enabled"`
	BotTokenEnv string `yaml:"bot_token_env"`
	Channel     string `yaml:"channel"`
	GrafanaURL  string `yaml:"grafana_url"`
	DashboardURL string `yaml:"dashboard_url"`
	GrafanaKeyEnv string `yaml:"grafana_key_env"`
	RXUtilPanelID string `yaml:"rx_util_panel_id"`
	TXUtilPanelID string `yaml:"tx_util_panel_id"`
}

// InfluxConfig holds InfluxDB connection settings.
type InfluxConfig struct {
	URL            string `yaml:"url"`
	Org            string `yaml:"org"`
	Bucket         string `yaml:"bucket"`
	CapacityBucket string `yaml:"capacity_bucket"` // separate bucket for capacity metrics (longer retention)
	TokenFile      string `yaml:"token_file"`
	Token          string `yaml:"-"`
}

// LoggingConfig holds logging settings.
type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// TelemetryConfig holds self-monitoring settings.
type TelemetryConfig struct {
	Enabled bool   `yaml:"enabled"`
	Address string `yaml:"address"`
}

// IntervalConfig holds collection interval settings.
type IntervalConfig struct {
	Default time.Duration `yaml:"default"` // for cluster, nodes, routers
	Traffic time.Duration `yaml:"traffic"` // for interface throughput (future)
	Slow    time.Duration `yaml:"slow"`    // for alarms, capacity, LB (changes slowly)
	HA      time.Duration `yaml:"ha"`      // for T0/T1 HA state of observed SRs (default 1m)
}

// LoadConfig reads and parses the collector config file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file %s: %w", path, err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	// Resolve InfluxDB token
	if cfg.InfluxDB.TokenFile != "" {
		tokenData, err := os.ReadFile(cfg.InfluxDB.TokenFile)
		if err == nil {
			cfg.InfluxDB.Token = strings.TrimSpace(string(tokenData))
		}
	}
	if cfg.InfluxDB.Token == "" {
		token := os.Getenv("INFLUX_TOKEN")
		if token != "" {
			cfg.InfluxDB.Token = token
		} else {
			return nil, fmt.Errorf("no influxdb token found: set INFLUX_TOKEN env or configure token_file")
		}
	}

	cfg.setDefaults()
	return cfg, nil
}

func (c *Config) setDefaults() {
	if c.Logging.Level == "" {
		c.Logging.Level = "info"
	}
	if c.Logging.Format == "" {
		c.Logging.Format = "json"
	}
	if c.InfluxDB.URL == "" {
		c.InfluxDB.URL = "http://localhost:8086"
	}
	if c.InfluxDB.Org == "" {
		c.InfluxDB.Org = "TOTVS"
	}
	if c.InfluxDB.Bucket == "" {
		c.InfluxDB.Bucket = "nsx"
	}
	if c.Telemetry.Address == "" {
		c.Telemetry.Address = ":9101"
	}
	if c.Intervals.Default == 0 {
		c.Intervals.Default = 40 * time.Second
	}
	if c.Intervals.Traffic == 0 {
		c.Intervals.Traffic = 15 * time.Second
	}
	if c.Intervals.Slow == 0 {
		c.Intervals.Slow = 5 * time.Minute
	}
	if c.Intervals.HA == 0 {
		c.Intervals.HA = 1 * time.Minute
	}
	if c.T1Watch.StateDir == "" {
		c.T1Watch.StateDir = "/home/nsx_collector/state"
	}
	if c.T1Watch.VRFT1LimitDefault == 0 {
		c.T1Watch.VRFT1LimitDefault = 200
	}
	if c.T1Watch.T0T1LimitDefault == 0 {
		c.T1Watch.T0T1LimitDefault = 1000
	}
	if c.Capacity.NATPerT1PaceMS == 0 {
		c.Capacity.NATPerT1PaceMS = 30 // 30ms × 2272 T1s = ~68s per slow cycle on TESP3
	}
	if c.Capacity.NATPerT1Parallel == 0 {
		c.Capacity.NATPerT1Parallel = 4
	}
}

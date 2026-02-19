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
	InfluxDB  InfluxConfig  `yaml:"influxdb"`
	Logging   LoggingConfig `yaml:"logging"`
	Telemetry TelemetryConfig `yaml:"telemetry"`
	Intervals IntervalConfig `yaml:"intervals"`
}

// InfluxConfig holds InfluxDB connection settings.
type InfluxConfig struct {
	URL       string `yaml:"url"`
	Org       string `yaml:"org"`
	Bucket    string `yaml:"bucket"`
	TokenFile string `yaml:"token_file"`
	Token     string `yaml:"-"`
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
	Default time.Duration `yaml:"default"` // for cluster, nodes, routers, BGP
	Traffic time.Duration `yaml:"traffic"` // for interface throughput (future)
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
}

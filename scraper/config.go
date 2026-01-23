package scraper

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config represents the scraper configuration
type Config struct {
	Global       GlobalConfig   `yaml:"global"`
	ScrapeConfigs []ScrapeConfig `yaml:"scrape_configs"`
}

// GlobalConfig contains global scraper settings
type GlobalConfig struct {
	ScrapeInterval time.Duration `yaml:"scrape_interval"`
	ScrapeTimeout  time.Duration `yaml:"scrape_timeout"`
}

// ScrapeConfig represents a single scrape target configuration
type ScrapeConfig struct {
	JobName        string            `yaml:"job_name"`
	ScrapeInterval time.Duration     `yaml:"scrape_interval,omitempty"`
	ScrapeTimeout  time.Duration     `yaml:"scrape_timeout,omitempty"`
	MetricsPath    string            `yaml:"metrics_path"`
	StaticConfigs  []StaticConfig    `yaml:"static_configs"`
	MetricPrefix   string            `yaml:"metric_prefix,omitempty"`
	Labels         map[string]string `yaml:"labels,omitempty"`
}

// StaticConfig represents static target configuration
type StaticConfig struct {
	Targets []string          `yaml:"targets"`
	Labels  map[string]string `yaml:"labels,omitempty"`
}

// LoadConfig loads configuration from YAML file
func LoadConfig(filename string) (*Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// Set defaults
	if config.Global.ScrapeInterval == 0 {
		config.Global.ScrapeInterval = 15 * time.Second
	}
	if config.Global.ScrapeTimeout == 0 {
		config.Global.ScrapeTimeout = 10 * time.Second
	}

	// Set per-job defaults from global
	for i := range config.ScrapeConfigs {
		if config.ScrapeConfigs[i].ScrapeInterval == 0 {
			config.ScrapeConfigs[i].ScrapeInterval = config.Global.ScrapeInterval
		}
		if config.ScrapeConfigs[i].ScrapeTimeout == 0 {
			config.ScrapeConfigs[i].ScrapeTimeout = config.Global.ScrapeTimeout
		}
		if config.ScrapeConfigs[i].MetricsPath == "" {
			config.ScrapeConfigs[i].MetricsPath = "/metrics"
		}
	}

	return &config, nil
}

// Validate validates the configuration
func (c *Config) Validate() error {
	if len(c.ScrapeConfigs) == 0 {
		return fmt.Errorf("no scrape configs defined")
	}

	for i, sc := range c.ScrapeConfigs {
		if sc.JobName == "" {
			return fmt.Errorf("scrape_config[%d]: job_name is required", i)
		}
		if len(sc.StaticConfigs) == 0 {
			return fmt.Errorf("scrape_config[%d]: no static_configs defined", i)
		}
		for j, static := range sc.StaticConfigs {
			if len(static.Targets) == 0 {
				return fmt.Errorf("scrape_config[%d].static_configs[%d]: no targets defined", i, j)
			}
		}
	}

	return nil
}

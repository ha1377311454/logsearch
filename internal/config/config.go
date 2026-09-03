package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Search   SearchConfig   `yaml:"search"`
	Security SecurityConfig `yaml:"security"`
}

type ServerConfig struct {
	Listen   string `yaml:"listen"`
	NodeName string `yaml:"node_name"`
}

type SearchConfig struct {
	Roots                 []string           `yaml:"roots"`
	AllowedExtensions     []string           `yaml:"allowed_extensions"`
	PodNameContains       []string           `yaml:"pod_name_contains"`
	ProcessLogs           []ProcessLogConfig `yaml:"process_logs"`
	MaxConcurrentSearches int                `yaml:"max_concurrent_searches"`
	MaxFilesPerRequest    int                `yaml:"max_files_per_request"`
	DefaultMaxResults     int                `yaml:"default_max_results"`
	HardMaxResults        int                `yaml:"hard_max_results"`
	DefaultTimeout        string             `yaml:"default_timeout"`
	HardTimeout           string             `yaml:"hard_timeout"`
	MaxResponseBytes      int64              `yaml:"max_response_bytes"`
	MaxLineBytes          int                `yaml:"max_line_bytes"`
}

type ProcessLogConfig struct {
	Name         string   `yaml:"name"`
	CommRegex    string   `yaml:"comm_regex"`
	CmdlineRegex string   `yaml:"cmdline_regex"`
	IncludeRegex string   `yaml:"include_regex"`
	ExcludeRegex string   `yaml:"exclude_regex"`
	LogDirs      []string `yaml:"log_dirs"`
	FilePatterns []string `yaml:"file_patterns"`
	MaxFiles     int      `yaml:"max_files"`
	MaxFileAge   string   `yaml:"max_file_age"`
}

type SecurityConfig struct {
	Token          string   `yaml:"token"`
	AllowedOrigins []string `yaml:"allowed_origins"`
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	cfg.defaults()
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	if token := os.Getenv("LOGSEARCH_TOKEN"); token != "" {
		cfg.Security.Token = token
	}
	return cfg, nil
}

func (c *Config) defaults() {
	if c.Server.Listen == "" {
		c.Server.Listen = ":9000"
	}
	if c.Server.NodeName == "" {
		c.Server.NodeName = os.Getenv("NODE_NAME")
	}
	if c.Server.NodeName == "" {
		c.Server.NodeName, _ = os.Hostname()
	}
	if len(c.Search.AllowedExtensions) == 0 {
		c.Search.AllowedExtensions = []string{".log"}
	}
	if c.Search.MaxConcurrentSearches <= 0 {
		c.Search.MaxConcurrentSearches = 4
	}
	if c.Search.MaxFilesPerRequest <= 0 {
		c.Search.MaxFilesPerRequest = 200
	}
	if c.Search.DefaultMaxResults <= 0 {
		c.Search.DefaultMaxResults = 500
	}
	if c.Search.HardMaxResults <= 0 {
		c.Search.HardMaxResults = 5000
	}
	if c.Search.DefaultTimeout == "" {
		c.Search.DefaultTimeout = "15s"
	}
	if c.Search.HardTimeout == "" {
		c.Search.HardTimeout = "60s"
	}
	if c.Search.MaxResponseBytes <= 0 {
		c.Search.MaxResponseBytes = 20 << 20
	}
	if c.Search.MaxLineBytes <= 0 {
		c.Search.MaxLineBytes = 1 << 20
	}
}

func (c Config) validate() error {
	if len(c.Search.Roots) == 0 {
		return fmt.Errorf("search.roots is required")
	}
	if _, err := time.ParseDuration(c.Search.DefaultTimeout); err != nil {
		return fmt.Errorf("invalid search.default_timeout: %w", err)
	}
	if _, err := time.ParseDuration(c.Search.HardTimeout); err != nil {
		return fmt.Errorf("invalid search.hard_timeout: %w", err)
	}
	for _, rule := range c.Search.ProcessLogs {
		if rule.Name == "" || (rule.CommRegex == "" && rule.CmdlineRegex == "") {
			return fmt.Errorf("process log rule name and process matcher are required")
		}
		for _, dir := range rule.LogDirs {
			if !filepath.IsAbs(dir) || filepath.Clean(dir) == string(filepath.Separator) {
				return fmt.Errorf("process log rule %q has invalid log_dir %q", rule.Name, dir)
			}
		}
		if rule.MaxFileAge != "" {
			if _, err := time.ParseDuration(rule.MaxFileAge); err != nil {
				return fmt.Errorf("process log rule %q has invalid max_file_age: %w", rule.Name, err)
			}
		}
	}
	return nil
}

func (c Config) DefaultTimeout() time.Duration {
	d, _ := time.ParseDuration(c.Search.DefaultTimeout)
	return d
}

func (c Config) HardTimeout() time.Duration {
	d, _ := time.ParseDuration(c.Search.HardTimeout)
	return d
}

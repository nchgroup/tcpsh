package config

import (
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// Config holds all runtime configuration for tcpsh.
type Config struct {
	Prompt      string `mapstructure:"prompt"`
	HistoryFile string `mapstructure:"history_file"`
	HistorySize int    `mapstructure:"history_size"`
	DialTimeout int    `mapstructure:"dial_timeout"` // seconds
	LogLevel    string `mapstructure:"log_level"`
	Quiet       bool   `mapstructure:"quiet"`
}

var defaultConfig = Config{
	Prompt:      "tcpsh> ",
	HistoryFile: "~/.tcpsh_history",
	HistorySize: 1000,
	DialTimeout: 10,
	LogLevel:    "info",
	Quiet:       false,
}

// Load reads ~/.tcpsh.yaml and merges with defaults. Missing file is not an error.
func Load() (*Config, error) {
	viper.SetDefault("prompt", defaultConfig.Prompt)
	viper.SetDefault("history_file", defaultConfig.HistoryFile)
	viper.SetDefault("history_size", defaultConfig.HistorySize)
	viper.SetDefault("dial_timeout", defaultConfig.DialTimeout)
	viper.SetDefault("log_level", defaultConfig.LogLevel)
	viper.SetDefault("quiet", defaultConfig.Quiet)

	home, err := os.UserHomeDir()
	if err == nil {
		viper.AddConfigPath(home)
	}
	viper.AddConfigPath(".")
	viper.SetConfigName(".tcpsh")
	viper.SetConfigType("yaml")
	viper.SetEnvPrefix("TCPSH")
	viper.AutomaticEnv()

	// Missing config file is acceptable.
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, err
		}
	}

	cfg := &Config{}
	if err := viper.Unmarshal(cfg); err != nil {
		return nil, err
	}

	// Expand ~ in history file path.
	cfg.HistoryFile = expandTilde(cfg.HistoryFile)
	return cfg, nil
}

func expandTilde(path string) string {
	if len(path) == 0 || path[0] != '~' {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type ServerConfig struct {
	Host          string `yaml:"host"`
	HTTPPort      int    `yaml:"http_port"`
	TCPPort       int    `yaml:"tcp_port"`
	UDPPort       int    `yaml:"udp_port"`
	GRPCPort      int    `yaml:"grpc_port"`
	WebSocketPort int    `yaml:"websocket_port"`
}

type DatabaseConfig struct {
	Path string `yaml:"path"`
}

type UserConfig struct {
	Username string `yaml:"username"`
	Token    string `yaml:"token"`
}

type SyncConfig struct {
	AutoSync           bool   `yaml:"auto_sync"`
	ConflictResolution string `yaml:"conflict_resolution"`
}

type NotificationsConfig struct {
	Enabled bool `yaml:"enabled"`
	Sound   bool `yaml:"sound"`
}

type LoggingConfig struct {
	Level string `yaml:"level"`
	Path  string `yaml:"path"`
}

type Config struct {
	Server        ServerConfig        `yaml:"server"`
	Database      DatabaseConfig      `yaml:"database"`
	User          UserConfig          `yaml:"user"`
	Sync          SyncConfig          `yaml:"sync"`
	Notifications NotificationsConfig `yaml:"notifications"`
	Logging       LoggingConfig       `yaml:"logging"`
}

func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Host:          "localhost",
			HTTPPort:      8080,
			TCPPort:       9090,
			UDPPort:       9091,
			GRPCPort:      9092,
			WebSocketPort: 9093,
		},
		Database: DatabaseConfig{
			Path: "~/.mangahub/data.db",
		},
		User: UserConfig{
			Username: "",
			Token:    "",
		},
		Sync: SyncConfig{
			AutoSync:           true,
			ConflictResolution: "last_write_wins",
		},
		Notifications: NotificationsConfig{
			Enabled: true,
			Sound:   false,
		},
		Logging: LoggingConfig{
			Level: "info",
			Path:  "~/.mangahub/logs/",
		},
	}
}

func DefaultConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".mangahub"), nil
}

func DefaultConfigPath() (string, error) {
	dir, err := DefaultConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}

func LoadConfig(customPath string) (*Config, error) {
	cfgPath := customPath
	if cfgPath == "" {
		var err error
		cfgPath, err = DefaultConfigPath()
		if err != nil {
			return nil, err
		}
	} else if strings.HasPrefix(cfgPath, "~/") {
		home, _ := os.UserHomeDir()
		cfgPath = filepath.Join(home, cfgPath[2:])
	}

	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		// return default if config file doesn't exist
		return DefaultConfig(), nil
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("reading config file %s: %w", cfgPath, err)
	}

	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config YAML: %w", err)
	}

	return cfg, nil
}

func SaveConfig(cfg *Config, customPath string) error {
	cfgPath := customPath
	if cfgPath == "" {
		var err error
		cfgPath, err = DefaultConfigPath()
		if err != nil {
			return err
		}
	} else if strings.HasPrefix(cfgPath, "~/") {
		home, _ := os.UserHomeDir()
		cfgPath = filepath.Join(home, cfgPath[2:])
	}

	dir := filepath.Dir(cfgPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshaling config YAML: %w", err)
	}

	return os.WriteFile(cfgPath, data, 0644)
}

func (c *Config) Set(key string, val string) error {
	switch strings.ToLower(key) {
	case "server.host":
		c.Server.Host = val
	case "server.http_port":
		p, err := strconv.Atoi(val)
		if err != nil {
			return err
		}
		c.Server.HTTPPort = p
	case "server.tcp_port":
		p, err := strconv.Atoi(val)
		if err != nil {
			return err
		}
		c.Server.TCPPort = p
	case "server.udp_port":
		p, err := strconv.Atoi(val)
		if err != nil {
			return err
		}
		c.Server.UDPPort = p
	case "server.grpc_port":
		p, err := strconv.Atoi(val)
		if err != nil {
			return err
		}
		c.Server.GRPCPort = p
	case "server.websocket_port":
		p, err := strconv.Atoi(val)
		if err != nil {
			return err
		}
		c.Server.WebSocketPort = p
	case "database.path":
		c.Database.Path = val
	case "user.username":
		c.User.Username = val
	case "user.token":
		c.User.Token = val
	case "sync.auto_sync":
		c.Sync.AutoSync = strings.ToLower(val) == "true" || val == "1"
	case "sync.conflict_resolution":
		c.Sync.ConflictResolution = val
	case "notifications.enabled":
		c.Notifications.Enabled = strings.ToLower(val) == "true" || val == "1"
	case "notifications.sound":
		c.Notifications.Sound = strings.ToLower(val) == "true" || val == "1"
	case "logging.level":
		c.Logging.Level = val
	case "logging.path":
		c.Logging.Path = val
	default:
		return fmt.Errorf("unknown configuration key: %s", key)
	}
	return nil
}

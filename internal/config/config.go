package config

import (
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is a main config
type Config struct {
	Servers   []Server  `yaml:"servers"`
	AppConfig AppConfig `yaml:"app"`
	DBConfig  DBConfig  `yaml:"db"`
}

// Servers is a config for backends
type Server struct {
	URL    string `yaml:"url"`
	Weight int    `yaml:"weight"`
}

// AppConfig is a config for application
type AppConfig struct {
	Port string `yaml:"port"`
}

// DBConfig is a config for PostgreSQL
type DBConfig struct {
	URL string `yaml:"url"`
}

// InitConfig parses config.yaml located in the project root
// and initializes a new Config
func InitConfig() (*Config, error) {
	file, err := os.Open("config.yaml")
	if err != nil {
		return nil, fmt.Errorf("failed to open config file: %w", err)
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	config := Config{}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return &config, nil
}

package config

import (
	"fmt"
	"io"
	"os"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

type Config struct {
	// data from yaml file
	Servers []struct {
		URL    string `yaml:"url"`
		Weight int    `yaml:"weight"`
	} `yaml:"servers"`

	// data from json file
	Port  string `json:"port,omitempty" env:"APP_PORT"`
	DBURL string `json:"db_url,omitempty" env:"DB_URL"`
}

// InitConfig parses config.yaml and env files located in the project root
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

	if err := godotenv.Load(); err != nil {
		panic("No .env file found")
	}

	config := &Config{}

	if err := yaml.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("failed to parse yaml config file: %w", err)
	}

	config.Port = os.Getenv("APP_PORT")
	config.DBURL = os.Getenv("DB_URL")

	return config, nil
}

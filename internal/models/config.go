package models

import (
	"os"

	"gopkg.in/yaml.v2"
)

type Config struct {
	ServerAddr    string `yaml:"server_addr"`
	DatabaseURL   string `yaml:"database_url"`
	KafkaBroker   string `yaml:"kafka_broker"`
	KafkaTopic    string `yaml:"kafka_topic"`
	StoragePath   string `yaml:"storage_path"`
	WatermarkText string `yaml:"watermark_text"`
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	err = yaml.Unmarshal(data, &cfg)
	return &cfg, err
}

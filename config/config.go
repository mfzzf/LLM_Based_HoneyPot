package config

import (
	"encoding/json"
	"os"
)

// Config 表示应用程序配置
type Config struct {
	ListenAddr string    `json:"listen_addr"`
	TargetAddr string    `json:"target_addr"`
	LogEnabled bool      `json:"log_enabled"`
	ELK        ELKConfig `json:"elk"`
}

// ELKConfig 表示ELK日志配置
type ELKConfig struct {
	Enabled  bool   `json:"enabled"`
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
	APIKey   string `json:"api_key"`
	Index    string `json:"index"`
}

// DefaultConfig 返回默认配置
func DefaultConfig() Config {
	return Config{
		ListenAddr: ":8080",
		TargetAddr: "http://10.255.248.65:11434",
		LogEnabled: true,
		ELK: ELKConfig{
			Enabled:  true,
			URL:      "http://10.255.248.65:50000",
			Username: "username",
			Password: "password",
			APIKey:   "",
			Index:    "ollama-proxy",
		},
	}
}

// LoadConfig 从文件加载配置
func LoadConfig(filename string) (Config, error) {
	config := DefaultConfig()

	file, err := os.Open(filename)
	if err != nil {
		return config, err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	err = decoder.Decode(&config)
	return config, err
}

// SaveConfig 保存配置到文件
func SaveConfig(filename string, config Config) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return encoder.Encode(config)
}

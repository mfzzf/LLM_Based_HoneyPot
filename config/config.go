package config

import (
	"encoding/json"
	"os"
)

// Config 表示应用程序配置
type Config struct {
	ListenAddr string          `json:"listen_addr"`
	TargetAddr string          `json:"target_addr"`
	LogEnabled bool            `json:"log_enabled"`
	ELK        ELKConfig       `json:"elk"`
	Admission  AdmissionConfig `json:"admission"`
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

// AdmissionConfig 表示准入控制配置
type AdmissionConfig struct {
	Enabled    bool   `json:"enabled"`
	ModelName  string `json:"model_name"`
	OllamaURL  string `json:"ollama_url"`
	Timeout    int    `json:"timeout_seconds"`
	MaxRetries int    `json:"max_retries"`
}

// DefaultConfig 返回默认配置
func DefaultConfig() Config {
	return Config{
		ListenAddr: ":8080",
		TargetAddr: "http://10.255.248.65:11434",
		LogEnabled: true,
		ELK: ELKConfig{
			Enabled:  true,
			URL:      "http://10.255.248.65:9200",
			Username: "elastic",
			Password: "H3JIfzF2Ic*dbRj4c5Kd",
			//APIKey:   "",
			Index: "ollama-proxy",
		},
		Admission: AdmissionConfig{
			Enabled:    true,
			ModelName:  "phi3:3.8b", // 使用较小的模型进行验证
			OllamaURL:  "http://10.255.248.65:11434",
			Timeout:    5, // 5秒超时
			MaxRetries: 2,
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

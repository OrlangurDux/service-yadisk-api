package config

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type FolderMapping struct {
	Local      string `yaml:"local"`
	Remote     string `yaml:"remote"`
	SyncMode   string `yaml:"sync_mode"`   // "upload", "download", "bidirectional"
	DeleteMode string `yaml:"delete_mode"` // "none", "local", "remote", "both"
}

type Config struct {
	Token         string          `yaml:"token"`
	SyncFrequency time.Duration   `yaml:"sync_frequency"`
	Folders       []FolderMapping `yaml:"folders"`
	CacheEnabled  bool            `yaml:"cache_enabled"`
	CachePath     string          `yaml:"cache_path"`
}

func LoadConfig(path string) (*Config, error) {
	// Чтение файла
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Замена переменных окружения
	content := os.ExpandEnv(string(data))

	var config Config
	err = yaml.Unmarshal([]byte(content), &config)
	if err != nil {
		return nil, err
	}

	// Установка значений по умолчанию
	for i := range config.Folders {
		if config.Folders[i].SyncMode == "" {
			config.Folders[i].SyncMode = "bidirectional"
		}
		if config.Folders[i].DeleteMode == "" {
			config.Folders[i].DeleteMode = "none"
		}

		// Нормализация путей
		config.Folders[i].Local = strings.TrimRight(config.Folders[i].Local, "/")
		config.Folders[i].Remote = strings.TrimRight(config.Folders[i].Remote, "/")

		// Добавление префикса disk:/ если отсутствует
		if !strings.HasPrefix(config.Folders[i].Remote, "disk:/") {
			config.Folders[i].Remote = "disk:/" + strings.TrimPrefix(config.Folders[i].Remote, "/")
		}
	}

	// Если частота не указана, используем 5 минут по умолчанию
	if config.SyncFrequency == 0 {
		config.SyncFrequency = 5 * time.Minute
	}

	if config.CachePath == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			config.CachePath = filepath.Join(home, ".cache", "yandex-sync", "cache.json")
		} else {
			config.CachePath = "/tmp/yandex-sync-cache.json"
		}
	}

	return &config, nil
}

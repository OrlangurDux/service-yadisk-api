package config

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Допустимые значения режимов
var (
	validSyncModes   = map[string]bool{"upload": true, "download": true, "bidirectional": true}
	validDeleteModes = map[string]bool{"none": true, "local": true, "remote": true, "both": true}
	validLogModes    = map[string]bool{"off": true, "simple": true, "extended": true}
)

type FolderMapping struct {
	Local      string `yaml:"local"`
	Remote     string `yaml:"remote"`
	SyncMode   string `yaml:"sync_mode"`   // "upload", "download", "bidirectional"
	DeleteMode string `yaml:"delete_mode"` // "none", "local", "remote", "both"
	// ExcludePattern — маски исключения через '|': "node_modules|*.tmp|cache_*".
	// Маска без '/' сравнивается с каждым компонентом пути (исключает файл или
	// папку на любой глубине), маска с '/' — с полным относительным путём.
	ExcludePattern string `yaml:"exclude_pattern"`
}

// SplitExcludePattern разбивает exclude_pattern на отдельные маски.
func SplitExcludePattern(patterns string) []string {
	var out []string
	for _, p := range strings.Split(patterns, "|") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

type Config struct {
	Token         string          `yaml:"token"`
	SyncFrequency time.Duration   `yaml:"sync_frequency"`
	Folders       []FolderMapping `yaml:"folders"`
	CacheEnabled  bool            `yaml:"cache_enabled"`
	CachePath     string          `yaml:"cache_path"`
	// LogMode: "off" — без файловых логов, "simple" — только итоги сессий,
	// "extended" — итоги + отдельный файл с деталями по каждой операции.
	LogMode string `yaml:"log_mode"`
	LogDir  string `yaml:"log_dir"`
	// HTTPEnabled — указатель, чтобы отличать "не задано" (по умолчанию включено) от false.
	HTTPEnabled *bool `yaml:"http_enabled"`
	HTTPPort    int   `yaml:"http_port"`
	LogMaxFiles int   `yaml:"log_max_files"`
}

// IsHTTPEnabled возвращает true, если HTTP-сервер не выключен явно.
func (c *Config) IsHTTPEnabled() bool {
	return c.HTTPEnabled == nil || *c.HTTPEnabled
}

func LoadConfig(path string) (*Config, error) {
	var config Config

	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		// Файл отсутствует — допустимо, если конфигурация задана через env
	} else {
		content := os.ExpandEnv(string(data))
		if err := yaml.Unmarshal([]byte(content), &config); err != nil {
			return nil, fmt.Errorf("failed to parse %s: %w", path, err)
		}
	}

	applyEnvOverrides(&config)
	applyDefaults(&config)

	if err := config.Validate(); err != nil {
		return nil, err
	}

	return &config, nil
}

// applyEnvOverrides применяет переменные окружения (приоритет над файлом).
func applyEnvOverrides(c *Config) {
	if v := os.Getenv("YANDEX_TOKEN"); v != "" {
		c.Token = v
	}
	if v := os.Getenv("SYNC_FREQUENCY"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.SyncFrequency = d
		}
	}
	if v := os.Getenv("CACHE_PATH"); v != "" {
		c.CachePath = v
	}
	if v := os.Getenv("CACHE_ENABLED"); v != "" {
		c.CacheEnabled = v == "true" || v == "1" || v == "yes"
	}
	if v := os.Getenv("LOG_MODE"); v != "" {
		c.LogMode = v
	}
	if v := os.Getenv("LOG_DIR"); v != "" {
		c.LogDir = v
	}
	if v := os.Getenv("HTTP_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			c.HTTPPort = p
		}
	}
	if v := os.Getenv("HTTP_ENABLED"); v != "" {
		b := v == "true" || v == "1" || v == "yes"
		c.HTTPEnabled = &b
	}

	// Папки из env: LOCAL_FOLDER_N + REMOTE_FOLDER_N (+ SYNC_MODE_N, DELETE_MODE_N).
	// Если задана хотя бы одна пара — заменяют список из файла.
	var envFolders []FolderMapping
	for i := 1; ; i++ {
		local := os.Getenv(fmt.Sprintf("LOCAL_FOLDER_%d", i))
		if local == "" {
			break
		}
		remote := os.Getenv(fmt.Sprintf("REMOTE_FOLDER_%d", i))
		if remote == "" {
			break
		}
		envFolders = append(envFolders, FolderMapping{
			Local:          local,
			Remote:         remote,
			SyncMode:       os.Getenv(fmt.Sprintf("SYNC_MODE_%d", i)),
			DeleteMode:     os.Getenv(fmt.Sprintf("DELETE_MODE_%d", i)),
			ExcludePattern: os.Getenv(fmt.Sprintf("EXCLUDE_PATTERN_%d", i)),
		})
	}
	if len(envFolders) > 0 {
		c.Folders = envFolders
	}
}

func applyDefaults(c *Config) {
	for i := range c.Folders {
		if c.Folders[i].SyncMode == "" {
			c.Folders[i].SyncMode = "bidirectional"
		}
		if c.Folders[i].DeleteMode == "" {
			c.Folders[i].DeleteMode = "none"
		}
		c.Folders[i].Local = strings.TrimRight(c.Folders[i].Local, "/")
		c.Folders[i].Remote = strings.TrimRight(c.Folders[i].Remote, "/")
		if c.Folders[i].Remote != "" && !strings.HasPrefix(c.Folders[i].Remote, "disk:/") {
			c.Folders[i].Remote = "disk:/" + strings.TrimPrefix(c.Folders[i].Remote, "/")
		}
	}

	if c.SyncFrequency == 0 {
		c.SyncFrequency = 5 * time.Minute
	}

	if c.CachePath == "" {
		if home, err := os.UserHomeDir(); err == nil {
			c.CachePath = filepath.Join(home, ".cache", "yandex-sync", "cache.json")
		} else {
			c.CachePath = "/tmp/yandex-sync-cache.json"
		}
	}

	if c.LogMode == "" {
		c.LogMode = "simple"
	}
	if c.LogDir == "" {
		c.LogDir = filepath.Join(filepath.Dir(c.CachePath), "logs")
	}
	if c.LogMaxFiles == 0 {
		c.LogMaxFiles = 10
	}
	if c.HTTPPort == 0 {
		c.HTTPPort = 9277
	}
}

// Validate проверяет корректность конфигурации.
func (c *Config) Validate() error {
	var errs []string

	if c.Token == "" || c.Token == "your_oauth_token_here" {
		errs = append(errs, "token is not configured (set YANDEX_TOKEN env or token in config)")
	}
	if c.SyncFrequency < time.Second {
		errs = append(errs, fmt.Sprintf("sync_frequency %v is too small (min 1s)", c.SyncFrequency))
	}
	if !validLogModes[c.LogMode] {
		errs = append(errs, fmt.Sprintf("invalid log_mode %q (allowed: off, simple, extended)", c.LogMode))
	}
	if c.HTTPPort < 0 || c.HTTPPort > 65535 {
		errs = append(errs, fmt.Sprintf("invalid http_port %d", c.HTTPPort))
	}
	if len(c.Folders) == 0 {
		errs = append(errs, "no folders configured")
	}

	for i, f := range c.Folders {
		if f.Local == "" {
			errs = append(errs, fmt.Sprintf("folders[%d]: local path is empty", i))
		}
		if f.Remote == "" {
			errs = append(errs, fmt.Sprintf("folders[%d]: remote path is empty", i))
		}
		if !validSyncModes[f.SyncMode] {
			errs = append(errs, fmt.Sprintf("folders[%d]: invalid sync_mode %q (allowed: upload, download, bidirectional)", i, f.SyncMode))
		}
		if !validDeleteModes[f.DeleteMode] {
			errs = append(errs, fmt.Sprintf("folders[%d]: invalid delete_mode %q (allowed: none, local, remote, both)", i, f.DeleteMode))
		}
		for _, pat := range SplitExcludePattern(f.ExcludePattern) {
			if _, err := path.Match(pat, "probe"); err != nil {
				errs = append(errs, fmt.Sprintf("folders[%d]: invalid exclude_pattern %q: %v", i, pat, err))
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("invalid config:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

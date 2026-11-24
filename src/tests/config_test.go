package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"service-yadisk-api/internal/config"
)

func writeConfigFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestConfig_LoadAndDefaults(t *testing.T) {
	path := writeConfigFile(t, `
token: "tok"
sync_frequency: "10m"
log_mode: "extended"
folders:
  - local: "/a/b/"
    remote: "/backup/b/"
    sync_mode: "upload"
    delete_mode: "remote"
`)

	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.SyncFrequency != 10*time.Minute {
		t.Errorf("freq = %v", cfg.SyncFrequency)
	}
	f := cfg.Folders[0]
	if f.Remote != "disk:/backup/b" {
		t.Errorf("remote should get disk:/ prefix and no trailing slash, got %q", f.Remote)
	}
	if f.Local != "/a/b" {
		t.Errorf("local trailing slash should be trimmed, got %q", f.Local)
	}
	if cfg.LogMode != "extended" {
		t.Errorf("log_mode = %q", cfg.LogMode)
	}
	if !cfg.IsHTTPEnabled() {
		t.Error("http should be enabled by default")
	}
	if cfg.HTTPPort != 9277 {
		t.Errorf("default http_port = %d", cfg.HTTPPort)
	}
}

func TestConfig_DefaultsForEmptyModes(t *testing.T) {
	path := writeConfigFile(t, `
token: "tok"
folders:
  - local: "/a"
    remote: "disk:/r"
`)
	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Folders[0].SyncMode != "bidirectional" || cfg.Folders[0].DeleteMode != "none" {
		t.Errorf("defaults: sync=%q delete=%q", cfg.Folders[0].SyncMode, cfg.Folders[0].DeleteMode)
	}
	if cfg.LogMode != "simple" {
		t.Errorf("default log_mode = %q", cfg.LogMode)
	}
}

func TestConfig_ValidationErrors(t *testing.T) {
	path := writeConfigFile(t, `
token: "tok"
folders:
  - local: "/a"
    remote: "disk:/r"
    sync_mode: "bidirectionl"
`)
	if _, err := config.LoadConfig(path); err == nil {
		t.Error("expected validation error for typo in sync_mode")
	}
}

func TestConfig_EnvOverrides(t *testing.T) {
	path := writeConfigFile(t, `
token: "file-token"
sync_frequency: "5m"
folders:
  - local: "/file/local"
    remote: "disk:/file/remote"
`)
	t.Setenv("YANDEX_TOKEN", "env-token")
	t.Setenv("SYNC_FREQUENCY", "30s")
	t.Setenv("LOCAL_FOLDER_1", "/env/local")
	t.Setenv("REMOTE_FOLDER_1", "disk:/env/remote")
	t.Setenv("SYNC_MODE_1", "download")
	t.Setenv("DELETE_MODE_1", "both")

	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "env-token" {
		t.Errorf("token = %q", cfg.Token)
	}
	if cfg.SyncFrequency != 30*time.Second {
		t.Errorf("freq = %v", cfg.SyncFrequency)
	}
	if len(cfg.Folders) != 1 || cfg.Folders[0].Local != "/env/local" {
		t.Fatalf("env folders should replace file folders, got %+v", cfg.Folders)
	}
	if cfg.Folders[0].SyncMode != "download" || cfg.Folders[0].DeleteMode != "both" {
		t.Errorf("env modes: %q/%q", cfg.Folders[0].SyncMode, cfg.Folders[0].DeleteMode)
	}
}

func TestConfig_EnvOnly_NoFile(t *testing.T) {
	t.Setenv("YANDEX_TOKEN", "env-token")
	t.Setenv("LOCAL_FOLDER_1", "/env/local")
	t.Setenv("REMOTE_FOLDER_1", "disk:/env/remote")

	cfg, err := config.LoadConfig(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("env-only config should load without file: %v", err)
	}
	if len(cfg.Folders) != 1 || cfg.Folders[0].Remote != "disk:/env/remote" {
		t.Errorf("folders = %+v", cfg.Folders)
	}
}

func TestConfig_NoTokenFails(t *testing.T) {
	path := writeConfigFile(t, `
folders:
  - local: "/a"
    remote: "disk:/r"
`)
	if _, err := config.LoadConfig(path); err == nil {
		t.Error("expected error for missing token")
	}
}

func TestSplitExcludePattern(t *testing.T) {
	got := config.SplitExcludePattern(" node_modules |cache_*||*.tmp| ")
	want := []string{"node_modules", "cache_*", "*.tmp"}
	if len(got) != len(want) {
		t.Fatalf("Expected %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Expected %v, got %v", want, got)
		}
	}
	if got := config.SplitExcludePattern(""); got != nil {
		t.Errorf("Expected nil for empty pattern, got %v", got)
	}
}

func TestConfig_InvalidExcludePattern(t *testing.T) {
	path := writeConfigFile(t, `
token: "tok"
folders:
  - local: "/a"
    remote: "/b"
    exclude_pattern: "node_modules|[bad"
`)
	if _, err := config.LoadConfig(path); err == nil || !strings.Contains(err.Error(), "exclude_pattern") {
		t.Errorf("Expected validation error for bad glob, got %v", err)
	}
}

package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"service-yadisk-api/internal/config"
	"service-yadisk-api/internal/httpapi"
	"service-yadisk-api/internal/logger"
	"service-yadisk-api/internal/sync"
	"service-yadisk-api/internal/yandex"
)

// Версия приложения (заполняется при сборке)
var (
	Version    = "dev"
	BuildDate  = "unknown"
	CommitHash = "unknown"
)

func main() {
	log.Printf("Starting Yandex.Disk Sync Tool v%s (%s, built %s)",
		Version, CommitHash, BuildDate)

	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "configs/config.yaml"
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		log.Fatalf("Failed to load config from %s: %v", configPath, err)
	}

	fileLogger, err := logger.NewFileLogger(cfg.LogDir, cfg.LogMaxFiles, cfg.LogMode)
	if err != nil {
		log.Fatalf("Не удалось создать логгер: %v", err)
	}
	defer fileLogger.Close()

	client := yandex.NewClient(cfg.Token)

	startTime := time.Now()
	diskInfo, err := client.GetDiskInfo(context.Background())
	if err != nil {
		log.Fatalf("Failed to connect to Yandex.Disk: %v", err)
	}

	log.Printf("✓ Connected to Yandex.Disk in %v", time.Since(startTime))
	log.Printf("  Total space: %.2f GB", float64(diskInfo.TotalSpace)/(1024*1024*1024))
	log.Printf("  Used space: %.2f GB (%.1f%%)",
		float64(diskInfo.UsedSpace)/(1024*1024*1024),
		float64(diskInfo.UsedSpace)/float64(diskInfo.TotalSpace)*100)

	syncer, err := sync.NewSyncer(client, cfg, fileLogger)
	if err != nil {
		log.Fatalf("Failed to create syncer: %v", err)
	}

	// Graceful shutdown: сигнал → отмена ctx → syncer завершает итерацию и выходит
	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	// HTTP-сервер статуса (/healthz, /stats)
	var httpServer *httpapi.Server
	if cfg.IsHTTPEnabled() {
		httpServer = httpapi.NewServer(cfg.HTTPPort, syncer, Version)
		httpServer.Start()
	}

	log.Printf("Starting sync service with frequency: %v", cfg.SyncFrequency)
	log.Printf("Configured %d folder(s) for synchronization", len(cfg.Folders))
	log.Printf("Cache: enabled=%t, path=%s", cfg.CacheEnabled, cfg.CachePath)
	log.Printf("Logging: mode=%s, dir=%s", cfg.LogMode, cfg.LogDir)
	for i, folder := range cfg.Folders {
		log.Printf("  %d. %s -> %s (%s, delete: %s)",
			i+1, folder.Local, folder.Remote, folder.SyncMode, folder.DeleteMode)
	}

	syncer.Start(ctx)

	if httpServer != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}
	log.Println("Sync service stopped")
}

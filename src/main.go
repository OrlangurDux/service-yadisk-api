package main

import (
	"log"
	"os"
	"os/signal"
	"service-yadisk-api/internal/logger"
	"syscall"
	"time"

	"service-yadisk-api/internal/config"
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

	// Определение пути к конфигурационному файлу
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "configs/config.yaml"
	}

	// Загрузка конфигурации
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		log.Fatalf("Failed to load config from %s: %v", configPath, err)
	}

	// Приоритет токена из переменной окружения
	if envToken := os.Getenv("YANDEX_TOKEN"); envToken != "" {
		cfg.Token = envToken
	}

	if cfg.Token == "" || cfg.Token == "your_oauth_token_here" {
		log.Fatal("Yandex.Disk token is not configured. Please set YANDEX_TOKEN environment variable or update config.yaml")
	}

	// Создаём логгер (в /tmp, макс. 5 файлов)
	fileLogger, err := logger.NewFileLogger("/tmp", 5)
	if err != nil {
		log.Fatalf("Не удалось создать логгер: %v", err)
	}
	defer fileLogger.Close()

	// Создание клиента Яндекс.Диска
	client := yandex.NewClient(cfg.Token)

	// Проверка подключения и получение информации о диске
	startTime := time.Now()
	diskInfo, err := client.GetDiskInfo()
	if err != nil {
		log.Fatalf("Failed to connect to Yandex.Disk: %v", err)
	}
	connectionTime := time.Since(startTime)

	log.Printf("✓ Connected to Yandex.Disk in %v", connectionTime)
	log.Printf("  Total space: %.2f GB", float64(diskInfo.TotalSpace)/(1024*1024*1024))
	log.Printf("  Used space: %.2f GB (%.1f%%)",
		float64(diskInfo.UsedSpace)/(1024*1024*1024),
		float64(diskInfo.UsedSpace)/float64(diskInfo.TotalSpace)*100)
	log.Printf("  Trash size: %.2f MB", float64(diskInfo.TrashSize)/(1024*1024))

	// Создание синхронизатора
	syncer, err := sync.NewSyncer(client, cfg, fileLogger)
	if err != nil {
		log.Fatalf("Failed to create syncer: %v", err)
	}

	// Обработка graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	go func() {
		sig := <-sigChan
		log.Printf("Received signal: %v", sig)
		log.Println("Shutting down sync service gracefully...")

		// Здесь можно добавить логику для корректного завершения
		// Например, завершение текущей синхронизации

		os.Exit(0)
	}()

	// Запуск сервиса синхронизации
	log.Printf("Starting sync service with frequency: %v", cfg.SyncFrequency)
	log.Printf("Configured %d folder(s) for synchronization", len(cfg.Folders))

	for i, folder := range cfg.Folders {
		log.Printf("  %d. %s -> %s (%s, delete: %s)",
			i+1, folder.Local, folder.Remote, folder.SyncMode, folder.DeleteMode)
	}

	syncer.Start()
}

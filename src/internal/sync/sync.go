package sync

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"service-yadisk-api/internal/logger"
	"strings"
	"sync"
	"time"

	"service-yadisk-api/internal/config"
	"service-yadisk-api/internal/yandex"
)

// Syncer управляет синхронизацией между локальной и удаленной файловыми системами
type Syncer struct {
	client yandex.ClientInterface
	config *config.Config
	stats  *SyncStatistics
	cache  *FileHashCache
	logger logger.Logger // новый логгер
}

// FileInfo содержит информацию о файле для сравнения
type FileInfo struct {
	Path         string    // Полный путь к файлу
	Size         int64     // Размер файла в байтах
	Modified     time.Time // Время последнего изменения
	MD5          string    // MD5 хэш содержимого
	IsDir        bool      // Является ли директорией
	RelativePath string    // Относительный путь от корня синхронизации
}

// SyncResult содержит результаты синхронизации
type SyncResult struct {
	Uploaded   []string // Загруженные файлы
	Downloaded []string // Скачанные файлы
	Skipped    []string // Пропущенные файлы (уже синхронизированы)
	Errors     []string // Ошибки синхронизации
	StartedAt  time.Time
	FinishedAt time.Time
}

// SyncStatistics содержит статистику синхронизации
type SyncStatistics struct {
	TotalSyncs            int
	TotalUploaded         int
	TotalDownloaded       int
	TotalSkipped          int
	TotalErrors           int
	LastSync              time.Time
	TotalBytesTransferred int64
}

// CacheEntry хранит информацию о файле в кэше
type CacheEntry struct {
	MD5     string    `json:"md5"`
	ModTime time.Time `json:"mod_time"`
	Size    int64     `json:"size"`
}

// FileHashCache управляет кэшированием хешей
type FileHashCache struct {
	mu      sync.RWMutex
	cache   map[string]CacheEntry
	file    string // путь к файлу кэша
	enabled bool
}

// NewFileHashCache создаёт новый кэш
func NewFileHashCache(cachePath string, enabled bool) (*FileHashCache, error) {
	if !enabled {
		return &FileHashCache{enabled: false}, nil
	}

	// Создаём директорию, если её нет
	if err := os.MkdirAll(filepath.Dir(cachePath), 0755); err != nil {
		return nil, err
	}

	fhc := &FileHashCache{
		cache:   make(map[string]CacheEntry),
		file:    cachePath,
		enabled: true,
	}

	// Загружаем существующий кэш
	data, err := os.ReadFile(cachePath)
	if err == nil {
		_ = json.Unmarshal(data, &fhc.cache)
	}

	return fhc, nil
}

// Get возвращает хеш из кэша, если файл не изменился
func (fhc *FileHashCache) Get(path string, modTime time.Time, size int64) (string, bool) {
	if !fhc.enabled {
		return "", false
	}
	fhc.mu.RLock()
	defer fhc.mu.RUnlock()

	entry, ok := fhc.cache[path]
	if !ok {
		return "", false
	}
	// Проверяем, что файл не изменился
	if entry.ModTime.Equal(modTime) && entry.Size == size {
		return entry.MD5, true
	}
	return "", false
}

// Set сохраняет хеш в кэш
func (fhc *FileHashCache) Set(path, md5 string, modTime time.Time, size int64) {
	if !fhc.enabled {
		return
	}
	fhc.mu.Lock()
	defer fhc.mu.Unlock()
	fhc.cache[path] = CacheEntry{
		MD5:     md5,
		ModTime: modTime,
		Size:    size,
	}
}

// Remove удаляет запись из кэша (например, при удалении файла)
func (fhc *FileHashCache) Remove(path string) {
	if !fhc.enabled {
		return
	}
	fhc.mu.Lock()
	defer fhc.mu.Unlock()
	delete(fhc.cache, path)
}

// Save сохраняет кэш на диск
func (fhc *FileHashCache) Save() error {
	if !fhc.enabled {
		return nil
	}
	fhc.mu.RLock()
	defer fhc.mu.RUnlock()

	data, err := json.MarshalIndent(fhc.cache, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(fhc.file, data, 0644)
}

// NewSyncer создает новый экземпляр синхронизатора
func NewSyncer(client yandex.ClientInterface, config *config.Config, logger logger.Logger) (*Syncer, error) {
	cache, err := NewFileHashCache(config.CachePath, config.CacheEnabled)
	if err != nil {
		return nil, fmt.Errorf("failed to init cache: %w", err)
	}
	return &Syncer{
		client: client,
		config: config,
		stats: &SyncStatistics{
			TotalSyncs: 0,
			LastSync:   time.Time{},
		},
		cache:  cache,
		logger: logger,
	}, nil
}

// GetStatistics возвращает текущую статистику синхронизации
func (s *Syncer) GetStatistics() *SyncStatistics {
	return s.stats
}

// Start запускает сервис синхронизации в фоновом режиме
func (s *Syncer) Start() {
	// Логирование начала работы
	fmt.Printf("🚀 Синхронизатор Яндекс.Диска запущен\n")
	fmt.Printf("⏰ Частота синхронизации: %v\n", s.config.SyncFrequency)
	fmt.Printf("📁 Папок для синхронизации: %d\n", len(s.config.Folders))

	// Немедленный запуск первой синхронизации
	s.runSync()

	// Запуск периодической синхронизации
	ticker := time.NewTicker(s.config.SyncFrequency)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.runSync()
		}
	}
}

// runSync выполняет одну итерацию синхронизации
// runSync выполняет одну итерацию синхронизации
func (s *Syncer) runSync() {
	s.stats.TotalSyncs++
	startTime := time.Now()

	// Глобальный результат для текущей сессии
	globalResult := &SyncResult{
		Uploaded:   []string{},
		Downloaded: []string{},
		Skipped:    []string{},
		Errors:     []FileError{},
		StartedAt:  startTime,
	}

	fmt.Printf("\n═══════════════════════════════════════════════════\n")
	fmt.Printf("🔄 Начало синхронизации #%d в %v\n", s.stats.TotalSyncs, startTime.Format("15:04:05"))
	fmt.Printf("═══════════════════════════════════════════════════\n")

	// Обработка всех настроенных папок
	for _, folder := range s.config.Folders {
		fmt.Printf("\n📂 Папка: %s ↔ %s (режим: %s, удаление: %s)\n",
			folder.Local, folder.Remote, folder.SyncMode, folder.DeleteMode)

		// Проверка существования локальной папки
		if _, err := os.Stat(folder.Local); os.IsNotExist(err) {
			fmt.Printf("   📁 Локальная папка не существует, создаю: %s\n", folder.Local)
			if err := os.MkdirAll(folder.Local, 0755); err != nil {
				errMsg := fmt.Sprintf("Не удалось создать локальную папку: %v", err)
				globalResult.Errors = append(globalResult.Errors, FileError{
					Path:      folder.Local,
					Operation: "folder",
					Error:     errMsg,
				})
				continue
			}
		}

		// Выполнение синхронизации
		result, err := s.BidirectionalSync(folder.Local, folder.Remote, folder.SyncMode, folder.DeleteMode)
		if err != nil {
			globalResult.Errors = append(globalResult.Errors, FileError{
				Path:      folder.Local,
				Operation: "folder",
				Error:     err.Error(),
			})
		} else {
			// Объединение результатов
			globalResult.Uploaded = append(globalResult.Uploaded, result.Uploaded...)
			globalResult.Downloaded = append(globalResult.Downloaded, result.Downloaded...)
			globalResult.Skipped = append(globalResult.Skipped, result.Skipped...)
			globalResult.Errors = append(globalResult.Errors, result.Errors...)
		}
	}

	globalResult.FinishedAt = time.Now()
	s.stats.LastSync = globalResult.FinishedAt
	duration := globalResult.FinishedAt.Sub(globalResult.StartedAt)

	// Обновление глобальной статистики
	s.stats.TotalUploaded += len(globalResult.Uploaded)
	s.stats.TotalDownloaded += len(globalResult.Downloaded)
	s.stats.TotalSkipped += len(globalResult.Skipped)
	s.stats.TotalErrors += len(globalResult.Errors)

	// Конвертируем ошибки для логгера
	var logErrors []logger.LogEntry
	for _, e := range globalResult.Errors {
		logErrors = append(logErrors, logger.LogEntry{
			Timestamp: globalResult.FinishedAt, // Можно улучшить, передавая точное время ошибки, но пока так
			Level:     "error",
			Operation: e.Operation,
			Path:      e.Path,
			Message:   e.Error,
		})
	}

	// Запись лога сессии
	if err := s.logger.LogSession(s.stats.TotalSyncs, globalResult.StartedAt, globalResult.FinishedAt,
		len(globalResult.Uploaded), len(globalResult.Downloaded), len(globalResult.Skipped), logErrors); err != nil {
		fmt.Printf("❌ Ошибка записи лога: %v\n", err)
	}

	// Сохраняем кэш хешей
	if err := s.cache.Save(); err != nil {
		fmt.Printf("❌ Ошибка сохранения кэша: %v\n", err)
	}

	// Вывод итогов в консоль
	fmt.Printf("\n═══════════════════════════════════════════════════\n")
	fmt.Printf("🏁 Сеанс #%d завершён за %v\n", s.stats.TotalSyncs, duration)
	fmt.Printf("   ↑ Загружено: %d\n", len(globalResult.Uploaded))
	fmt.Printf("   ↓ Скачано: %d\n", len(globalResult.Downloaded))
	fmt.Printf("   ⏭️ Пропущено: %d\n", len(globalResult.Skipped))
	fmt.Printf("   ❌ Ошибок: %d\n", len(globalResult.Errors))
	fmt.Printf("═══════════════════════════════════════════════════\n\n")

	// Если есть ошибки, выводим первые 5 для наглядности
	if len(globalResult.Errors) > 0 {
		fmt.Println("Первые ошибки:")
		for i, e := range globalResult.Errors {
			if i >= 5 {
				break
			}
			fmt.Printf("   [%s] %s: %s\n", e.Operation, e.Path, e.Error)
		}
		if len(globalResult.Errors) > 5 {
			fmt.Printf("   ... и ещё %d ошибок (см. лог-файл)\n", len(globalResult.Errors)-5)
		}
		fmt.Println()
	}
}

// BidirectionalSync выполняет двустороннюю синхронизацию папки
func (s *Syncer) BidirectionalSync(localPath, remotePath, syncMode, deleteMode string) (*SyncResult, error) {
	result := &SyncResult{
		Uploaded:   []string{},
		Downloaded: []string{},
		Skipped:    []string{},
		Errors:     []string{},
		StartedAt:  time.Now(),
	}

	// 1. Обеспечение существования локальной директории
	if err := os.MkdirAll(localPath, 0755); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("Не удалось создать локальную папку: %v", err))
		result.FinishedAt = time.Now()
		return result, fmt.Errorf("failed to create local directory: %w", err)
	}

	// 2. Обеспечение существования удаленной папки
	if err := s.client.EnsureFolderExists(remotePath); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("Не удалось создать удаленную папку: %v", err))
		result.FinishedAt = time.Now()
		return result, fmt.Errorf("failed to ensure remote folder exists: %w", err)
	}

	// 3. Получение списков файлов
	localFiles, err := s.getLocalFilesWithMD5(localPath)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("Не удалось получить локальные файлы: %v", err))
		result.FinishedAt = time.Now()
		return result, fmt.Errorf("failed to get local files: %w", err)
	}

	remoteFiles, err := s.getRemoteFilesWithMetadata(remotePath)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("Не удалось получить удаленные файлы: %v", err))
		result.FinishedAt = time.Now()
		return result, fmt.Errorf("failed to get remote files: %w", err)
	}

	// 4. Загрузка файлов (локальные → удаленные)
	if syncMode == "upload" || syncMode == "bidirectional" {
		s.uploadMissingFiles(localPath, remotePath, localFiles, remoteFiles, result)
	}

	// 5. Скачивание файлов (удаленные → локальные)
	if syncMode == "download" || syncMode == "bidirectional" {
		s.downloadMissingFiles(localPath, remotePath, localFiles, remoteFiles, result)
	}

	// 6. Обработка удалений
	if deleteMode != "none" {
		s.handleDeletions(localPath, remotePath, localFiles, remoteFiles, deleteMode, result)
	}

	result.FinishedAt = time.Now()
	return result, nil
}

// getLocalFilesWithMD5 получает локальные файлы с вычислением MD5
func (s *Syncer) getLocalFilesWithMD5(rootPath string) (map[string]FileInfo, error) {
	files := make(map[string]FileInfo)
	var mu sync.Mutex
	var wg sync.WaitGroup
	errChan := make(chan error, 100)

	type job struct {
		path    string
		info    os.FileInfo
		relPath string
	}
	jobs := make(chan job, 1000)

	numWorkers := 5
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				fileInfo := FileInfo{
					Path:         j.path,
					RelativePath: j.relPath,
					Size:         j.info.Size(),
					Modified:     j.info.ModTime(),
					IsDir:        j.info.IsDir(),
				}

				if !j.info.IsDir() {
					// Сначала пробуем получить из кэша
					md5Hash, found := s.cache.Get(j.path, j.info.ModTime(), j.info.Size())
					if !found {
						// Вычисляем MD5
						var err error
						md5Hash, err = calculateFileMD5(j.path)
						if err == nil {
							// Сохраняем в кэш
							s.cache.Set(j.path, md5Hash, j.info.ModTime(), j.info.Size())
						} else {
							md5Hash = ""
						}
					}
					fileInfo.MD5 = md5Hash
				}

				mu.Lock()
				files[j.relPath] = fileInfo
				mu.Unlock()
			}
		}()
	}

	err := filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == rootPath {
			return nil
		}
		relPath, err := filepath.Rel(rootPath, path)
		if err != nil {
			return err
		}
		jobs <- job{path, info, relPath}
		return nil
	})
	close(jobs)
	wg.Wait()
	close(errChan)

	if err != nil {
		return nil, err
	}
	return files, nil
}

// getRemoteFilesWithMetadata получает удаленные файлы с метаданными
func (s *Syncer) getRemoteFilesWithMetadata(remotePath string) (map[string]FileInfo, error) {
	files := make(map[string]FileInfo)

	// Получение всех ресурсов из удаленной папки
	resources, err := s.client.GetResourcesByPath(remotePath)
	if err != nil {
		return nil, err
	}

	for _, resource := range resources {
		// Пропуск самой корневой папки
		if resource.Path == remotePath {
			continue
		}

		// Вычисление относительного пути
		relPath := strings.TrimPrefix(resource.Path, remotePath+"/")
		relPath = strings.TrimPrefix(relPath, "disk:/")

		// Парсинг времени модификации
		var modifiedTime time.Time
		if resource.Modified != "" {
			modifiedTime, _ = time.Parse(time.RFC3339, resource.Modified)
		}

		fileInfo := FileInfo{
			Path:         resource.Path,
			RelativePath: relPath,
			Size:         resource.Size,
			Modified:     modifiedTime,
			MD5:          resource.MD5,
			IsDir:        resource.Type == "dir",
		}

		files[relPath] = fileInfo
	}

	return files, nil
}

// uploadMissingFiles загружает отсутствующие или измененные файлы
func (s *Syncer) uploadMissingFiles(localPath, remotePath string, localFiles, remoteFiles map[string]FileInfo, result *SyncResult) {
	type job struct {
		relPath   string
		localFile FileInfo
	}

	total := len(localFiles)
	if total == 0 {
		return
	}

	jobs := make(chan job, total)
	results := make(chan string, total)
	errs := make(chan string, total)

	var wg sync.WaitGroup
	numWorkers := 3 // не более 3 одновременных загрузок, чтобы не нагружать API
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := range jobs {
				if j.localFile.IsDir {
					remoteDirPath := remotePath + "/" + j.relPath
					if err := s.client.EnsureFolderExists(remoteDirPath); err != nil {
						errs <- fmt.Sprintf("Не удалось создать удаленную директорию %s: %v", j.relPath, err)
					}
					continue
				}

				remoteFile, exists := remoteFiles[j.relPath]
				remoteFilePath := remotePath + "/" + j.relPath

				if !exists {
					fmt.Printf("      ↑ Загрузка (новый) [воркер %d]: %s\n", workerID, j.relPath)
					if err := s.client.UploadFile(j.localFile.Path, remoteFilePath); err != nil {
						errs <- fmt.Sprintf("Не удалось загрузить %s: %v", j.relPath, err)
					} else {
						results <- j.relPath
					}
				} else {
					needsSync := s.needsSyncBasedOnMD5(j.localFile, remoteFile)
					if needsSync {
						fmt.Printf("      ↑ Загрузка (обновление) [воркер %d]: %s\n", workerID, j.relPath)
						if err := s.client.UploadFile(j.localFile.Path, remoteFilePath); err != nil {
							errs <- fmt.Sprintf("Не удалось загрузить %s: %v", j.relPath, err)
						} else {
							results <- j.relPath
						}
					}
				}
			}
		}(i)
	}

	// Отправка заданий
	for relPath, localFile := range localFiles {
		jobs <- job{relPath, localFile}
	}
	close(jobs)

	// Ожидание завершения воркеров
	wg.Wait()
	close(results)
	close(errs)

	// Сбор результатов
	for r := range results {
		result.Uploaded = append(result.Uploaded, r)
	}
	for e := range errs {
		result.Errors = append(result.Errors, e)
	}
}

// downloadMissingFiles скачивает отсутствующие или измененные файлы
func (s *Syncer) downloadMissingFiles(localPath, remotePath string, localFiles, remoteFiles map[string]FileInfo, result *SyncResult) {
	type job struct {
		relPath    string
		remoteFile FileInfo
	}

	total := len(remoteFiles)
	if total == 0 {
		return
	}

	jobs := make(chan job, total)
	results := make(chan string, total)
	errs := make(chan string, total)

	var wg sync.WaitGroup
	numWorkers := 3
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := range jobs {
				if j.remoteFile.IsDir {
					localDirPath := filepath.Join(localPath, j.relPath)
					if err := os.MkdirAll(localDirPath, 0755); err != nil {
						errs <- fmt.Sprintf("Не удалось создать локальную директорию %s: %v", j.relPath, err)
					}
					continue
				}

				localFile, exists := localFiles[j.relPath]
				localFilePath := filepath.Join(localPath, j.relPath)

				if !exists {
					fmt.Printf("      ↓ Скачивание (новый) [воркер %d]: %s\n", workerID, j.relPath)
					if err := s.client.DownloadFile(j.remoteFile.Path, localFilePath); err != nil {
						errs <- fmt.Sprintf("Не удалось скачать %s: %v", j.relPath, err)
					} else {
						results <- j.relPath
					}
				} else {
					needsSync := s.needsSyncBasedOnMD5(j.remoteFile, localFile)
					if needsSync {
						fmt.Printf("      ↓ Скачивание (обновление) [воркер %d]: %s\n", workerID, j.relPath)
						if err := s.client.DownloadFile(j.remoteFile.Path, localFilePath); err != nil {
							errs <- fmt.Sprintf("Не удалось скачать %s: %v", j.relPath, err)
						} else {
							results <- j.relPath
						}
					}
				}
			}
		}(i)
	}

	// Отправка заданий
	for relPath, remoteFile := range remoteFiles {
		jobs <- job{relPath, remoteFile}
	}
	close(jobs)

	wg.Wait()
	close(results)
	close(errs)

	for r := range results {
		result.Downloaded = append(result.Downloaded, r)
	}
	for e := range errs {
		result.Errors = append(result.Errors, e)
	}
}

// needsSyncBasedOnMD5 проверяет, нужна ли синхронизация на основе MD5
func (s *Syncer) needsSyncBasedOnMD5(source, target FileInfo) bool {
	// Если один из файлов - директория, а другой - нет, это ошибка
	if source.IsDir != target.IsDir {
		return true
	}

	// Если размеры различаются
	if source.Size != target.Size {
		return true
	}

	// Если оба имеют MD5 и они различаются
	if source.MD5 != "" && target.MD5 != "" {
		return source.MD5 != target.MD5
	}

	// Если один из MD5 отсутствует, проверяем по времени модификации
	if !source.Modified.IsZero() && !target.Modified.IsZero() {
		// Если разница во времени больше 1 секунды
		timeDiff := source.Modified.Sub(target.Modified).Abs()
		return timeDiff > time.Second
	}

	// Если нет информации для точного сравнения, предполагаем, что синхронизация нужна
	return true
}

// handleDeletions обрабатывает удаление файлов в соответствии с политикой
func (s *Syncer) handleDeletions(localPath, remotePath string, localFiles, remoteFiles map[string]FileInfo, deleteMode string, result *SyncResult) {
	// Находим файлы, которые есть локально, но отсутствуют удаленно
	if deleteMode == "local" || deleteMode == "both" {
		for relPath, localFile := range localFiles {
			if _, exists := remoteFiles[relPath]; !exists && !localFile.IsDir {
				fmt.Printf("      🗑️ Удаление локального (отсутствует в облаке): %s\n", relPath)
				if err := os.Remove(localFile.Path); err != nil {
					msg := fmt.Sprintf("Не удалось удалить локальный файл %s: %v", relPath, err)
					result.Errors = append(result.Errors, msg)
				} else {
					s.cache.Remove(localFile.Path)
				}
			}
		}
	}

	// Находим файлы, которые есть удаленно, но отсутствуют локально
	if deleteMode == "remote" || deleteMode == "both" {
		for relPath, remoteFile := range remoteFiles {
			if _, exists := localFiles[relPath]; !exists && !remoteFile.IsDir {
				remoteFilePath := remotePath + "/" + relPath
				fmt.Printf("      🗑️ Удаление удаленного (отсутствует локально): %s\n", relPath)
				if err := s.client.DeleteResource(remoteFilePath, false); err != nil {
					msg := fmt.Sprintf("Не удалось удалить удаленный файл %s: %v", relPath, err)
					result.Errors = append(result.Errors, msg)
				}
			}
		}
	}
}

// shortMD5 возвращает сокращенную версию MD5 для отображения
func shortMD5(md5 string) string {
	if len(md5) > 8 {
		return md5[:8] + "..."
	}
	return md5
}

// calculateFileMD5 вычисляет MD5 хэш файла
func calculateFileMD5(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := md5.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

// CalculateFileMD5 - экспортируемая версия для тестов
func CalculateFileMD5(filePath string) (string, error) {
	return calculateFileMD5(filePath)
}

// NeedsSyncBasedOnMD5 - экспортируемая версия для тестов
func (s *Syncer) NeedsSyncBasedOnMD5(source, target FileInfo) bool {
	return s.needsSyncBasedOnMD5(source, target)
}

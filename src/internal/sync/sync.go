package sync

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"service-yadisk-api/internal/config"
	"service-yadisk-api/internal/logger"
	"service-yadisk-api/internal/models"
	"service-yadisk-api/internal/yandex"
)

// Syncer управляет синхронизацией между локальной и удаленной файловыми системами.
//
// Архитектура: единый merge-проход. Сначала строится план (planOps) — для каждого
// относительного пути из объединения локальных и удалённых файлов выбирается ровно
// одно действие (upload / download / skip / delete_local / delete_remote).
// Затем план исполняется пулами воркеров. Это устраняет класс ошибок
// «снимок состояния устарел» и двойную передачу файла при конфликте.
//
// Распространение удалений: кэш хешей хранит историю (флаг Synced и последний
// известный RemoteMD5). Если файл был синхронизирован, а теперь отсутствует на
// одной из сторон и не менялся на другой — считается удалённым, и удаление
// распространяется при разрешении delete_mode.
// cacheSaveInterval — периодический сброс кэша на диск во время сессии,
// чтобы падение/ребут не обнулял выполненную работу по хешированию.
const cacheSaveInterval = time.Minute

type Syncer struct {
	client    yandex.ClientInterface
	config    *config.Config
	stats     *models.SyncStatistics
	statsMu   sync.Mutex
	cache     *FileHashCache
	logger    logger.Logger
	knownDirs sync.Map // подтверждённые удалённые директории (экономия API-вызовов)
	syncing   atomic.Bool

	progressMu     sync.Mutex
	progress       models.SessionProgress
	progressActive bool
}

// FileHashCache управляет кэшированием хешей и истории синхронизации
type FileHashCache struct {
	mu      sync.RWMutex
	cache   map[string]models.CacheEntry
	file    string
	enabled bool
}

// NewFileHashCache создаёт новый кэш
func NewFileHashCache(cachePath string, enabled bool) (*FileHashCache, error) {
	if !enabled {
		return &FileHashCache{enabled: false}, nil
	}

	if err := os.MkdirAll(filepath.Dir(cachePath), 0755); err != nil {
		return nil, err
	}

	fhc := &FileHashCache{
		cache:   make(map[string]models.CacheEntry),
		file:    cachePath,
		enabled: true,
	}

	data, err := os.ReadFile(cachePath)
	if err == nil {
		_ = json.Unmarshal(data, &fhc.cache)
	}

	return fhc, nil
}

// Get возвращает хеш из кэша, если файл не изменился
func (fhc *FileHashCache) Get(path string, modTime time.Time, size int64) (models.CacheEntry, bool) {
	if !fhc.enabled {
		return models.CacheEntry{}, false
	}
	fhc.mu.RLock()
	defer fhc.mu.RUnlock()

	entry, ok := fhc.cache[path]
	if !ok {
		return models.CacheEntry{}, false
	}
	if entry.ModTime.Equal(modTime) && entry.Size == size {
		return entry, true
	}
	return models.CacheEntry{}, false
}

// Lookup возвращает запись кэша без проверки свежести (для истории синхронизации)
func (fhc *FileHashCache) Lookup(path string) (models.CacheEntry, bool) {
	if !fhc.enabled {
		return models.CacheEntry{}, false
	}
	fhc.mu.RLock()
	defer fhc.mu.RUnlock()
	e, ok := fhc.cache[path]
	return e, ok
}

// Set сохраняет запись в кэш
func (fhc *FileHashCache) Set(path string, entry models.CacheEntry) {
	if !fhc.enabled {
		return
	}
	fhc.mu.Lock()
	defer fhc.mu.Unlock()
	fhc.cache[path] = entry
}

// Remove удаляет запись из кэша
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
func NewSyncer(client yandex.ClientInterface, config *config.Config, log logger.Logger) (*Syncer, error) {
	cache, err := NewFileHashCache(config.CachePath, config.CacheEnabled)
	if err != nil {
		return nil, fmt.Errorf("failed to init cache: %w", err)
	}
	return &Syncer{
		client: client,
		config: config,
		stats:  &models.SyncStatistics{},
		cache:  cache,
		logger: log,
	}, nil
}

// GetStatistics возвращает копию текущей статистики (безопасно для чтения извне)
func (s *Syncer) GetStatistics() models.SyncStatistics {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	return *s.stats
}

// IsSyncing сообщает, выполняется ли сейчас синхронизация
func (s *Syncer) IsSyncing() bool {
	return s.syncing.Load()
}

// Progress возвращает живой прогресс текущей сессии (nil, если синхронизация не идёт)
func (s *Syncer) Progress() *models.SessionProgress {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	if !s.progressActive {
		return nil
	}
	p := s.progress
	return &p
}

// progressStart начинает отслеживание прогресса для папки
func (s *Syncer) progressStart(folder string) {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	s.progress = models.SessionProgress{
		Folder:    folder,
		Phase:     "scan",
		StartedAt: time.Now(),
	}
	s.progressActive = true
}

// progressPhase меняет фазу (и при первом переходе к execute задаёт Planned)
func (s *Syncer) progressPhase(phase string, planned int) {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	s.progress.Phase = phase
	if planned >= 0 {
		s.progress.Planned = planned
	}
}

// progressBump инкрементирует счётчики текущей сессии
func (s *Syncer) progressBump(up, down, skipped, delLocal, delRemote, errs int, bytes int64) {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	s.progress.Uploaded += up
	s.progress.Downloaded += down
	s.progress.Skipped += skipped
	s.progress.DeletedLocal += delLocal
	s.progress.DeletedRemote += delRemote
	s.progress.Errors += errs
	s.progress.BytesTransferred += bytes
	s.progress.Processed += up + down + delLocal + delRemote
}

// progressStop завершает отслеживание (Progress() вернёт nil)
func (s *Syncer) progressStop() {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	s.progressActive = false
}

// Start запускает сервис синхронизации, работает до отмены ctx.
// При отмене завершает текущий файл (передачи прерываются через ctx) и
// сохраняет кэш перед выходом.
func (s *Syncer) Start(ctx context.Context) {
	defer func() {
		if err := s.cache.Save(); err != nil {
			fmt.Printf("❌ Ошибка сохранения кэша: %v\n", err)
		}
	}()

	fmt.Printf("🚀 Синхронизатор Яндекс.Диска запущен\n")
	fmt.Printf("⏰ Частота синхронизации: %v\n", s.config.SyncFrequency)
	fmt.Printf("📁 Папок для синхронизации: %d\n", len(s.config.Folders))

	s.runSync(ctx)

	ticker := time.NewTicker(s.config.SyncFrequency)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("\n🛑 Остановка синхронизатора (получен сигнал завершения)")
			return
		case <-ticker.C:
			s.runSync(ctx)
		}
	}
}

// runSync выполняет одну итерацию синхронизации
func (s *Syncer) runSync(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}

	s.syncing.Store(true)
	defer s.syncing.Store(false)
	defer s.progressStop()

	// Периодический сброс кэша на диск во время сессии:
	// при падении/ребуте сохранённая работа по хешированию не потеряется
	cacheFlushDone := make(chan struct{})
	defer close(cacheFlushDone)
	go func() {
		t := time.NewTicker(cacheSaveInterval)
		defer t.Stop()
		for {
			select {
			case <-cacheFlushDone:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				_ = s.cache.Save()
			}
		}
	}()

	s.statsMu.Lock()
	s.stats.TotalSyncs++
	sessionID := s.stats.TotalSyncs
	s.statsMu.Unlock()

	if err := s.logger.BeginSession(sessionID); err != nil {
		fmt.Printf("❌ Ошибка открытия лога сессии: %v\n", err)
	}

	startTime := time.Now()
	globalResult := &models.SyncResult{StartedAt: startTime}

	fmt.Printf("\n═══════════════════════════════════════════════════\n")
	fmt.Printf("🔄 Начало синхронизации #%d в %v\n", sessionID, startTime.Format("15:04:05"))
	fmt.Printf("═══════════════════════════════════════════════════\n")

	for _, folder := range s.config.Folders {
		if ctx.Err() != nil {
			break
		}

		fmt.Printf("\n📂 Папка: %s ↔ %s (режим: %s, удаление: %s)\n",
			folder.Local, folder.Remote, folder.SyncMode, folder.DeleteMode)

		if _, err := os.Stat(folder.Local); os.IsNotExist(err) {
			fmt.Printf("   📁 Локальная папка не существует, создаю: %s\n", folder.Local)
			if err := os.MkdirAll(folder.Local, 0755); err != nil {
				addResultError(s.logger, globalResult, "folder", folder.Local, err)
				continue
			}
		}

		result := s.syncFolder(ctx, folder.Local, folder.Remote, folder.SyncMode, folder.DeleteMode,
			config.SplitExcludePattern(folder.ExcludePattern))
		mergeResult(globalResult, result)

		// Промежуточный сброс кэша после каждой папки
		if err := s.cache.Save(); err != nil {
			fmt.Printf("❌ Ошибка сохранения кэша: %v\n", err)
		}

		if ctx.Err() != nil {
			break
		}
	}

	globalResult.FinishedAt = time.Now()
	duration := globalResult.FinishedAt.Sub(globalResult.StartedAt)

	s.statsMu.Lock()
	s.stats.LastSync = globalResult.FinishedAt
	s.stats.TotalUploaded += len(globalResult.Uploaded)
	s.stats.TotalDownloaded += len(globalResult.Downloaded)
	s.stats.TotalSkipped += len(globalResult.Skipped)
	s.stats.TotalErrors += len(globalResult.Errors)
	s.stats.TotalBytesTransferred += globalResult.BytesTransferred
	s.statsMu.Unlock()

	if err := s.logger.LogSession(sessionID, globalResult); err != nil {
		fmt.Printf("❌ Ошибка записи лога: %v\n", err)
	}
	if err := s.cache.Save(); err != nil {
		fmt.Printf("❌ Ошибка сохранения кэша: %v\n", err)
	}

	fmt.Printf("\n═══════════════════════════════════════════════════\n")
	fmt.Printf("🏁 Сеанс #%d завершён за %v\n", sessionID, duration)
	fmt.Printf("   ↑ Загружено: %d\n", len(globalResult.Uploaded))
	fmt.Printf("   ↓ Скачано: %d\n", len(globalResult.Downloaded))
	fmt.Printf("   ⏭️  Пропущено: %d\n", len(globalResult.Skipped))
	fmt.Printf("   🗑️  Удалено: %d локально, %d в облаке\n",
		len(globalResult.DeletedLocal), len(globalResult.DeletedRemote))
	fmt.Printf("   ❌ Ошибок: %d\n", len(globalResult.Errors))
	fmt.Printf("═══════════════════════════════════════════════════\n\n")

	if len(globalResult.Errors) > 0 {
		fmt.Println("Первые ошибки:")
		for i, e := range globalResult.Errors {
			if i >= 5 {
				break
			}
			fmt.Printf("   [%s] %s: %s\n", e.Operation, e.Path, e.Message)
		}
		if len(globalResult.Errors) > 5 {
			fmt.Printf("   ... и ещё %d ошибок (см. лог-файл)\n", len(globalResult.Errors)-5)
		}
		fmt.Println()
	}
}

// ---- План синхронизации ---------------------------------------------------

// fileOp — единица работы по одному файлу
type fileOp struct {
	rel    string
	local  models.FileInfo
	remote models.FileInfo
}

// syncPlan — результат merge-прохода: набор непересекающихся действий
type syncPlan struct {
	uploads       []fileOp
	downloads     []fileOp
	deletedLocal  []models.FileInfo
	deletedRemote []models.FileInfo
	ensureRemote  []string // удалённые директории к созданию (отсортированы)
	ensureLocal   []string // локальные директории к созданию
	skipped       []string
}

// syncFolder выполняет синхронизацию одной пары папок: скан → план → исполнение.
// Ошибки отдельных файлов собираются в result, не прерывая остальные.
func (s *Syncer) syncFolder(ctx context.Context, localPath, remotePath, syncMode, deleteMode string, exclude []string) *models.SyncResult {
	result := &models.SyncResult{StartedAt: time.Now()}
	defer func() { result.FinishedAt = time.Now() }()

	s.progressStart(localPath + " ↔ " + remotePath)
	defer s.progressPhase("finalize", -1)

	if err := os.MkdirAll(localPath, 0755); err != nil {
		addResultError(s.logger, result, "folder", localPath, fmt.Errorf("не удалось создать локальную папку: %w", err))
		return result
	}
	if err := s.client.EnsureFolderExists(ctx, remotePath); err != nil {
		addResultError(s.logger, result, "folder", remotePath, fmt.Errorf("не удалось создать/проверить удалённую папку: %w", err))
		return result
	}

	s.progressPhase("scan_local", -1)
	localFiles, err := s.getLocalFilesWithMD5(localPath, exclude)
	if err != nil {
		addResultError(s.logger, result, "folder", localPath, fmt.Errorf("не удалось просканировать локальные файлы: %w", err))
		return result
	}
	s.progressPhase("scan_remote", -1)
	remoteFiles, err := s.getRemoteFilesWithMetadata(ctx, remotePath, exclude)
	if err != nil {
		addResultError(s.logger, result, "folder", remotePath, fmt.Errorf("не удалось получить список удалённых файлов: %w", err))
		return result
	}

	s.progressPhase("plan", -1)
	plan := s.buildPlan(localPath, remotePath, syncMode, deleteMode, localFiles, remoteFiles)
	result.Skipped = plan.skipped
	s.progressBump(0, 0, len(plan.skipped), 0, 0, 0, 0)

	planned := len(plan.uploads) + len(plan.downloads) + len(plan.deletedLocal) + len(plan.deletedRemote)
	s.progressPhase("execute", planned)
	s.executePlan(ctx, localPath, remotePath, plan, result)

	return result
}

// buildPlan — merge-проход: для каждого relPath выбирает одно действие.
func (s *Syncer) buildPlan(localPath, remotePath, syncMode, deleteMode string, localFiles, remoteFiles map[string]models.FileInfo) *syncPlan {
	canUpload := syncMode == "upload" || syncMode == "bidirectional"
	canDownload := syncMode == "download" || syncMode == "bidirectional"
	canDelLocal := deleteMode == "local" || deleteMode == "both"
	canDelRemote := deleteMode == "remote" || deleteMode == "both"

	plan := &syncPlan{}

	// Объединение ключей, сортировка для детерминированного порядка
	keys := make([]string, 0, len(localFiles)+len(remoteFiles))
	seen := make(map[string]bool, len(localFiles))
	for k := range localFiles {
		seen[k] = true
		keys = append(keys, k)
	}
	for k := range remoteFiles {
		if !seen[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	for _, rel := range keys {
		l, lok := localFiles[rel]
		r, rok := remoteFiles[rel]

		switch {
		// Директории: только обеспечиваем существование, сами не удаляем
		// и не считаем пропущенными.
		case lok && rok:
			if l.IsDir || r.IsDir {
				continue
			}
			if !filesDiffer(l, r) {
				plan.skipped = append(plan.skipped, rel)
				// Стороны идентичны — фиксируем состояние как синхронизированное
				s.cache.Set(l.Path, models.CacheEntry{
					MD5: l.MD5, ModTime: l.Modified, Size: l.Size,
					Synced: true, RemoteMD5: r.MD5,
				})
				continue
			}
			// Конфликт содержимого — выбираем одно направление
			switch s.resolveConflict(l, r, syncMode) {
			case "upload":
				plan.uploads = append(plan.uploads, fileOp{rel, l, r})
			case "download":
				plan.downloads = append(plan.downloads, fileOp{rel, l, r})
			default:
				plan.skipped = append(plan.skipped, rel)
			}

		case lok && !rok:
			if l.IsDir {
				if canUpload {
					plan.ensureRemote = append(plan.ensureRemote, rel)
				}
				continue
			}
			switch {
			case s.wasRemoteDeleted(l):
				// Файл был синхронизирован, пропал в облаке и не менялся локально
				if canDelLocal {
					plan.deletedLocal = append(plan.deletedLocal, l)
				} else if canUpload {
					plan.uploads = append(plan.uploads, fileOp{rel, l, r})
				}
			case canUpload:
				plan.uploads = append(plan.uploads, fileOp{rel, l, r})
			case canDelLocal:
				plan.deletedLocal = append(plan.deletedLocal, l)
			}

		case rok && !lok:
			if r.IsDir {
				if canDownload {
					plan.ensureLocal = append(plan.ensureLocal, rel)
				}
				continue
			}
			switch {
			case s.wasLocalDeleted(localPath, rel, r):
				// Файл был синхронизирован, пропал локально и не менялся в облаке
				if canDelRemote {
					plan.deletedRemote = append(plan.deletedRemote, r)
				} else if canDownload {
					plan.downloads = append(plan.downloads, fileOp{rel, l, r})
				}
			case canDownload:
				plan.downloads = append(plan.downloads, fileOp{rel, l, r})
			case canDelRemote:
				plan.deletedRemote = append(plan.deletedRemote, r)
			}
		}
	}

	sort.Strings(plan.ensureRemote)
	sort.Strings(plan.ensureLocal)
	return plan
}

// resolveConflict выбирает направление для различающихся файлов.
//   - upload/download-режимы: направление задано режимом (зеркалирование).
//   - bidirectional: сначала история кэша (если менялась только одна сторона —
//     побеждает изменённая), иначе по mtime; при равном/неизвестном mtime
//     побеждает удалённая версия (download).
func (s *Syncer) resolveConflict(l, r models.FileInfo, syncMode string) string {
	switch syncMode {
	case "upload":
		return "upload"
	case "download":
		return "download"
	}

	if e, ok := s.cache.Lookup(l.Path); ok {
		localUnchanged := e.Synced && e.MD5 == l.MD5
		remoteUnchanged := e.RemoteMD5 != "" && e.RemoteMD5 == r.MD5
		switch {
		case localUnchanged && !remoteUnchanged:
			return "download" // менялась только удалённая сторона
		case remoteUnchanged && !localUnchanged:
			return "upload" // менялась только локальная сторона
		}
		// Обе стороны менялись или истории нет — решаем по времени
	}

	if l.Modified.After(r.Modified) {
		return "upload"
	}
	return "download" // удалённая новее или mtime равны/неизвестны → remote wins
}

// wasRemoteDeleted: файл был синхронизирован (Synced), исчез в облаке и
// не менялся локально → его удалили на удалённой стороне.
func (s *Syncer) wasRemoteDeleted(l models.FileInfo) bool {
	e, ok := s.cache.Lookup(l.Path)
	return ok && e.Synced && e.MD5 == l.MD5
}

// wasLocalDeleted: файл был синхронизирован, исчез локально и не менялся
// в облаке (MD5 совпадает с последним синхронизированным) → удалён локально.
func (s *Syncer) wasLocalDeleted(localPath, rel string, r models.FileInfo) bool {
	e, ok := s.cache.Lookup(filepath.Join(localPath, filepath.FromSlash(rel)))
	return ok && e.Synced && e.RemoteMD5 != "" && e.RemoteMD5 == r.MD5
}

// filesDiffer сравнивает файлы по размеру и MD5 (с запасным вариантом по mtime)
func filesDiffer(l, r models.FileInfo) bool {
	if l.Size != r.Size {
		return true
	}
	if l.MD5 != "" && r.MD5 != "" {
		return l.MD5 != r.MD5
	}
	if !l.Modified.IsZero() && !r.Modified.IsZero() {
		return l.Modified.Sub(r.Modified).Abs() > time.Second
	}
	return true
}

// ---- Исполнение плана -----------------------------------------------------

func (s *Syncer) executePlan(ctx context.Context, localPath, remotePath string, plan *syncPlan, result *models.SyncResult) {
	var mu sync.Mutex
	addErr := func(op, rel string, err error) {
		mu.Lock()
		addResultError(s.logger, result, op, rel, err)
		mu.Unlock()
		s.progressBump(0, 0, 0, 0, 0, 1, 0)
	}
	addBytes := func(n int64) {
		mu.Lock()
		result.BytesTransferred += n
		mu.Unlock()
	}

	// 1. Директории
	for _, rel := range plan.ensureLocal {
		if err := os.MkdirAll(filepath.Join(localPath, filepath.FromSlash(rel)), 0755); err != nil {
			addErr("folder", rel, err)
		}
	}
	for _, rel := range plan.ensureRemote {
		if err := s.ensureRemoteDir(ctx, remotePath+"/"+rel); err != nil {
			addErr("folder", rel, err)
		}
	}

	// 2. Передачи параллельно
	var wg sync.WaitGroup

	uploadJobs := make(chan fileOp)
	downloadJobs := make(chan fileOp)

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := range uploadJobs {
				if ctx.Err() != nil {
					return
				}
				remoteFilePath := remotePath + "/" + j.rel
				s.ensureRemoteDirQuiet(ctx, remotePath+"/"+dirOf(j.rel))
				fmt.Printf("      ↑ Загрузка [воркер %d]: %s\n", workerID, j.rel)
				if err := s.client.UploadFile(ctx, j.local.Path, remoteFilePath); err != nil {
					addErr("upload", j.rel, err)
					continue
				}
				s.cache.Set(j.local.Path, models.CacheEntry{
					MD5: j.local.MD5, ModTime: j.local.Modified, Size: j.local.Size,
					Synced: true, RemoteMD5: j.local.MD5,
				})
				addBytes(j.local.Size)
				s.progressBump(1, 0, 0, 0, 0, 0, j.local.Size)
				mu.Lock()
				result.Uploaded = append(result.Uploaded, j.rel)
				mu.Unlock()
			}
		}(i)
	}

	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := range downloadJobs {
				if ctx.Err() != nil {
					return
				}
				localFilePath := filepath.Join(localPath, filepath.FromSlash(j.rel))
				fmt.Printf("      ↓ Скачивание [воркер %d]: %s\n", workerID, j.rel)
				if err := s.client.DownloadFile(ctx, j.remote.Path, localFilePath); err != nil {
					addErr("download", j.rel, err)
					continue
				}
				// Сохраняем mtime облачной версии — сравнение по времени остаётся честным
				if !j.remote.Modified.IsZero() {
					_ = os.Chtimes(localFilePath, time.Now(), j.remote.Modified)
				}
				s.cache.Set(localFilePath, models.CacheEntry{
					MD5: j.remote.MD5, ModTime: j.remote.Modified, Size: j.remote.Size,
					Synced: true, RemoteMD5: j.remote.MD5,
				})
				addBytes(j.remote.Size)
				s.progressBump(0, 1, 0, 0, 0, 0, j.remote.Size)
				mu.Lock()
				result.Downloaded = append(result.Downloaded, j.rel)
				mu.Unlock()
			}
		}(i)
	}

	go func() {
		defer close(uploadJobs)
		for _, j := range plan.uploads {
			select {
			case <-ctx.Done():
				return
			case uploadJobs <- j:
			}
		}
	}()
	go func() {
		defer close(downloadJobs)
		for _, j := range plan.downloads {
			select {
			case <-ctx.Done():
				return
			case downloadJobs <- j:
			}
		}
	}()

	wg.Wait()

	// 3. Удаления после передач (локальные быстро — последовательно)
	for _, f := range plan.deletedLocal {
		if ctx.Err() != nil {
			break
		}
		fmt.Printf("      🗑️  Удаление локального: %s\n", f.RelativePath)
		if err := os.Remove(f.Path); err != nil {
			addErr("delete_local", f.RelativePath, err)
			continue
		}
		s.cache.Remove(f.Path)
		s.progressBump(0, 0, 0, 1, 0, 0, 0)
		result.DeletedLocal = append(result.DeletedLocal, f.RelativePath)
	}

	var delWg sync.WaitGroup
	delJobs := make(chan models.FileInfo, len(plan.deletedRemote))
	for i := 0; i < 2; i++ {
		delWg.Add(1)
		go func() {
			defer delWg.Done()
			for f := range delJobs {
				if ctx.Err() != nil {
					return
				}
				fmt.Printf("      🗑️  Удаление удалённого: %s\n", f.RelativePath)
				if err := s.client.DeleteResource(ctx, f.Path, false); err != nil {
					addErr("delete_remote", f.RelativePath, err)
					continue
				}
				s.cache.Remove(filepath.Join(localPath, filepath.FromSlash(f.RelativePath)))
				s.progressBump(0, 0, 0, 0, 1, 0, 0)
				mu.Lock()
				result.DeletedRemote = append(result.DeletedRemote, f.RelativePath)
				mu.Unlock()
			}
		}()
	}
	for _, f := range plan.deletedRemote {
		delJobs <- f
	}
	close(delJobs)
	delWg.Wait()
}

// dirOf возвращает родительский относительный путь ("" для корня)
func dirOf(rel string) string {
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		return rel[:i]
	}
	return ""
}

// ensureRemoteDir создаёт удалённую директорию с кэшированием подтверждённых
func (s *Syncer) ensureRemoteDir(ctx context.Context, remotePath string) error {
	clean := strings.TrimPrefix(remotePath, "disk:/")
	clean = strings.Trim(clean, "/")
	if clean == "" {
		return nil
	}

	// Находим самый длинный уже подтверждённый префикс
	parts := strings.Split(clean, "/")
	confirmed := 0
	for i := len(parts); i > 0; i-- {
		if _, ok := s.knownDirs.Load("disk:/" + strings.Join(parts[:i], "/")); ok {
			confirmed = i
			break
		}
	}

	if confirmed == len(parts) {
		return nil
	}

	if err := s.client.EnsureFolderExists(ctx, remotePath); err != nil {
		return err
	}

	// Помечаем все префиксы как существующие
	for i := confirmed + 1; i <= len(parts); i++ {
		s.knownDirs.Store("disk:/"+strings.Join(parts[:i], "/"), true)
	}
	return nil
}

func (s *Syncer) ensureRemoteDirQuiet(ctx context.Context, remotePath string) {
	if strings.HasSuffix(remotePath, "/") || remotePath == "" {
		return
	}
	_ = s.ensureRemoteDir(ctx, remotePath)
}

// ---- Сканирование ---------------------------------------------------------

// excludedByPatterns проверяет, попадает ли относительный путь ('/'-нотация)
// под маски исключения. Маска без '/' сравнивается с каждым компонентом пути
// (node_modules выключает и саму папку, и всё её содержимое на любой глубине);
// маска с '/' — с полным относительным путём.
func excludedByPatterns(rel string, patterns []string) bool {
	for _, p := range patterns {
		if strings.ContainsRune(p, '/') {
			if ok, err := path.Match(p, rel); err == nil && ok {
				return true
			}
			continue
		}
		for _, comp := range strings.Split(rel, "/") {
			if ok, err := path.Match(p, comp); err == nil && ok {
				return true
			}
		}
	}
	return false
}

// getLocalFilesWithMD5 получает локальные файлы с вычислением MD5.
// Ключи карты — относительные пути в нотации '/' (кросс-платформенно).
func (s *Syncer) getLocalFilesWithMD5(rootPath string, exclude []string) (map[string]models.FileInfo, error) {
	files := make(map[string]models.FileInfo)
	var mu sync.Mutex
	var wg sync.WaitGroup

	type job struct {
		path    string
		info    os.FileInfo
		relPath string
	}
	jobs := make(chan job, 1000)

	const numWorkers = 5
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				fileInfo := models.FileInfo{
					Path:         j.path,
					RelativePath: j.relPath,
					Size:         j.info.Size(),
					Modified:     j.info.ModTime(),
					IsDir:        j.info.IsDir(),
				}

				if !j.info.IsDir() {
					entry, found := s.cache.Get(j.path, j.info.ModTime(), j.info.Size())
					if found {
						fileInfo.MD5 = entry.MD5
					} else if md5Hash, err := calculateFileMD5(j.path); err == nil {
						fileInfo.MD5 = md5Hash
						// Файл изменился: сбрасываем Synced, но сохраняем RemoteMD5 —
						// это история удалённой стороны для разрешения конфликтов
						old, hadOld := s.cache.Lookup(j.path)
						newEntry := models.CacheEntry{MD5: md5Hash, ModTime: j.info.ModTime(), Size: j.info.Size()}
						if hadOld {
							newEntry.RemoteMD5 = old.RemoteMD5
						}
						s.cache.Set(j.path, newEntry)
					}
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
		if len(exclude) > 0 && excludedByPatterns(filepath.ToSlash(relPath), exclude) {
			if info.IsDir() {
				return filepath.SkipDir // не спускаемся в исключённую папку
			}
			return nil
		}
		jobs <- job{path, info, filepath.ToSlash(relPath)}
		return nil
	})
	close(jobs)
	wg.Wait()

	if err != nil {
		return nil, err
	}
	return files, nil
}

// getRemoteFilesWithMetadata получает удаленные файлы с метаданными
func (s *Syncer) getRemoteFilesWithMetadata(ctx context.Context, remotePath string, exclude []string) (map[string]models.FileInfo, error) {
	files := make(map[string]models.FileInfo)

	resources, err := s.client.GetResourcesByPath(ctx, remotePath)
	if err != nil {
		return nil, err
	}

	for _, resource := range resources {
		if resource.Path == remotePath {
			continue
		}

		relPath := strings.TrimPrefix(resource.Path, remotePath+"/")
		relPath = strings.TrimPrefix(relPath, "disk:/")
		if relPath == "" || strings.HasPrefix(relPath, "disk:") {
			continue
		}
		if len(exclude) > 0 && excludedByPatterns(relPath, exclude) {
			continue
		}

		var modifiedTime time.Time
		if resource.Modified != "" {
			modifiedTime, _ = time.Parse(time.RFC3339, resource.Modified)
		}

		files[relPath] = models.FileInfo{
			Path:         resource.Path,
			RelativePath: relPath,
			Size:         resource.Size,
			Modified:     modifiedTime,
			MD5:          resource.MD5,
			IsDir:        resource.Type == "dir",
		}
	}

	return files, nil
}

// ---- helpers результатов и экспорт для тестов ------------------------------

// addResultError добавляет ошибку в результат и пишет её в логгер
func addResultError(log logger.Logger, r *models.SyncResult, op, path string, err error) {
	detail := models.ErrorDetail{
		Timestamp: time.Now(),
		Operation: op,
		Path:      path,
		Message:   err.Error(),
	}
	r.Errors = append(r.Errors, detail)
	_ = log.LogError(detail)
}

// mergeResult объединяет результаты по папкам
func mergeResult(dst, src *models.SyncResult) {
	dst.Uploaded = append(dst.Uploaded, src.Uploaded...)
	dst.Downloaded = append(dst.Downloaded, src.Downloaded...)
	dst.Skipped = append(dst.Skipped, src.Skipped...)
	dst.DeletedLocal = append(dst.DeletedLocal, src.DeletedLocal...)
	dst.DeletedRemote = append(dst.DeletedRemote, src.DeletedRemote...)
	dst.Errors = append(dst.Errors, src.Errors...)
	dst.BytesTransferred += src.BytesTransferred
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

// CalculateFileMD5 — экспортируемая версия для тестов
func CalculateFileMD5(filePath string) (string, error) {
	return calculateFileMD5(filePath)
}

// BidirectionalSync — экспортируемая обёртка syncFolder для тестов и внешних вызовов
func (s *Syncer) BidirectionalSync(ctx context.Context, localPath, remotePath, syncMode, deleteMode string) *models.SyncResult {
	return s.syncFolder(ctx, localPath, remotePath, syncMode, deleteMode, nil)
}

// SyncFolderWithExcludes — syncFolder с масками исключения (тесты/внешние вызовы)
func (s *Syncer) SyncFolderWithExcludes(ctx context.Context, localPath, remotePath, syncMode, deleteMode string, exclude []string) *models.SyncResult {
	return s.syncFolder(ctx, localPath, remotePath, syncMode, deleteMode, exclude)
}

// Cache возвращает кэш хешей (для тестов и диагностики)
func (s *Syncer) Cache() *FileHashCache {
	return s.cache
}

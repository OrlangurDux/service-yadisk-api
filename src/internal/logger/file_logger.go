package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"service-yadisk-api/internal/models"
)

// FileLogger пишет логи в файлы с автоматической ротацией.
// Режимы:
//   - simple:   файл итогов сессии  yandex-sync-{ts}-s{N}.log
//   - extended: итоги + detail-файл yandex-sync-{ts}-s{N}.detail.log
//     (потоковая запись ошибок через LogError и всех операций по сессии)
type FileLogger struct {
	dir      string
	maxFiles int
	mode     string

	mu      sync.Mutex
	detail  *os.File
	started time.Time
}

// NewFileLogger создаёт файловый логгер. При mode == "off" возвращает NilLogger.
func NewFileLogger(dir string, maxFiles int, mode string) (Logger, error) {
	switch mode {
	case ModeOff:
		return NilLogger{}, nil
	case ModeExtended:
		// итоги сессий + детальный лог операций отдельным файлом
	case ModeSimple:
		// только итоги сессий
	default:
		// неизвестное значение (Validate конфига его не пропускает) — как simple
		mode = ModeSimple
	}
	if maxFiles <= 0 {
		maxFiles = 10
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	return &FileLogger{dir: dir, maxFiles: maxFiles, mode: mode}, nil
}

// sessionStamp возвращает префикс имени файла сессии: timestamp + номер сессии.
// Timestamp защищает от перезаписи логов при рестарте (sessionID снова с 1).
func sessionStamp(sessionID int, t time.Time) string {
	return fmt.Sprintf("%s-s%d", t.Format("20060102-150405"), sessionID)
}

// BeginSession открывает detail-файл для потоковой записи (только extended).
func (f *FileLogger) BeginSession(sessionID int) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.started = time.Now()
	if f.mode != ModeExtended {
		return nil
	}
	if f.detail != nil {
		_ = f.detail.Close()
		f.detail = nil
	}
	detailDir := filepath.Join(f.dir, "detail")
	if err := os.MkdirAll(detailDir, 0755); err != nil {
		return err
	}
	path := filepath.Join(detailDir, "yandex-sync-"+sessionStamp(sessionID, f.started)+".log")
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	f.detail = file
	fmt.Fprintf(file, "Yandex.Disk Sync — детальный лог сессии #%d\nНачало: %s\n\n",
		sessionID, f.started.Format(time.RFC3339))
	return nil
}

// LogError потоково пишет ошибку в detail-файл (extended-режим).
func (f *FileLogger) LogError(e models.ErrorDetail) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.detail == nil {
		return nil
	}
	_, err := fmt.Fprintf(f.detail, "[%s] ERROR %s %s: %s\n",
		e.Timestamp.Format("15:04:05"), e.Operation, e.Path, e.Message)
	return err
}

// LogSession записывает итоговый отчёт сессии (+ detail при extended).
func (f *FileLogger) LogSession(sessionID int, result *models.SyncResult) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.mode == ModeExtended && f.detail != nil {
		f.writeDetailLocked(result)
		_ = f.detail.Close()
		f.detail = nil
	}

	stamp := sessionStamp(sessionID, f.started)
	if f.started.IsZero() {
		stamp = sessionStamp(sessionID, result.FinishedAt)
	}
	path := filepath.Join(f.dir, "yandex-sync-"+stamp+".log")
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	fmt.Fprintf(file, "Yandex.Disk Sync - сеанс #%d\n", sessionID)
	fmt.Fprintf(file, "Начало: %s\n", result.StartedAt.Format(time.RFC3339))
	fmt.Fprintf(file, "Окончание: %s\n", result.FinishedAt.Format(time.RFC3339))
	fmt.Fprintf(file, "Длительность: %v\n", result.FinishedAt.Sub(result.StartedAt))
	fmt.Fprintf(file, "Загружено: %d, Скачано: %d, Пропущено: %d\n",
		len(result.Uploaded), len(result.Downloaded), len(result.Skipped))
	fmt.Fprintf(file, "Удалено локально: %d, удалено в облаке: %d\n",
		len(result.DeletedLocal), len(result.DeletedRemote))
	fmt.Fprintf(file, "Ошибок: %d\n\n", len(result.Errors))

	if len(result.Errors) > 0 {
		fmt.Fprintln(file, "Детали ошибок:")
		for _, e := range result.Errors {
			fmt.Fprintf(file, "  [%s] %s: %s\n", e.Operation, e.Path, e.Message)
		}
	} else {
		fmt.Fprintln(file, "Ошибок нет.")
	}

	return f.rotateLocked()
}

// writeDetailLocked пишет все операции сессии в detail-файл.
func (f *FileLogger) writeDetailLocked(r *models.SyncResult) {
	w := func(title string, items []string) {
		if len(items) == 0 {
			return
		}
		fmt.Fprintf(f.detail, "\n%s (%d):\n", title, len(items))
		for _, p := range items {
			fmt.Fprintf(f.detail, "  %s\n", p)
		}
	}
	w("Загружено", r.Uploaded)
	w("Скачано", r.Downloaded)
	w("Пропущено", r.Skipped)
	w("Удалено локально", r.DeletedLocal)
	w("Удалено в облаке", r.DeletedRemote)

	fmt.Fprintf(f.detail, "\nОкончание: %s\nДлительность: %v\nОшибок: %d\n",
		r.FinishedAt.Format(time.RFC3339), r.FinishedAt.Sub(r.StartedAt), len(r.Errors))
}

// rotate удаляет старые файлы, оставляя не более maxFiles в основной папке
// и в detail/. Сортировка по времени модификации (лексикографическая
// сортировка имён ненадёжна при номерах сессий >9).
func (f *FileLogger) rotateLocked() error {
	patterns := []string{
		filepath.Join(f.dir, "yandex-sync-*.log"),
		filepath.Join(f.dir, "detail", "yandex-sync-*.log"),
	}
	for _, pattern := range patterns {
		files, err := filepath.Glob(pattern)
		if err != nil {
			return err
		}
		if len(files) <= f.maxFiles {
			continue
		}
		type fileInfo struct {
			path    string
			modTime time.Time
		}
		infos := make([]fileInfo, 0, len(files))
		for _, p := range files {
			fi, err := os.Stat(p)
			if err != nil {
				continue
			}
			infos = append(infos, fileInfo{p, fi.ModTime()})
		}
		sort.Slice(infos, func(i, j int) bool { return infos[i].modTime.Before(infos[j].modTime) })
		for i := 0; i < len(infos)-f.maxFiles; i++ {
			_ = os.Remove(infos[i].path)
		}
	}
	return nil
}

// Close закрывает открытый detail-файл.
func (f *FileLogger) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.detail != nil {
		err := f.detail.Close()
		f.detail = nil
		return err
	}
	return nil
}

var _ Logger = (*FileLogger)(nil)

package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// FileLogger пишет логи в файлы с автоматической ротацией
type FileLogger struct {
	dir      string // директория для хранения логов
	maxFiles int    // максимальное количество файлов
}

// NewFileLogger создаёт новый файловый логгер
func NewFileLogger(dir string, maxFiles int) (*FileLogger, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	return &FileLogger{
		dir:      dir,
		maxFiles: maxFiles,
	}, nil
}

// filename возвращает путь к файлу для заданного номера сессии
func (f *FileLogger) filename(sessionID int) string {
	return filepath.Join(f.dir, fmt.Sprintf("yandex-sync-%d.log", sessionID))
}

// rotate удаляет старые файлы, оставляя не более maxFiles
func (f *FileLogger) rotate() error {
	pattern := filepath.Join(f.dir, "yandex-sync-*.log")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}
	if len(files) <= f.maxFiles {
		return nil
	}
	// Сортируем по имени (номера сессий идут по порядку)
	sort.Strings(files)
	// Удаляем самые старые
	for i := 0; i < len(files)-f.maxFiles; i++ {
		_ = os.Remove(files[i]) // игнорируем ошибки удаления
	}
	return nil
}

// LogSession записывает итоговый отчёт сессии
func (f *FileLogger) LogSession(sessionID int, start, end time.Time, uploaded, downloaded, skipped int, errors []LogEntry) error {
	file, err := os.Create(f.filename(sessionID))
	if err != nil {
		return err
	}
	defer file.Close()

	fmt.Fprintf(file, "Yandex.Disk Sync - сеанс #%d\n", sessionID)
	fmt.Fprintf(file, "Начало: %s\n", start.Format(time.RFC3339))
	fmt.Fprintf(file, "Окончание: %s\n", end.Format(time.RFC3339))
	fmt.Fprintf(file, "Длительность: %v\n", end.Sub(start))
	fmt.Fprintf(file, "Загружено: %d, Скачано: %d, Пропущено: %d\n", uploaded, downloaded, skipped)
	fmt.Fprintf(file, "Ошибок: %d\n\n", len(errors))

	if len(errors) > 0 {
		fmt.Fprintln(file, "Детали ошибок:")
		for _, e := range errors {
			fmt.Fprintf(file, "  [%s] %s: %s\n", e.Operation, e.Path, e.Message)
		}
	} else {
		fmt.Fprintln(file, "Ошибок нет.")
	}

	return f.rotate()
}

// Log (реализация интерфейса) — в текущей версии не используется, но может быть расширена
func (f *FileLogger) Log(entry LogEntry) error {
	// Можно реализовать запись каждого события в отдельный файл или в общий лог,
	// но пока оставляем заглушку.
	return nil
}

// Close — ничего не делаем, так как нет открытых файлов
func (f *FileLogger) Close() error {
	return nil
}

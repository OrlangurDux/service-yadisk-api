package logger

import "time"

// LogEntry представляет одну запись в логе (ошибка или событие)
type LogEntry struct {
	Timestamp time.Time
	Level     string // "info", "error", "warning"
	Operation string // "upload", "download", "delete", "folder"
	Path      string
	Message   string
}

// Logger определяет интерфейс для логирования
type Logger interface {
	// Log записывает отдельное событие
	Log(entry LogEntry) error
	// LogSession записывает итоговый отчёт по завершении сеанса синхронизации
	LogSession(sessionID int, start, end time.Time, uploaded, downloaded, skipped int, errors []LogEntry) error
	// Close освобождает ресурсы
	Close() error
}

// NilLogger — пустая реализация, ничего не делающая (удобно для тестов)
type NilLogger struct{}

func (n NilLogger) Log(entry LogEntry) error { return nil }
func (n NilLogger) LogSession(sessionID int, start, end time.Time, uploaded, downloaded, skipped int, errors []LogEntry) error {
	return nil
}
func (n NilLogger) Close() error { return nil }

package models

import "time"

// ErrorDetail представляет детальную информацию об ошибке при работе с файлом
type ErrorDetail struct {
	Timestamp time.Time
	Operation string // "upload", "download", "delete_local", "delete_remote", "folder"
	Path      string
	Message   string
}

// SyncStats содержит сводную статистику сеанса
type SyncStats struct {
	Uploaded    int
	Downloaded  int
	Skipped     int
	TotalErrors int
}

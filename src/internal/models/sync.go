package models

import "time"

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
	Uploaded         []string      // Загруженные файлы
	Downloaded       []string      // Скачанные файлы
	Skipped          []string      // Пропущенные файлы (уже синхронизированы)
	DeletedLocal     []string      // Удалённые локально
	DeletedRemote    []string      // Удалённые в облаке
	Errors           []ErrorDetail // Ошибки синхронизации
	StartedAt        time.Time
	FinishedAt       time.Time
	BytesTransferred int64 // Суммарный объём переданных данных в сессии
}

// SessionProgress — живой прогресс текущей сессии синхронизации.
// Обновляется по ходу выполнения (для /stats), в отличие от SyncStatistics,
// которая накапливается только по завершении сессии.
type SessionProgress struct {
	Folder           string    `json:"folder,omitempty"`
	Phase            string    `json:"phase"` // scan_local | scan_remote | plan | execute | finalize
	Planned          int       `json:"planned"`
	Processed        int       `json:"processed"` // выполненные операции (upload+download+deletes)
	Uploaded         int       `json:"uploaded"`
	Downloaded       int       `json:"downloaded"`
	Skipped          int       `json:"skipped"`
	DeletedLocal     int       `json:"deleted_local"`
	DeletedRemote    int       `json:"deleted_remote"`
	Errors           int       `json:"errors"`
	BytesTransferred int64     `json:"bytes_transferred"`
	StartedAt        time.Time `json:"started_at,omitempty"`
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

// CacheEntry хранит информацию о файле в кэше.
// Помимо ускорения (MD5 не пересчитывается), служит историей синхронизации:
// по флагу Synced определяется, был ли файл ранее синхронизирован с облаком,
// что позволяет распространять удаления в bidirectional-режиме.
type CacheEntry struct {
	MD5       string    `json:"md5"`
	ModTime   time.Time `json:"mod_time"`
	Size      int64     `json:"size"`
	Synced    bool      `json:"synced,omitempty"`
	RemoteMD5 string    `json:"remote_md5,omitempty"`
}

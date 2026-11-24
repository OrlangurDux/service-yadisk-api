package logger

import (
	"service-yadisk-api/internal/models"
)

// Режимы логирования (config.LogMode)
const (
	ModeOff      = "off"      // файловые логи отключены
	ModeSimple   = "simple"   // только итоги сессий
	ModeExtended = "extended" // итоги + детальный лог операций отдельным файлом
)

// Logger определяет интерфейс для логирования сеансов синхронизации
type Logger interface {
	// BeginSession открывает сессию логирования (создаёт detail-файл в extended-режиме)
	BeginSession(sessionID int) error
	// LogError потоково записывает ошибку (в extended-режиме — в detail-файл)
	LogError(err models.ErrorDetail) error
	// LogSession записывает итоговый отчёт по завершении сеанса синхронизации
	LogSession(sessionID int, result *models.SyncResult) error
	// Close освобождает ресурсы
	Close() error
}

// NilLogger — пустая реализация, ничего не делающая (удобно для тестов)
type NilLogger struct{}

func (n NilLogger) BeginSession(sessionID int) error              { return nil }
func (n NilLogger) LogError(err models.ErrorDetail) error         { return nil }
func (n NilLogger) LogSession(id int, r *models.SyncResult) error { return nil }
func (n NilLogger) Close() error                                  { return nil }

var _ Logger = NilLogger{}

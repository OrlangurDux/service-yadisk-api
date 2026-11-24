package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"service-yadisk-api/internal/models"
	"service-yadisk-api/internal/sync"
)

// Server — лёгкий HTTP-сервер статуса: /healthz и /stats
type Server struct {
	srv       *http.Server
	syncer    *sync.Syncer
	startedAt time.Time
	version   string
}

type healthResponse struct {
	Status         string    `json:"status"`
	Version        string    `json:"version"`
	Uptime         string    `json:"uptime"`
	SyncInProgress bool      `json:"sync_in_progress"`
	LastSync       time.Time `json:"last_sync,omitempty"`
	TotalSyncs     int       `json:"total_syncs"`
}

type statsResponse struct {
	Version               string                  `json:"version"`
	UptimeSeconds         float64                 `json:"uptime_seconds"`
	SyncInProgress        bool                    `json:"sync_in_progress"`
	TotalSyncs            int                     `json:"total_syncs"`
	TotalUploaded         int                     `json:"total_uploaded"`
	TotalDownloaded       int                     `json:"total_downloaded"`
	TotalSkipped          int                     `json:"total_skipped"`
	TotalErrors           int                     `json:"total_errors"`
	TotalBytesTransferred int64                   `json:"total_bytes_transferred"`
	LastSync              time.Time               `json:"last_sync,omitempty"`
	CurrentSession        *models.SessionProgress `json:"current_session,omitempty"`
}

func NewServer(port int, syncer *sync.Syncer, version string) *Server {
	s := &Server{syncer: syncer, startedAt: time.Now(), version: version}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/stats", s.handleStats)

	s.srv = &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s
}

// Start запускает сервер в отдельной горутине
func (s *Server) Start() {
	go func() {
		if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP status server error: %v", err)
		}
	}()
	log.Printf("HTTP status server listening on %s (endpoints: /healthz, /stats)", s.srv.Addr)
}

// Shutdown останавливает сервер
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

// Handler возвращает http.Handler (для тестов через httptest)
func (s *Server) Handler() http.Handler {
	return s.srv.Handler
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	stats := s.syncer.GetStatistics()
	writeJSON(w, http.StatusOK, healthResponse{
		Status:         "ok",
		Version:        s.version,
		Uptime:         time.Since(s.startedAt).Round(time.Second).String(),
		SyncInProgress: s.syncer.IsSyncing(),
		LastSync:       stats.LastSync,
		TotalSyncs:     stats.TotalSyncs,
	})
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	stats := s.syncer.GetStatistics()
	writeJSON(w, http.StatusOK, statsResponse{
		Version:               s.version,
		UptimeSeconds:         time.Since(s.startedAt).Seconds(),
		SyncInProgress:        s.syncer.IsSyncing(),
		TotalSyncs:            stats.TotalSyncs,
		TotalUploaded:         stats.TotalUploaded,
		TotalDownloaded:       stats.TotalDownloaded,
		TotalSkipped:          stats.TotalSkipped,
		TotalErrors:           stats.TotalErrors,
		TotalBytesTransferred: stats.TotalBytesTransferred,
		LastSync:              stats.LastSync,
		CurrentSession:        s.syncer.Progress(),
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

package tests

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"service-yadisk-api/internal/httpapi"
	"service-yadisk-api/internal/logger"
	syncer "service-yadisk-api/internal/sync"
)

func TestHTTPAPI_HealthAndStats(t *testing.T) {
	cfg := testConfig(t)
	s, err := syncer.NewSyncer(NewMockClient(), cfg, logger.NilLogger{})
	if err != nil {
		t.Fatal(err)
	}

	srv := httpapi.NewServer(0, s, "test-version")
	h := srv.Handler()

	req := httptest.NewRequest("GET", "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("/healthz status = %d", rec.Code)
	}
	var health map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	if health["status"] != "ok" || health["version"] != "test-version" {
		t.Errorf("unexpected /healthz body: %v", health)
	}

	req = httptest.NewRequest("GET", "/stats", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("/stats status = %d", rec.Code)
	}
	var stats map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"total_syncs", "total_uploaded", "sync_in_progress", "total_bytes_transferred"} {
		if _, ok := stats[key]; !ok {
			t.Errorf("/stats missing key %q in %v", key, stats)
		}
	}
}

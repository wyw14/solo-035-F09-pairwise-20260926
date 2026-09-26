package api

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"

	"github.com/wyw14/solo-035/internal/service"
)

func TestDiagnosticsEndpoints(t *testing.T) {
	runtime, err := service.NewRuntime(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Bootstrap(runtime); err != nil {
		t.Fatal(err)
	}
	var web fs.FS = fstest.MapFS{"operations.html": &fstest.MapFile{Data: []byte("ok")}}
	handler := NewServer(runtime, web).Router()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/diagnostics", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("diagnostics status = %d", recorder.Code)
	}
	var report struct {
		Healthy bool   `json:"healthy"`
		Stamp   string `json:"stamp"`
		Checks  []struct {
			Name     string `json:"name"`
			Versions []struct {
				Subject string `json:"subject"`
			} `json:"versions"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if !report.Healthy || report.Stamp == "" || len(report.Checks) != 6 {
		t.Fatalf("unexpected report: healthy=%v stamp=%q checks=%d", report.Healthy, report.Stamp, len(report.Checks))
	}

	staleness := func(path string) (map[string]any, int) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		var payload map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &payload)
		return payload, rec.Code
	}

	payload, code := staleness("/api/diagnostics/stale?stamp=" + report.Stamp)
	if code != http.StatusOK || payload["stale"] != false {
		t.Fatalf("the current stamp must not be stale: %v", payload)
	}

	signals := runtime.SourceFlow.Snapshot()
	if len(signals) == 0 {
		t.Fatal("no signals registered")
	}
	runtime.Sources.BeginSwitch(signals[0].Source, time.Now())

	payload, code = staleness("/api/diagnostics/stale?stamp=" + report.Stamp)
	if code != http.StatusOK || payload["stale"] != true {
		t.Fatalf("an old stamp must be marked stale after a state change: %v", payload)
	}

	if _, code = staleness("/api/diagnostics/stale"); code != http.StatusBadRequest {
		t.Fatalf("missing stamp status = %d", code)
	}
}

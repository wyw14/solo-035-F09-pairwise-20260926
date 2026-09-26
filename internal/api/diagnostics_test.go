package api

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/wyw14/solo-035/internal/service"
)

func diagnosticsServer(t *testing.T) (*Server, *service.Runtime) {
	t.Helper()
	runtime, err := service.NewRuntime(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Bootstrap(runtime); err != nil {
		t.Fatal(err)
	}
	var web fs.FS = fstest.MapFS{"diagnostics.html": &fstest.MapFile{Data: []byte("ok")}}
	return NewServer(runtime, web), runtime
}

func decodeReport(t *testing.T, recorder *httptest.ResponseRecorder) service.DiagnosticReport {
	t.Helper()
	var report service.DiagnosticReport
	if err := json.Unmarshal(recorder.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	return report
}

func TestDiagnosticsGetRunsOnFirstRequest(t *testing.T) {
	server, _ := diagnosticsServer(t)
	recorder := httptest.NewRecorder()
	server.Router().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/diagnostics", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	report := decodeReport(t, recorder)
	if !report.Healthy || len(report.Checks) != 6 {
		t.Fatalf("report = %+v", report)
	}
}

func TestDiagnosticsPostReevaluatesChain(t *testing.T) {
	server, runtime := diagnosticsServer(t)

	first := httptest.NewRecorder()
	server.Router().ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/api/diagnostics", nil))
	before := decodeReport(t, first)
	if before.Stale {
		t.Fatal("fresh post report is stale")
	}

	// 触发信号世代变更后，缓存的报告应被标记失效。
	runtime.SourceFlow.Switch(runtime.SourceFlow.Snapshot()[0].Source)

	get := httptest.NewRecorder()
	server.Router().ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/diagnostics", nil))
	cached := decodeReport(t, get)
	// 失效报告保留生成时的结论（当时健康），仅标记结论已过时。
	if !cached.Stale || cached.RootBlocker != "" {
		t.Fatalf("cached report stale=%v root=%q, want stale with preserved conclusion", cached.Stale, cached.RootBlocker)
	}

	second := httptest.NewRecorder()
	server.Router().ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/api/diagnostics", nil))
	rerun := decodeReport(t, second)
	if rerun.Stale || rerun.RootBlocker != "signal-locks" {
		t.Fatalf("rerun stale=%v root=%q", rerun.Stale, rerun.RootBlocker)
	}
}

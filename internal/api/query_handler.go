package api

import "net/http"

func (server *Server) health(writer http.ResponseWriter, request *http.Request) {
	report := server.runtime.Diagnostics()
	snapshot := server.runtime.Snapshot()
	status := http.StatusOK
	if !report.Healthy || !snapshot.Healthy() {
		status = http.StatusServiceUnavailable
	}
	writeJSON(writer, status, map[string]any{
		"status":        "available",
		"healthy":       report.Healthy,
		"checked_at":    report.CheckedAt,
		"stamp":         report.Stamp,
		"first_blocker": report.FirstBlocker,
		"advice":        report.Advice,
		"checks":        report.Checks,
		"failures":      report.Failures(),
	})
}

// diagnostics serves the full dependency-chain report. It always answers 200
// because the payload itself carries the health verdict.
func (server *Server) diagnostics(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, http.StatusOK, server.runtime.Diagnostics())
}

// diagnosticStaleness lets a holder of an older report check whether state
// changes have invalidated it since.
func (server *Server) diagnosticStaleness(writer http.ResponseWriter, request *http.Request) {
	stamp := request.URL.Query().Get("stamp")
	if stamp == "" {
		writeError(writer, http.StatusBadRequest, "stamp query parameter is required")
		return
	}
	current := server.runtime.Diagnostics()
	writeJSON(writer, http.StatusOK, map[string]any{
		"stamp":         stamp,
		"stale":         current.Stamp != stamp,
		"current_stamp": current.Stamp,
		"checked_at":    current.CheckedAt,
	})
}

func (server *Server) operations(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{"operations": server.runtime.Operations()})
}

func (server *Server) equipment(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{"equipment": server.runtime.Equipment()})
}

func (server *Server) interlocks(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{"interlocks": server.runtime.Interlocks()})
}

func (server *Server) incidents(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{"incidents": server.runtime.Incidents()})
}

func (server *Server) snapshot(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, http.StatusOK, server.runtime.Snapshot())
}

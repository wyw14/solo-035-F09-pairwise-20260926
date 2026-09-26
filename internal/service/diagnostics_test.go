package service

import (
	"strings"
	"testing"
	"time"
)

func bootstrappedDiagnosticsRuntime(t *testing.T) *Runtime {
	t.Helper()
	runtime, err := NewRuntime(t.TempDir(), func() time.Time { return time.Unix(100, 0).UTC() })
	if err != nil {
		t.Fatal(err)
	}
	if err := Bootstrap(runtime); err != nil {
		t.Fatal(err)
	}
	return runtime
}

func TestDiagnosticsFollowsDependencyChain(t *testing.T) {
	runtime := bootstrappedDiagnosticsRuntime(t)
	report := runtime.Diagnostics()
	want := []string{"event-generations", "signal-locks", "encoder-leases", "playout-generations", "journal-order", "on-air-interlocks"}
	if len(report.Checks) != len(want) {
		t.Fatalf("checks = %d, want %d", len(report.Checks), len(want))
	}
	for i, name := range want {
		check := report.Checks[i]
		if check.Name != name {
			t.Fatalf("check %d = %s, want %s", i, check.Name, name)
		}
		if !check.Passed {
			t.Fatalf("%s failed on a healthy runtime: %s", name, check.Detail)
		}
		if check.Blocked {
			t.Fatalf("%s must not be blocked on a healthy runtime", name)
		}
		if len(check.Versions) == 0 {
			t.Fatalf("%s references no state versions", name)
		}
	}
	if !report.Healthy || report.FirstBlocker != "" || report.Advice != "" {
		t.Fatalf("healthy runtime produced blocker %q", report.FirstBlocker)
	}
	if report.Stamp == "" {
		t.Fatal("report stamp is empty")
	}
	if runtime.DiagnosticsStale(report) {
		t.Fatal("a fresh report must not be stale")
	}
}

func TestDiagnosticsStampTracksStateChanges(t *testing.T) {
	runtime := bootstrappedDiagnosticsRuntime(t)
	first := runtime.Diagnostics()
	second := runtime.Diagnostics()
	if first.Stamp != second.Stamp {
		t.Fatal("stamp changed without any state change")
	}
	signals := runtime.SourceFlow.Snapshot()
	if len(signals) == 0 {
		t.Fatal("no signals registered")
	}
	runtime.Sources.BeginSwitch(signals[0].Source, runtime.Clock())
	if !runtime.DiagnosticsStale(first) {
		t.Fatal("a report drawn before a state change must be marked stale")
	}
	if runtime.DiagnosticsStale(runtime.Diagnostics()) {
		t.Fatal("a fresh report must not be stale")
	}
}

func TestDiagnosticsChainFindsEarliestBlocker(t *testing.T) {
	runtime := bootstrappedDiagnosticsRuntime(t)
	signals := runtime.SourceFlow.Snapshot()
	if len(signals) == 0 {
		t.Fatal("no signals registered")
	}
	runtime.Sources.BeginSwitch(signals[0].Source, runtime.Clock())
	report := runtime.Diagnostics()
	if report.Healthy {
		t.Fatal("expected an unhealthy report")
	}
	if report.FirstBlocker != "signal-locks" {
		t.Fatalf("first blocker = %q, want signal-locks", report.FirstBlocker)
	}
	if !strings.Contains(report.Advice, "signal-locks") {
		t.Fatalf("advice %q does not point at signal-locks", report.Advice)
	}
	checks := make(map[string]DiagnosticCheck)
	for _, check := range report.Checks {
		checks[check.Name] = check
	}
	if checks["signal-locks"].Passed {
		t.Fatal("signal-locks must fail after the lock is dropped")
	}
	if checks["signal-locks"].Impact == "" {
		t.Fatal("a failing check must explain its downstream impact")
	}
	for _, name := range []string{"playout-generations", "journal-order", "on-air-interlocks"} {
		check := checks[name]
		if !check.Blocked {
			t.Fatalf("%s must be blocked by the upstream signal failure", name)
		}
		if !strings.Contains(check.Impact, "signal-locks") {
			t.Fatalf("%s impact %q does not name the upstream failure", name, check.Impact)
		}
	}
	if checks["encoder-leases"].Blocked {
		t.Fatal("encoder-leases does not depend on signal-locks and must not be blocked")
	}
	if !checks["journal-order"].Passed {
		t.Fatal("journal-order must still pass on its own logic")
	}
}

package service

import (
	"testing"
	"time"

	"github.com/wyw14/solo-035/internal/model"
)

func healthyDiagnosticRuntime(t *testing.T) *Runtime {
	t.Helper()
	runtime, err := NewRuntime(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Bootstrap(runtime); err != nil {
		t.Fatal(err)
	}
	return runtime
}

func findCheck(report DiagnosticReport, name string) DiagnosticCheck {
	for _, check := range report.Checks {
		if check.Name == name {
			return check
		}
	}
	return DiagnosticCheck{Name: name + " (missing)"}
}

func TestDiagnosticsHealthyChainPassesInOrder(t *testing.T) {
	runtime := healthyDiagnosticRuntime(t)
	report := runtime.RunDiagnostics()

	if !report.Healthy {
		t.Fatalf("healthy = false: %+v", report.Failures())
	}
	if report.RootBlocker != "" {
		t.Fatalf("root blocker = %q, want none", report.RootBlocker)
	}
	wantOrder := []string{
		"event-generations",
		"signal-locks",
		"encoder-leases",
		"playout-generations",
		"journal-order",
		"on-air-interlocks",
	}
	if len(report.Checks) != len(wantOrder) {
		t.Fatalf("checks = %d, want %d", len(report.Checks), len(wantOrder))
	}
	for index, name := range wantOrder {
		if report.Checks[index].Name != name {
			t.Fatalf("check[%d] = %q, want %q", index, report.Checks[index].Name, name)
		}
		if report.Checks[index].Status != CheckPassed {
			t.Fatalf("%s status = %s, want passed", name, report.Checks[index].Status)
		}
		if len(report.Checks[index].StateRefs) == 0 {
			t.Fatalf("%s has no state version refs", name)
		}
	}
	if len(report.StateVersions) == 0 {
		t.Fatal("report carries no state version fingerprint")
	}
}

func TestDiagnosticsSignalFailureBlocksDownstreamAndIsRoot(t *testing.T) {
	runtime := healthyDiagnosticRuntime(t)
	source := runtime.SourceFlow.Snapshot()[0]

	// 重新切换信号会提升世代并清除视音频锁定，最早阻断点落在信号环节。
	runtime.SourceFlow.Switch(source.Source)

	report := runtime.RunDiagnostics()
	if report.Healthy {
		t.Fatal("report is healthy after signal lock loss")
	}
	if report.RootBlocker != "signal-locks" {
		t.Fatalf("root blocker = %q, want signal-locks", report.RootBlocker)
	}
	if report.RecommendedFirstAction == "" {
		t.Fatal("root blocker carries no first action")
	}

	signal := findCheck(report, "signal-locks")
	if signal.Status != CheckFailed || signal.Recommendation == "" {
		t.Fatalf("signal check = %+v, want failed with recommendation", signal)
	}
	wantImpact := map[string]bool{
		"encoder-leases":      true,
		"playout-generations": true,
		"journal-order":       true,
		"on-air-interlocks":   true,
	}
	if len(signal.Impact) != len(wantImpact) {
		t.Fatalf("signal impact = %v, want %v", signal.Impact, wantImpact)
	}
	for _, impacted := range signal.Impact {
		if !wantImpact[impacted] {
			t.Fatalf("unexpected impacted stage %q", impacted)
		}
	}
	for _, name := range []string{"encoder-leases", "playout-generations", "journal-order", "on-air-interlocks"} {
		check := findCheck(report, name)
		if check.Status != CheckBlocked || !check.Blocked || check.BlockedBy != "signal-locks" {
			t.Fatalf("%s = %+v, want blocked by signal-locks", name, check)
		}
	}
	if len(report.FailedChecks()) != 1 || len(report.BlockedChecks()) != 4 || len(report.Failures()) != 5 {
		t.Fatalf("failure split = %d failed / %d blocked, want 1 / 4", len(report.FailedChecks()), len(report.BlockedChecks()))
	}
}

func TestDiagnosticsDownstreamFailureDoesNotImplicateUpstream(t *testing.T) {
	runtime := healthyDiagnosticRuntime(t)
	event := runtime.Events.List()[0]

	// 字幕编码器报错只会让链尾的联锁环节失败。
	runtime.CaptionFlow.ReportError(event.ID, event.Revision, "caption encoder offline")

	report := runtime.RunDiagnostics()
	if report.RootBlocker != "on-air-interlocks" {
		t.Fatalf("root blocker = %q, want on-air-interlocks", report.RootBlocker)
	}
	for _, name := range []string{"event-generations", "signal-locks", "encoder-leases", "playout-generations", "journal-order"} {
		if check := findCheck(report, name); check.Status != CheckPassed {
			t.Fatalf("%s = %s, want passed", name, check.Status)
		}
	}
	interlocks := findCheck(report, "on-air-interlocks")
	if interlocks.Status != CheckFailed || len(interlocks.Impact) != 0 {
		t.Fatalf("interlocks = %+v, want failed with no downstream impact", interlocks)
	}
}

func TestDiagnosticsIndependentFailureIsNotMaskedByUpstream(t *testing.T) {
	runtime := healthyDiagnosticRuntime(t)
	source := runtime.SourceFlow.Snapshot()[0]
	event := runtime.Events.List()[0]

	runtime.SourceFlow.Switch(source.Source)
	runtime.CaptionFlow.ReportError(event.ID, event.Revision, "caption encoder offline")

	report := runtime.RunDiagnostics()
	// 最早阻断点仍是信号，但联锁自身的独立失败必须保留为 failed。
	if report.RootBlocker != "signal-locks" {
		t.Fatalf("root blocker = %q, want signal-locks", report.RootBlocker)
	}
	if interlocks := findCheck(report, "on-air-interlocks"); interlocks.Status != CheckFailed {
		t.Fatalf("interlocks status = %s, want failed (own failure visible)", interlocks.Status)
	}
}

func TestDiagnosticsStaleAfterStateChange(t *testing.T) {
	runtime := healthyDiagnosticRuntime(t)
	fresh := runtime.RunDiagnostics()
	if fresh.Stale {
		t.Fatal("fresh report is marked stale")
	}
	source := runtime.SourceFlow.Snapshot()[0]
	runtime.SourceFlow.Switch(source.Source)

	stale, ok := runtime.LatestDiagnostics()
	if !ok {
		t.Fatal("no cached diagnostics")
	}
	if !stale.Stale {
		t.Fatal("report not stale after source change")
	}
	changed := map[string]bool{}
	for _, component := range stale.ChangedStates {
		changed[component] = true
	}
	if !changed["source-state"] {
		t.Fatalf("changed states = %v, want source-state", stale.ChangedStates)
	}
	signal := findCheck(stale, "signal-locks")
	if !signal.Stale || len(signal.ChangedStates) != 1 || signal.ChangedStates[0] != "source-state" {
		t.Fatalf("signal staleness = %+v", signal)
	}
	if events := findCheck(stale, "event-generations"); events.Stale {
		t.Fatal("event check marked stale although event state did not change")
	}

	// 重新诊断后失效标记清除。
	rerun := runtime.RunDiagnostics()
	if rerun.Stale {
		t.Fatal("rerun report is still stale")
	}
	if findCheck(rerun, "signal-locks").Stale {
		t.Fatal("rerun signal check is still stale")
	}
}

func TestDiagnosticsFrameArrivalDoesNotInvalidateReport(t *testing.T) {
	runtime := healthyDiagnosticRuntime(t)
	runtime.RunDiagnostics()

	event := runtime.Events.List()[0]
	source := runtime.SourceFlow.Snapshot()[0]
	coordinator := NewCoordinator(runtime)
	frame := model.Frame{
		Source:           source.Source,
		SourceGeneration: source.Generation,
		Sequence:         source.LastFrame + 1,
		Timecode:         source.LastTimecode + 1,
		CapturedAt:       runtime.Clock().Add(40 * time.Millisecond),
		VideoLocked:      true,
		AudioLocked:      true,
	}
	if err := coordinator.PushFrame(event.ID, frame); err != nil {
		t.Fatal(err)
	}
	report, ok := runtime.LatestDiagnostics()
	if !ok {
		t.Fatal("no cached diagnostics")
	}
	if report.Stale {
		t.Fatalf("continuous frame arrival invalidated diagnostics: %v", report.ChangedStates)
	}
}

func TestDiagnosticsEachCheckCarriesVersionsAndDependencies(t *testing.T) {
	runtime := healthyDiagnosticRuntime(t)
	report := runtime.RunDiagnostics()

	versions := map[string]uint64{}
	for _, check := range report.Checks {
		for _, ref := range check.StateRefs {
			if previous, seen := versions[ref.Component]; seen && previous != ref.Version {
				t.Fatalf("component %s cited at versions %d and %d", ref.Component, previous, ref.Version)
			}
			versions[ref.Component] = ref.Version
		}
	}
	if findCheck(report, "signal-locks").DependsOn[0] != "event-generations" {
		t.Fatal("signal-locks does not depend on event-generations")
	}
	if findCheck(report, "on-air-interlocks").DependsOn[0] != "journal-order" {
		t.Fatal("on-air-interlocks does not depend on journal-order")
	}
}

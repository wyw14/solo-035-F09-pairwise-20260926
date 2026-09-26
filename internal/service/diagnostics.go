package service

import (
	"fmt"
	"hash/fnv"
	"io"
	"sort"
	"strconv"
	"time"

	"github.com/wyw14/solo-035/internal/model"
)

// StateVersion records one versioned state input a diagnostic check read,
// so operators can see exactly which generation, fence, or epoch the
// conclusion was drawn from.
type StateVersion struct {
	Subject string `json:"subject"`
	Kind    string `json:"kind"`
	Value   string `json:"value"`
}

type DiagnosticCheck struct {
	Name      string         `json:"name"`
	Passed    bool           `json:"passed"`
	Blocked   bool           `json:"blocked"`
	Detail    string         `json:"detail"`
	Impact    string         `json:"impact,omitempty"`
	DependsOn []string       `json:"depends_on,omitempty"`
	Versions  []StateVersion `json:"versions,omitempty"`
	CheckedAt time.Time      `json:"checked_at"`
}

type DiagnosticReport struct {
	Healthy      bool              `json:"healthy"`
	Checks       []DiagnosticCheck `json:"checks"`
	FirstBlocker string            `json:"first_blocker,omitempty"`
	Advice       string            `json:"advice,omitempty"`
	Stamp        string            `json:"stamp"`
	CheckedAt    time.Time         `json:"checked_at"`
}

func (report DiagnosticReport) Failures() []DiagnosticCheck {
	items := make([]DiagnosticCheck, 0)
	for _, check := range report.Checks {
		if !check.Passed {
			items = append(items, check)
		}
	}
	return items
}

// diagnosticLink is one stage of the broadcast dependency chain. Stages are
// evaluated in chain order, which is topological: every stage only depends on
// stages listed before it.
type diagnosticLink struct {
	name        string
	dependsOn   []string
	consequence string
	run         func(*Runtime, time.Time) DiagnosticCheck
}

func diagnosticChain() []diagnosticLink {
	return []diagnosticLink{
		{name: "event-generations", consequence: "downstream checks lose a trusted event generation baseline", run: (*Runtime).checkEvents},
		{name: "signal-locks", dependsOn: []string{"event-generations"}, consequence: "encoder input and frame-sync interlocks lose a locked signal", run: (*Runtime).checkSignals},
		{name: "encoder-leases", dependsOn: []string{"event-generations"}, consequence: "playout cannot obtain a valid fencing value", run: (*Runtime).checkEncoders},
		{name: "playout-generations", dependsOn: []string{"signal-locks", "encoder-leases"}, consequence: "journal proofs and on-air interlocks lose their playout baseline", run: (*Runtime).checkPlayout},
		{name: "journal-order", dependsOn: []string{"playout-generations"}, consequence: "on-air publication can no longer be proven from durable records", run: (*Runtime).checkJournal},
		{name: "on-air-interlocks", dependsOn: []string{"signal-locks", "journal-order"}, consequence: "on-air safety is directly affected", run: (*Runtime).checkInterlocks},
	}
}

func (runtime *Runtime) Diagnostics() DiagnosticReport {
	now := runtime.Clock().UTC()
	chain := diagnosticChain()
	checks := make([]DiagnosticCheck, 0, len(chain))
	for _, link := range chain {
		check := link.run(runtime, now)
		check.Name = link.name
		check.DependsOn = link.dependsOn
		checks = append(checks, check)
	}
	report := DiagnosticReport{Healthy: true, Checks: checks, CheckedAt: now}
	report.annotateChain(chain)
	report.Stamp = diagnosticsStamp(report.Checks)
	return report
}

// DiagnosticsStale reports whether an earlier diagnostic report was drawn
// from state versions that no longer match the current runtime. Any state
// change that alters a check outcome or a referenced version invalidates the
// old report.
func (runtime *Runtime) DiagnosticsStale(report DiagnosticReport) bool {
	if report.Stamp == "" {
		return true
	}
	return runtime.Diagnostics().Stamp != report.Stamp
}

// annotateChain marks checks whose upstream dependencies failed, explains the
// impact on each stage, and derives the earliest blocking point plus the
// recommended first link to fix.
func (report *DiagnosticReport) annotateChain(chain []diagnosticLink) {
	failed := make(map[string]bool)
	details := make(map[string]string)
	consequences := make(map[string]string)
	dependsOn := make(map[string][]string)
	for _, link := range chain {
		consequences[link.name] = link.consequence
		dependsOn[link.name] = link.dependsOn
	}
	for _, check := range report.Checks {
		details[check.Name] = check.Detail
		if !check.Passed {
			failed[check.Name] = true
			report.Healthy = false
		}
	}
	failures := 0
	for i := range report.Checks {
		check := &report.Checks[i]
		if !check.Passed {
			failures++
			check.Impact = consequences[check.Name]
			if report.FirstBlocker == "" {
				report.FirstBlocker = check.Name
			}
		}
		if upstream := firstFailedUpstream(check.Name, dependsOn, failed, report.Checks); upstream != "" {
			check.Blocked = true
			note := fmt.Sprintf("blocked by upstream %s: %s", upstream, details[upstream])
			if check.Impact == "" {
				check.Impact = note
			} else {
				check.Impact += "; " + note
			}
		}
	}
	if report.FirstBlocker != "" {
		report.Advice = fmt.Sprintf("resolve %s first: %s", report.FirstBlocker, details[report.FirstBlocker])
		if failures > 1 {
			report.Advice += fmt.Sprintf("; %d other failure(s) may clear once it is fixed", failures-1)
		}
	}
}

// firstFailedUpstream returns the earliest failed transitive dependency of
// name in chain order, or "" when every upstream stage passed.
func firstFailedUpstream(name string, dependsOn map[string][]string, failed map[string]bool, chain []DiagnosticCheck) string {
	seen := make(map[string]bool)
	var walk func(stage string) bool
	walk = func(stage string) bool {
		for _, dependency := range dependsOn[stage] {
			if seen[dependency] {
				continue
			}
			seen[dependency] = true
			if failed[dependency] || walk(dependency) {
				return true
			}
		}
		return false
	}
	if !walk(name) {
		return ""
	}
	for _, check := range chain {
		if check.Name != name && seen[check.Name] && failed[check.Name] {
			return check.Name
		}
	}
	return ""
}

// diagnosticsStamp fingerprints every check outcome and referenced state
// version. It deliberately excludes timestamps so identical state produces an
// identical stamp.
func diagnosticsStamp(checks []DiagnosticCheck) string {
	hash := fnv.New64a()
	for _, check := range checks {
		io.WriteString(hash, check.Name)
		io.WriteString(hash, "\x00")
		io.WriteString(hash, strconv.FormatBool(check.Passed))
		io.WriteString(hash, "\x00")
		io.WriteString(hash, check.Detail)
		versions := append([]StateVersion(nil), check.Versions...)
		sort.Slice(versions, func(i, j int) bool {
			if versions[i].Subject != versions[j].Subject {
				return versions[i].Subject < versions[j].Subject
			}
			return versions[i].Kind < versions[j].Kind
		})
		for _, version := range versions {
			io.WriteString(hash, "\x00"+version.Subject+"\x01"+version.Kind+"\x01"+version.Value)
		}
		io.WriteString(hash, "\x02")
	}
	return strconv.FormatUint(hash.Sum64(), 16)
}

func (runtime *Runtime) checkEvents(now time.Time) DiagnosticCheck {
	events := runtime.Events.List()
	check := DiagnosticCheck{Name: "event-generations", Passed: true, CheckedAt: now}
	for _, event := range events {
		subject := fmt.Sprintf("event %s", event.ID)
		check.Versions = append(check.Versions,
			StateVersion{Subject: subject, Kind: "command_epoch", Value: strconv.FormatUint(event.CommandEpoch, 10)},
			StateVersion{Subject: subject, Kind: "source_generation", Value: strconv.FormatUint(event.SourceGeneration, 10)},
			StateVersion{Subject: subject, Kind: "revision", Value: string(event.Revision)},
			StateVersion{Subject: subject, Kind: "phase", Value: string(event.Phase)},
		)
	}
	if len(events) == 0 {
		check.Detail = "runtime is ready for the first event"
		return check
	}
	for _, event := range events {
		if !event.ID.Valid() || !event.Revision.Valid() {
			check.Passed = false
			check.Detail = fmt.Sprintf("event %s has an invalid identity", event.ID)
			return check
		}
		if event.CommandEpoch == 0 || event.SourceGeneration == 0 {
			check.Passed = false
			check.Detail = fmt.Sprintf("event %s has an empty command or source generation", event.ID)
			return check
		}
	}
	check.Detail = fmt.Sprintf("%d event generations are internally consistent", len(events))
	return check
}

func (runtime *Runtime) checkSignals(now time.Time) DiagnosticCheck {
	signals := runtime.SourceFlow.Snapshot()
	check := DiagnosticCheck{Name: "signal-locks", Passed: true, CheckedAt: now}
	for _, signal := range signals {
		subject := fmt.Sprintf("source %s", signal.Source)
		check.Versions = append(check.Versions,
			StateVersion{Subject: subject, Kind: "generation", Value: strconv.FormatUint(signal.Generation, 10)},
			StateVersion{Subject: subject, Kind: "lock", Value: lockSummary(signal.VideoLocked, signal.AudioLocked)},
		)
	}
	if len(signals) == 0 {
		check.Detail = "no signal receivers are registered"
		return check
	}
	locked := 0
	for _, signal := range signals {
		if signal.Generation == 0 || !signal.Source.Valid() {
			check.Passed = false
			check.Detail = "a signal receiver has invalid generation metadata"
			return check
		}
		if signal.VideoLocked && signal.AudioLocked {
			locked++
		}
	}
	if locked == 0 {
		check.Passed = false
		check.Detail = "no registered signal has complete media lock"
		return check
	}
	check.Detail = fmt.Sprintf("%d of %d signals have complete media lock", locked, len(signals))
	return check
}

func lockSummary(video bool, audio bool) string {
	switch {
	case video && audio:
		return "video+audio"
	case video:
		return "video"
	case audio:
		return "audio"
	default:
		return "none"
	}
}

func (runtime *Runtime) checkEncoders(now time.Time) DiagnosticCheck {
	resources := runtime.EncoderFlow.Equipment()
	check := DiagnosticCheck{Name: "encoder-leases", Passed: true, CheckedAt: now}
	for _, resource := range resources {
		subject := fmt.Sprintf("encoder %s", resource.ID)
		check.Versions = append(check.Versions,
			StateVersion{Subject: subject, Kind: "fence", Value: strconv.FormatUint(resource.Lease.Fence, 10)},
			StateVersion{Subject: subject, Kind: "healthy", Value: strconv.FormatBool(resource.Healthy)},
		)
	}
	thresholds := runtime.Evaluator.Thresholds()
	if !runtime.AudioMonitor.WithinRange(thresholds.MinimumLoudness) || !runtime.AudioMonitor.WithinRange(thresholds.MaximumLoudness) {
		check.Passed = false
		check.Detail = "audio and quality thresholds disagree"
		return check
	}
	owners := make(map[model.EventID]string)
	healthy := 0
	for _, resource := range resources {
		if resource.Healthy {
			healthy++
		}
		if resource.Lease.Owner == "" {
			continue
		}
		if runtime.Encoders.Owner(resource.ID) != resource.Lease.Owner {
			check.Passed = false
			check.Detail = fmt.Sprintf("encoder %s owner view is inconsistent", resource.ID)
			return check
		}
		if previous := owners[resource.Lease.Owner]; previous != "" {
			check.Passed = false
			check.Detail = fmt.Sprintf("event %s owns both %s and %s", resource.Lease.Owner, previous, resource.ID)
			return check
		}
		if resource.Lease.Fence == 0 || !resource.Lease.ID.Valid() {
			check.Passed = false
			check.Detail = fmt.Sprintf("encoder %s has an invalid lease fence", resource.ID)
			return check
		}
		owners[resource.Lease.Owner] = resource.ID
	}
	if healthy == 0 {
		check.Passed = false
		check.Detail = "no encoder resource is healthy"
		return check
	}
	check.Detail = fmt.Sprintf("%d encoders are healthy and %d leases are fenced", healthy, len(owners))
	return check
}

func (runtime *Runtime) checkPlayout(now time.Time) DiagnosticCheck {
	outputs := runtime.PlayoutState.List()
	check := DiagnosticCheck{Name: "playout-generations", Passed: true, CheckedAt: now}
	for _, output := range outputs {
		subject := fmt.Sprintf("playout %s", output.EventID)
		check.Versions = append(check.Versions,
			StateVersion{Subject: subject, Kind: "fence", Value: strconv.FormatUint(output.Fence, 10)},
			StateVersion{Subject: subject, Kind: "revision", Value: string(output.Revision)},
			StateVersion{Subject: subject, Kind: "on_air", Value: strconv.FormatBool(output.OnAir)},
		)
	}
	for _, output := range outputs {
		if !output.EventID.Valid() || !output.Revision.Valid() || !output.Source.Valid() {
			check.Passed = false
			check.Detail = "a playout output has incomplete generation identity"
			return check
		}
		if output.OnAir && output.Fence == 0 {
			check.Passed = false
			check.Detail = fmt.Sprintf("on-air output %s has no fencing value", output.EventID)
			return check
		}
	}
	check.Detail = fmt.Sprintf("%d playout outputs retain revision and source identity", len(outputs))
	return check
}

func (runtime *Runtime) checkJournal(now time.Time) DiagnosticCheck {
	records := runtime.Journal.Records()
	check := DiagnosticCheck{Name: "journal-order", Passed: true, CheckedAt: now}
	check.Versions = append(check.Versions, StateVersion{Subject: "journal", Kind: "records", Value: strconv.Itoa(len(records))})
	proofs := make(map[model.EventID]bool)
	for _, record := range records {
		var payload map[string]any
		if err := record.Decode(&payload); err != nil {
			check.Passed = false
			check.Detail = fmt.Sprintf("journal record %s cannot be decoded", record.ID)
			return check
		}
		if record.Kind == "lip-sync-proof" {
			proofs[record.EventID] = true
		}
		if record.Kind == "on-air" && !proofs[record.EventID] {
			check.Passed = false
			check.Detail = fmt.Sprintf("event %s was published before its lip-sync proof", record.EventID)
			return check
		}
	}
	for _, operation := range runtime.Operations() {
		revision, fence, known := runtime.JournalState.Program(operation.EventID)
		if known {
			subject := fmt.Sprintf("journal %s", operation.EventID)
			check.Versions = append(check.Versions,
				StateVersion{Subject: subject, Kind: "revision", Value: string(revision)},
				StateVersion{Subject: subject, Kind: "fence", Value: strconv.FormatUint(fence, 10)},
			)
		}
		if known && (revision != operation.ProgramRevision || fence == 0) {
			check.Passed = false
			check.Detail = fmt.Sprintf("journal program generation for %s is inconsistent", operation.EventID)
			return check
		}
	}
	check.Detail = fmt.Sprintf("%d durable records at %s preserve proof-before-publication order", len(records), runtime.JournalStore.Path())
	return check
}

func (runtime *Runtime) checkInterlocks(now time.Time) DiagnosticCheck {
	interlocks := runtime.Interlocks()
	check := DiagnosticCheck{Name: "on-air-interlocks", Passed: true, CheckedAt: now}
	for _, interlock := range interlocks {
		check.Versions = append(check.Versions, StateVersion{
			Subject: fmt.Sprintf("interlock %s/%s", interlock.EventID, interlock.Name),
			Kind:    "passed",
			Value:   strconv.FormatBool(interlock.Passed),
		})
	}
	operations := runtime.Operations()
	active := make(map[model.EventID]bool)
	for _, operation := range operations {
		if operation.Phase == model.PhaseOnAir || operation.Phase == model.PhaseFailedOver {
			active[operation.EventID] = true
		}
	}
	for _, interlock := range interlocks {
		if active[interlock.EventID] && !interlock.Passed {
			check.Passed = false
			check.Detail = fmt.Sprintf("active event %s failed %s", interlock.EventID, interlock.Name)
			return check
		}
	}
	check.Detail = fmt.Sprintf("%d interlock observations match active event state", len(interlocks))
	return check
}

package service

import (
	"fmt"
	"sync"
	"time"

	"github.com/wyw14/solo-035/internal/model"
)

// DiagnosticStatus 描述检查在依赖链中的状态。
type DiagnosticStatus string

const (
	// CheckPassed 表示本环节自身检查通过，且没有失败的上游。
	CheckPassed DiagnosticStatus = "passed"
	// CheckFailed 表示本环节自身检查发现故障（无论上游状态如何，独立故障不被掩盖）。
	CheckFailed DiagnosticStatus = "failed"
	// CheckBlocked 表示本环节自身虽通过，但上游存在失败，其结果不能用于放行链路。
	CheckBlocked DiagnosticStatus = "blocked"
)

// 诊断引用的状态组件名称，也是状态版本指纹的键。
const (
	componentEvent   = "event-state"
	componentSource  = "source-state"
	componentEncoder = "encoder-state"
	componentPlayout = "playout-state"
	componentJournal = "journal-state"
	componentSync    = "sync-state"
	componentCaption = "caption-state"
)

// StateRef 记录一项检查所读取的状态组件及其版本。
type StateRef struct {
	Component string `json:"component"`
	Version   uint64 `json:"version"`
}

type DiagnosticCheck struct {
	Name    string           `json:"name"`
	Status  DiagnosticStatus `json:"status"`
	Passed  bool             `json:"passed"`
	Blocked bool             `json:"blocked"`
	Detail  string           `json:"detail"`
	// DependsOn 是本环节直接依赖的上游检查。
	DependsOn []string `json:"depends_on"`
	// BlockedBy 是直接导致本环节被阻断的失败上游。
	BlockedBy string `json:"blocked_by,omitempty"`
	// StateRefs 是本环节做出判断时引用的状态版本。
	StateRefs []StateRef `json:"state_refs"`
	// Impact 说明本环节失败时会波及哪些下游环节。
	Impact []string `json:"impact,omitempty"`
	// Recommendation 是值班员针对当前状态应先处理的动作。
	Recommendation string    `json:"recommendation,omitempty"`
	CheckedAt      time.Time `json:"checked_at"`
	// Stale 表示自本次检查之后其引用的状态已发生变化，结论可能失效。
	Stale bool `json:"stale,omitempty"`
	// ChangedStates 列出导致该检查失效的状态组件。
	ChangedStates []string `json:"changed_states,omitempty"`
}

type DiagnosticReport struct {
	Healthy   bool              `json:"healthy"`
	Checks    []DiagnosticCheck `json:"checks"`
	CheckedAt time.Time         `json:"checked_at"`
	// StateVersions 是整份报告引用的全部状态版本指纹。
	StateVersions map[string]uint64 `json:"state_versions"`
	// RootBlocker 是依赖链上最早的失败环节，应最先处理。
	RootBlocker string `json:"root_blocker,omitempty"`
	// RecommendedFirstAction 是针对最早阻断点的处置建议。
	RecommendedFirstAction string `json:"recommended_first_action,omitempty"`
	// Stale 表示报告生成后所引用的状态已经变化，需要重新诊断。
	Stale bool `json:"stale,omitempty"`
	// ChangedStates 列出报告生成后发生变化的状态组件。
	ChangedStates []string `json:"changed_states,omitempty"`
}

// Failures 返回链上未能放行的环节（含真实失败与被上游阻断）。
func (report DiagnosticReport) Failures() []DiagnosticCheck {
	items := make([]DiagnosticCheck, 0)
	for _, check := range report.Checks {
		if check.Status != CheckPassed {
			items = append(items, check)
		}
	}
	return items
}

// FailedChecks 仅返回自身发现故障的环节（不含被阻断的下游）。
func (report DiagnosticReport) FailedChecks() []DiagnosticCheck {
	items := make([]DiagnosticCheck, 0)
	for _, check := range report.Checks {
		if check.Status == CheckFailed {
			items = append(items, check)
		}
	}
	return items
}

// BlockedChecks 返回因上游失败而无法放行的下游环节。
func (report DiagnosticReport) BlockedChecks() []DiagnosticCheck {
	items := make([]DiagnosticCheck, 0)
	for _, check := range report.Checks {
		if check.Status == CheckBlocked {
			items = append(items, check)
		}
	}
	return items
}

// diagnosticSpec 描述依赖链中的一个环节。
type diagnosticSpec struct {
	name      string
	dependsOn []string
	refs      func(runtime *Runtime) []StateRef
	evaluate  func(runtime *Runtime) (bool, string, string)
}

// diagnosticChain 按信号流向排列：事件世代 → 信号锁定 → 编码器租约 →
// 播出世代 → 日志顺序 → 播出联锁。
func diagnosticChain() []diagnosticSpec {
	return []diagnosticSpec{
		{
			name:      "event-generations",
			dependsOn: nil,
			refs: func(runtime *Runtime) []StateRef {
				return []StateRef{{Component: componentEvent, Version: runtime.Events.Version()}}
			},
			evaluate: evaluateEvents,
		},
		{
			name:      "signal-locks",
			dependsOn: []string{"event-generations"},
			refs: func(runtime *Runtime) []StateRef {
				return []StateRef{{Component: componentSource, Version: runtime.Sources.Version()}}
			},
			evaluate: evaluateSignals,
		},
		{
			name:      "encoder-leases",
			dependsOn: []string{"signal-locks"},
			refs: func(runtime *Runtime) []StateRef {
				return []StateRef{{Component: componentEncoder, Version: runtime.Encoders.Version()}}
			},
			evaluate: evaluateEncoders,
		},
		{
			name:      "playout-generations",
			dependsOn: []string{"encoder-leases"},
			refs: func(runtime *Runtime) []StateRef {
				return []StateRef{{Component: componentPlayout, Version: runtime.PlayoutState.Version()}}
			},
			evaluate: evaluatePlayout,
		},
		{
			name:      "journal-order",
			dependsOn: []string{"playout-generations"},
			refs: func(runtime *Runtime) []StateRef {
				return []StateRef{{Component: componentJournal, Version: runtime.JournalState.Version()}}
			},
			evaluate: evaluateJournal,
		},
		{
			name:      "on-air-interlocks",
			dependsOn: []string{"journal-order"},
			refs: func(runtime *Runtime) []StateRef {
				return []StateRef{
					{Component: componentEvent, Version: runtime.Events.Version()},
					{Component: componentSync, Version: runtime.SyncState.Version()},
					{Component: componentCaption, Version: runtime.Captions.Version()},
					{Component: componentEncoder, Version: runtime.Encoders.Version()},
					{Component: componentSource, Version: runtime.Sources.Version()},
				}
			},
			evaluate: evaluateInterlocks,
		},
	}
}

// Diagnostics 依据当前状态即时评估整条依赖链，供健康探针使用，不缓存结果。
func (runtime *Runtime) Diagnostics() DiagnosticReport {
	return runtime.evaluateDiagnostics()
}

func (runtime *Runtime) evaluateDiagnostics() DiagnosticReport {
	now := runtime.Clock().UTC()
	specs := diagnosticChain()
	names := make([]string, len(specs))
	depends := make(map[string][]string, len(specs))
	ownPassed := make(map[string]bool, len(specs))
	details := make(map[string]string, len(specs))
	recommendations := make(map[string]string, len(specs))
	refs := make(map[string][]StateRef, len(specs))
	versions := make(map[string]uint64)
	for index, spec := range specs {
		names[index] = spec.name
		depends[spec.name] = spec.dependsOn
		specRefs := spec.refs(runtime)
		refs[spec.name] = specRefs
		for _, ref := range specRefs {
			versions[ref.Component] = ref.Version
		}
		passed, detail, recommendation := spec.evaluate(runtime)
		ownPassed[spec.name] = passed
		details[spec.name] = detail
		recommendations[spec.name] = recommendation
	}

	// blockers[name] 是 name 的传递上游中所有真实失败的环节。
	blockers := make(map[string]map[string]bool, len(specs))
	for _, name := range names {
		set := make(map[string]bool)
		for _, upstream := range depends[name] {
			if ownPassed[upstream] == false {
				set[upstream] = true
			}
			for blocked := range blockers[upstream] {
				set[blocked] = true
			}
		}
		blockers[name] = set
	}

	indexByName := make(map[string]int, len(names))
	for index, name := range names {
		indexByName[name] = index
	}

	checks := make([]DiagnosticCheck, 0, len(specs))
	for _, name := range names {
		check := DiagnosticCheck{
			Name:      name,
			Detail:    details[name],
			DependsOn: append([]string(nil), depends[name]...),
			StateRefs: append([]StateRef(nil), refs[name]...),
			CheckedAt: now,
		}
		switch {
		case !ownPassed[name]:
			check.Status = CheckFailed
			check.Passed = false
			check.Recommendation = recommendations[name]
		case len(blockers[name]) > 0:
			check.Status = CheckBlocked
			check.Blocked = true
			check.BlockedBy = closestBlocker(blockers[name], indexByName)
			check.Detail = fmt.Sprintf("upstream %s has failed; this stage was not used to clear the chain: %s", check.BlockedBy, details[name])
			check.Recommendation = fmt.Sprintf("Resolve %s first, then rerun diagnostics before trusting this stage.", check.BlockedBy)
		default:
			check.Status = CheckPassed
			check.Passed = true
		}
		checks = append(checks, check)
	}

	// 标注每个真实失败环节对下游的影响。
	for index, name := range names {
		if ownPassed[name] {
			continue
		}
		impacted := make([]string, 0)
		for _, downstream := range names[index+1:] {
			if blockers[downstream][name] {
				impacted = append(impacted, downstream)
			}
		}
		checks[index].Impact = impacted
	}

	report := DiagnosticReport{
		Healthy:       true,
		Checks:        checks,
		CheckedAt:     now,
		StateVersions: versions,
	}
	for _, check := range checks {
		if check.Status != CheckPassed {
			report.Healthy = false
		}
	}
	for _, name := range names {
		if !ownPassed[name] {
			report.RootBlocker = name
			report.RecommendedFirstAction = recommendations[name]
			break
		}
	}
	return report
}

// closestBlocker 在阻断本环节的失败上游中选取最靠近本环节者（链路顺序最靠后）。
func closestBlocker(blockers map[string]bool, indexByName map[string]int) string {
	closest := ""
	closestIndex := -1
	for name := range blockers {
		if indexByName[name] > closestIndex {
			closestIndex = indexByName[name]
			closest = name
		}
	}
	return closest
}

// diagnosticStore 缓存值班员最近一次主动发起的诊断，供后续标注失效。
type diagnosticStore struct {
	mu   sync.Mutex
	last *DiagnosticReport
}

// RunDiagnostics 重新评估整条依赖链并缓存结果。
func (runtime *Runtime) RunDiagnostics() DiagnosticReport {
	report := runtime.evaluateDiagnostics()
	if runtime.diagCache != nil {
		runtime.diagCache.mu.Lock()
		runtime.diagCache.last = &report
		runtime.diagCache.mu.Unlock()
	}
	return report
}

// LatestDiagnostics 返回最近一次诊断，并依据当前状态版本标注失效情况。
func (runtime *Runtime) LatestDiagnostics() (DiagnosticReport, bool) {
	if runtime.diagCache == nil {
		return DiagnosticReport{}, false
	}
	runtime.diagCache.mu.Lock()
	cached := runtime.diagCache.last
	runtime.diagCache.mu.Unlock()
	if cached == nil {
		return DiagnosticReport{}, false
	}
	report := *cached
	report.Checks = append([]DiagnosticCheck(nil), cached.Checks...)
	runtime.markStaleness(&report)
	return report, true
}

// markStaleness 对比报告保存的状态版本与当前版本，标记整份报告及受影响的检查为失效。
func (runtime *Runtime) markStaleness(report *DiagnosticReport) {
	current := map[string]uint64{
		componentEvent:   runtime.Events.Version(),
		componentSource:  runtime.Sources.Version(),
		componentEncoder: runtime.Encoders.Version(),
		componentPlayout: runtime.PlayoutState.Version(),
		componentJournal: runtime.JournalState.Version(),
		componentSync:    runtime.SyncState.Version(),
		componentCaption: runtime.Captions.Version(),
	}
	changed := make([]string, 0)
	for component, version := range current {
		if previous, ok := report.StateVersions[component]; ok && previous != version {
			changed = append(changed, component)
		}
	}
	if len(changed) == 0 {
		return
	}
	changedSet := make(map[string]bool, len(changed))
	for _, component := range changed {
		changedSet[component] = true
	}
	report.Stale = true
	report.ChangedStates = changed
	for index := range report.Checks {
		checkChanged := make([]string, 0)
		for _, ref := range report.Checks[index].StateRefs {
			if changedSet[ref.Component] {
				checkChanged = append(checkChanged, ref.Component)
			}
		}
		if len(checkChanged) > 0 {
			report.Checks[index].Stale = true
			report.Checks[index].ChangedStates = checkChanged
		}
	}
}

func evaluateEvents(runtime *Runtime) (bool, string, string) {
	events := runtime.Events.List()
	if len(events) == 0 {
		return true, "runtime is ready for the first event", ""
	}
	for _, event := range events {
		if !event.ID.Valid() || !event.Revision.Valid() {
			return false,
				fmt.Sprintf("event %s has an invalid identity", event.ID),
				"Restore the event from its checkpoint so its id and revision are valid before continuing the chain."
		}
		if event.CommandEpoch == 0 || event.SourceGeneration == 0 {
			return false,
				fmt.Sprintf("event %s has an empty command or source generation", event.ID),
				"Re-arm the event to rebuild its command epoch and source generation; downstream stages fence on these values."
		}
	}
	return true, fmt.Sprintf("%d event generations are internally consistent", len(events)), ""
}

func evaluateSignals(runtime *Runtime) (bool, string, string) {
	signals := runtime.SourceFlow.Snapshot()
	if len(signals) == 0 {
		return true, "no signal receivers are registered", ""
	}
	locked := 0
	for _, signal := range signals {
		if signal.Generation == 0 || !signal.Source.Valid() {
			return false,
				"a signal receiver has invalid generation metadata",
				"Re-register the signal receiver; its generation metadata is corrupt."
		}
		if signal.VideoLocked && signal.AudioLocked {
			locked++
		}
	}
	if locked == 0 {
		return false,
			"no registered signal has complete media lock",
			"Switch the source again and confirm both video and audio lock before reserving encoders."
	}
	return true, fmt.Sprintf("%d of %d signals have complete media lock", locked, len(signals)), ""
}

func evaluateEncoders(runtime *Runtime) (bool, string, string) {
	resources := runtime.EncoderFlow.Equipment()
	thresholds := runtime.Evaluator.Thresholds()
	if !runtime.AudioMonitor.WithinRange(thresholds.MinimumLoudness) || !runtime.AudioMonitor.WithinRange(thresholds.MaximumLoudness) {
		return false,
			"audio and quality thresholds disagree",
			"Reconcile the audio monitor range with the quality thresholds before accepting leases."
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
			return false,
				fmt.Sprintf("encoder %s owner view is inconsistent", resource.ID),
				"Release and re-reserve encoder leases through the control path so owners and fences agree."
		}
		if previous := owners[resource.Lease.Owner]; previous != "" {
			return false,
				fmt.Sprintf("event %s owns both %s and %s", resource.Lease.Owner, previous, resource.ID),
				"Release the duplicate encoder lease so each event fences a single encoder."
		}
		if resource.Lease.Fence == 0 || !resource.Lease.ID.Valid() {
			return false,
				fmt.Sprintf("encoder %s has an invalid lease fence", resource.ID),
				"Release and re-reserve the encoder lease so it carries a valid fence."
		}
		owners[resource.Lease.Owner] = resource.ID
	}
	if healthy == 0 {
		return false,
			"no encoder resource is healthy",
			"Recover or replace encoder hardware; no program can be configured without a healthy resource."
	}
	return true, fmt.Sprintf("%d encoders are healthy and %d leases are fenced", healthy, len(owners)), ""
}

func evaluatePlayout(runtime *Runtime) (bool, string, string) {
	outputs := runtime.PlayoutState.List()
	for _, output := range outputs {
		if !output.EventID.Valid() || !output.Revision.Valid() || !output.Source.Valid() {
			return false,
				"a playout output has incomplete generation identity",
				"Re-stage the output with its event, revision and source identity."
		}
		if output.OnAir && output.Fence == 0 {
			return false,
				fmt.Sprintf("on-air output %s has no fencing value", output.EventID),
				"Take the unfenced on-air output down and re-publish it with a valid fence."
		}
	}
	return true, fmt.Sprintf("%d playout outputs retain revision and source identity", len(outputs)), ""
}

func evaluateJournal(runtime *Runtime) (bool, string, string) {
	records := runtime.Journal.Records()
	proofs := make(map[model.EventID]bool)
	for _, record := range records {
		var payload map[string]any
		if err := record.Decode(&payload); err != nil {
			return false,
				fmt.Sprintf("journal record %s cannot be decoded", record.ID),
				"Quarantine and repair the corrupt journal record at the store path."
		}
		if record.Kind == "lip-sync-proof" {
			proofs[record.EventID] = true
		}
		if record.Kind == "on-air" && !proofs[record.EventID] {
			return false,
				fmt.Sprintf("event %s was published before its lip-sync proof", record.EventID),
				"Replay the event journal so the lip-sync proof is persisted before the on-air record."
		}
	}
	for _, operation := range runtime.Operations() {
		revision, fence, known := runtime.JournalState.Program(operation.EventID)
		if known && (revision != operation.ProgramRevision || fence == 0) {
			return false,
				fmt.Sprintf("journal program generation for %s is inconsistent", operation.EventID),
				"Restore the journal program generation so it matches the live operation fence."
		}
	}
	return true, fmt.Sprintf("%d durable records at %s preserve proof-before-publication order", len(records), runtime.JournalStore.Path()), ""
}

func evaluateInterlocks(runtime *Runtime) (bool, string, string) {
	interlocks := runtime.Interlocks()
	operations := runtime.Operations()
	active := make(map[model.EventID]bool)
	for _, operation := range operations {
		if operation.Phase == model.PhaseOnAir || operation.Phase == model.PhaseFailedOver {
			active[operation.EventID] = true
		}
	}
	for _, interlock := range interlocks {
		if active[interlock.EventID] && !interlock.Passed {
			return false,
				fmt.Sprintf("active event %s failed %s", interlock.EventID, interlock.Name),
				"Clear the failed interlock for the active event (frame sync or caption continuity) before holding the on-air chain."
		}
	}
	return true, fmt.Sprintf("%d interlock observations match active event state", len(interlocks)), ""
}

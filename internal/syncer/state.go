package syncer

import (
	"sync"
	"time"

	"github.com/wyw14/solo-035/internal/model"
)

type Status struct {
	EventID          model.EventID  `json:"event_id"`
	Source           model.SourceID `json:"source"`
	SourceGeneration uint64         `json:"source_generation"`
	StableFrames     int            `json:"stable_frames"`
	Stable           bool           `json:"stable"`
	LastSequence     int64          `json:"last_sequence"`
	LastTimecode     int64          `json:"last_timecode"`
	UpdatedAt        time.Time      `json:"updated_at"`
	Reason           string         `json:"reason"`
}

type State struct {
	mu       sync.RWMutex
	statuses map[model.EventID]Status
	version  uint64
}

// Version 在帧同步状态写入（重置或观察更新）时递增。
func (state *State) Version() uint64 {
	state.mu.RLock()
	defer state.mu.RUnlock()
	return state.version
}

func NewState() *State {
	return &State{statuses: make(map[model.EventID]Status)}
}

func (state *State) Store(status Status) {
	state.mu.Lock()
	defer state.mu.Unlock()
	previous, existed := state.statuses[status.EventID]
	state.statuses[status.EventID] = status
	// 仅在世代、稳定性或原因变化时递增版本，避免连续帧到达不断使诊断失效。
	if !existed || previous.Source != status.Source ||
		previous.SourceGeneration != status.SourceGeneration ||
		previous.Stable != status.Stable ||
		previous.Reason != status.Reason {
		state.version++
	}
}

func (state *State) Get(eventID model.EventID) (Status, bool) {
	state.mu.RLock()
	defer state.mu.RUnlock()
	status, ok := state.statuses[eventID]
	return status, ok
}

func (state *State) List() []Status {
	state.mu.RLock()
	defer state.mu.RUnlock()
	items := make([]Status, 0, len(state.statuses))
	for _, status := range state.statuses {
		items = append(items, status)
	}
	return items
}

func (state *State) Reset(eventID model.EventID, source model.SourceID, generation uint64, now time.Time, reason string) Status {
	status := Status{EventID: eventID, Source: source, SourceGeneration: generation, UpdatedAt: now.UTC(), Reason: reason}
	state.Store(status)
	return status
}

package caption

import (
	"sync"
	"time"

	"github.com/wyw14/solo-035/internal/model"
)

type State struct {
	mu       sync.RWMutex
	statuses map[model.EventID]model.CaptionStatus
	version  uint64
}

// Version 在字幕连续性状态写入（成功或失败）时递增。
func (state *State) Version() uint64 {
	state.mu.RLock()
	defer state.mu.RUnlock()
	return state.version
}

func NewState() *State {
	return &State{statuses: make(map[model.EventID]model.CaptionStatus)}
}

func (state *State) Update(status model.CaptionStatus) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.statuses[status.EventID] = status
	state.version++
}

func (state *State) Success(eventID model.EventID, revision model.RevisionID, now time.Time) model.CaptionStatus {
	status := model.CaptionStatus{EventID: eventID, Revision: revision, Continuous: true, UpdatedAt: now.UTC()}
	state.Update(status)
	return status
}

func (state *State) Failure(eventID model.EventID, revision model.RevisionID, message string, now time.Time) model.CaptionStatus {
	status := model.CaptionStatus{EventID: eventID, Revision: revision, Continuous: false, EncoderErr: message, UpdatedAt: now.UTC()}
	state.Update(status)
	return status
}

func (state *State) Get(eventID model.EventID) (model.CaptionStatus, bool) {
	state.mu.RLock()
	defer state.mu.RUnlock()
	status, ok := state.statuses[eventID]
	return status, ok
}

func (state *State) List() []model.CaptionStatus {
	state.mu.RLock()
	defer state.mu.RUnlock()
	items := make([]model.CaptionStatus, 0, len(state.statuses))
	for _, status := range state.statuses {
		items = append(items, status)
	}
	return items
}

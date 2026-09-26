package journal

import (
	"sort"
	"sync"

	"github.com/wyw14/solo-035/internal/model"
)

type State struct {
	mu        sync.RWMutex
	records   []Record
	revisions map[model.EventID]model.RevisionID
	fences    map[model.EventID]uint64
	version   uint64
}

// Version 在持久化记录应用或日志重置时递增。
func (state *State) Version() uint64 {
	state.mu.RLock()
	defer state.mu.RUnlock()
	return state.version
}

func (state *State) bumpLocked() {
	state.version++
}

func NewState() *State {
	return &State{revisions: make(map[model.EventID]model.RevisionID), fences: make(map[model.EventID]uint64)}
}

func (state *State) Apply(record Record) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.records = append(state.records, record)
	if record.Revision != "" {
		state.revisions[record.EventID] = record.Revision
	}
	if record.Fence > state.fences[record.EventID] {
		state.fences[record.EventID] = record.Fence
	}
	state.bumpLocked()
}

func (state *State) Records() []Record {
	state.mu.RLock()
	defer state.mu.RUnlock()
	items := append([]Record(nil), state.records...)
	sort.SliceStable(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	return items
}

func (state *State) Program(eventID model.EventID) (model.RevisionID, uint64, bool) {
	state.mu.RLock()
	defer state.mu.RUnlock()
	revision, ok := state.revisions[eventID]
	return revision, state.fences[eventID], ok
}

func (state *State) Reset() {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.records = nil
	state.revisions = make(map[model.EventID]model.RevisionID)
	state.fences = make(map[model.EventID]uint64)
	state.bumpLocked()
}

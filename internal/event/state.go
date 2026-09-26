package event

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/wyw14/solo-035/internal/model"
)

type State struct {
	mu      sync.RWMutex
	events  map[model.EventID]model.LiveEvent
	history map[model.EventID][]model.EventPhase
	version uint64
}

// Version 每次事件身份或阶段变更时递增，供诊断引用其状态版本。
func (state *State) Version() uint64 {
	state.mu.RLock()
	defer state.mu.RUnlock()
	return state.version
}

func (state *State) bumpLocked() {
	state.version++
}

func NewState() *State {
	return &State{events: make(map[model.EventID]model.LiveEvent), history: make(map[model.EventID][]model.EventPhase)}
}

func (state *State) Create(name string, now time.Time) (model.LiveEvent, error) {
	event, err := model.NewLiveEvent(name, now)
	if err != nil {
		return model.LiveEvent{}, err
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	state.events[event.ID] = event
	state.history[event.ID] = []model.EventPhase{event.Phase}
	state.bumpLocked()
	return event, nil
}

func (state *State) Get(id model.EventID) (model.LiveEvent, bool) {
	state.mu.RLock()
	defer state.mu.RUnlock()
	event, ok := state.events[id]
	return event, ok
}

func (state *State) GetOrZero(id model.EventID) model.LiveEvent {
	event, _ := state.Get(id)
	return event
}

func (state *State) Transition(id model.EventID, phase model.EventPhase, now time.Time) (model.LiveEvent, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	current, ok := state.events[id]
	if !ok {
		return model.LiveEvent{}, fmt.Errorf("event %s does not exist", id)
	}
	updated, err := current.Transition(phase, now)
	if err != nil {
		return current, err
	}
	state.events[id] = updated
	state.history[id] = append(state.history[id], phase)
	state.bumpLocked()
	return updated, nil
}

func (state *State) Update(event model.LiveEvent) error {
	state.mu.Lock()
	defer state.mu.Unlock()
	if _, ok := state.events[event.ID]; !ok {
		return fmt.Errorf("event %s does not exist", event.ID)
	}
	state.events[event.ID] = event
	state.bumpLocked()
	return nil
}

func (state *State) List() []model.LiveEvent {
	state.mu.RLock()
	defer state.mu.RUnlock()
	items := make([]model.LiveEvent, 0, len(state.events))
	for _, item := range state.events {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	return items
}

func (state *State) History(id model.EventID) []model.EventPhase {
	state.mu.RLock()
	defer state.mu.RUnlock()
	return append([]model.EventPhase(nil), state.history[id]...)
}

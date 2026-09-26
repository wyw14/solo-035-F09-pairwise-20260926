package playout

import (
	"fmt"
	"sync"
	"time"

	"github.com/wyw14/solo-035/internal/model"
)

type Output struct {
	EventID   model.EventID    `json:"event_id"`
	Revision  model.RevisionID `json:"revision"`
	Fence     uint64           `json:"fence"`
	Source    model.SourceID   `json:"source"`
	OnAir     bool             `json:"on_air"`
	Segment   string           `json:"segment"`
	UpdatedAt time.Time        `json:"updated_at"`
}

type State struct {
	mu      sync.RWMutex
	outputs map[model.EventID]Output
	trace   map[model.EventID][]string
	version uint64
}

// Version 在播出输出暂存、发布或停播时递增。
func (state *State) Version() uint64 {
	state.mu.RLock()
	defer state.mu.RUnlock()
	return state.version
}

func (state *State) bumpLocked() {
	state.version++
}

func NewState() *State {
	return &State{outputs: make(map[model.EventID]Output), trace: make(map[model.EventID][]string)}
}

func (state *State) Stage(output Output) error {
	if !output.EventID.Valid() || !output.Revision.Valid() || !output.Source.Valid() {
		return fmt.Errorf("playout output identities are incomplete")
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	current := state.outputs[output.EventID]
	if current.Fence > output.Fence {
		return fmt.Errorf("playout output fence is stale")
	}
	output.OnAir = false
	state.outputs[output.EventID] = output
	state.trace[output.EventID] = append(state.trace[output.EventID], "staged:"+output.Segment)
	state.bumpLocked()
	return nil
}

func (state *State) Publish(eventID model.EventID, revision model.RevisionID, fence uint64, now time.Time) error {
	state.mu.Lock()
	defer state.mu.Unlock()
	output, ok := state.outputs[eventID]
	if !ok || output.Revision != revision || output.Fence != fence {
		return fmt.Errorf("playout publish does not match staged generation")
	}
	output.OnAir = true
	output.UpdatedAt = now.UTC()
	state.outputs[eventID] = output
	state.trace[eventID] = append(state.trace[eventID], "published:"+output.Segment)
	state.bumpLocked()
	return nil
}

func (state *State) Stop(eventID model.EventID, now time.Time) {
	state.mu.Lock()
	defer state.mu.Unlock()
	output := state.outputs[eventID]
	output.OnAir = false
	output.UpdatedAt = now.UTC()
	state.outputs[eventID] = output
	state.trace[eventID] = append(state.trace[eventID], "stopped")
	state.bumpLocked()
}

func (state *State) Output(eventID model.EventID) (Output, bool) {
	state.mu.RLock()
	defer state.mu.RUnlock()
	output, ok := state.outputs[eventID]
	return output, ok
}

func (state *State) Trace(eventID model.EventID) []string {
	state.mu.RLock()
	defer state.mu.RUnlock()
	return append([]string(nil), state.trace[eventID]...)
}

func (state *State) List() []Output {
	state.mu.RLock()
	defer state.mu.RUnlock()
	items := make([]Output, 0, len(state.outputs))
	for _, output := range state.outputs {
		items = append(items, output)
	}
	return items
}

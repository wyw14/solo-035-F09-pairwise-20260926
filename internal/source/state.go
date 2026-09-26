package source

import (
	"sort"
	"sync"
	"time"

	"github.com/wyw14/solo-035/internal/model"
)

type State struct {
	mu      sync.RWMutex
	signals map[model.SourceID]model.SignalStatus
	retired map[model.SourceID]uint64
	frames  map[model.SourceID][]model.Frame
	version uint64
}

// Version 在信号注册、世代切换、锁定确认或退役时递增；帧到达不计入。
func (state *State) Version() uint64 {
	state.mu.RLock()
	defer state.mu.RUnlock()
	return state.version
}

func (state *State) bumpLocked() {
	state.version++
}

func NewState() *State {
	return &State{
		signals: make(map[model.SourceID]model.SignalStatus),
		retired: make(map[model.SourceID]uint64),
		frames:  make(map[model.SourceID][]model.Frame),
	}
}

func (state *State) Register(id model.SourceID, now time.Time) model.SignalStatus {
	state.mu.Lock()
	defer state.mu.Unlock()
	status := model.SignalStatus{Source: id, Generation: 1, UpdatedAt: now.UTC()}
	state.signals[id] = status
	state.bumpLocked()
	return status
}

func (state *State) BeginSwitch(id model.SourceID, now time.Time) model.SignalStatus {
	state.mu.Lock()
	defer state.mu.Unlock()
	status := state.signals[id]
	status.Source = id
	status.Generation++
	status.CurrentCommand = model.NewCommandID()
	status.VideoLocked = false
	status.AudioLocked = false
	status.UpdatedAt = now.UTC()
	state.signals[id] = status
	state.frames[id] = nil
	state.bumpLocked()
	return status
}

func (state *State) AcceptLock(id model.SourceID, command model.CommandID, video bool, audio bool, now time.Time) bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	status, ok := state.signals[id]
	if !ok || status.CurrentCommand != command {
		return false
	}
	status.VideoLocked = video
	status.AudioLocked = audio
	status.UpdatedAt = now.UTC()
	state.signals[id] = status
	state.bumpLocked()
	return true
}

func (state *State) Status(id model.SourceID) (model.SignalStatus, bool) {
	state.mu.RLock()
	defer state.mu.RUnlock()
	status, ok := state.signals[id]
	return status, ok
}

func (state *State) AddFrame(frame model.Frame) bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	status, ok := state.signals[frame.Source]
	if !ok || frame.SourceGeneration != status.Generation {
		return false
	}
	state.frames[frame.Source] = append(state.frames[frame.Source], frame)
	status.LastFrame = frame.Sequence
	status.LastTimecode = frame.Timecode
	status.UpdatedAt = frame.CapturedAt.UTC()
	state.signals[frame.Source] = status
	return true
}

func (state *State) Retire(id model.SourceID) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.retired[id] = state.signals[id].Generation
	state.frames[id] = nil
	state.bumpLocked()
}

func (state *State) Frames(id model.SourceID) []model.Frame {
	state.mu.RLock()
	defer state.mu.RUnlock()
	return append([]model.Frame(nil), state.frames[id]...)
}

func (state *State) List() []model.SignalStatus {
	state.mu.RLock()
	defer state.mu.RUnlock()
	items := make([]model.SignalStatus, 0, len(state.signals))
	for _, item := range state.signals {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Source < items[j].Source })
	return items
}

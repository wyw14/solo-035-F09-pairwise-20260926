package event

import (
	"fmt"
	"time"

	"github.com/wyw14/solo-035/internal/model"
)

type RecoveryRecord struct {
	Event   model.LiveEvent    `json:"event"`
	History []model.EventPhase `json:"history"`
}

func (state *State) ExportRecovery() []RecoveryRecord {
	events := state.List()
	records := make([]RecoveryRecord, 0, len(events))
	for _, item := range events {
		records = append(records, RecoveryRecord{Event: item, History: state.History(item.ID)})
	}
	return records
}

func (state *State) Restore(records []RecoveryRecord, now time.Time) error {
	state.mu.Lock()
	defer state.mu.Unlock()
	for _, record := range records {
		if !record.Event.ID.Valid() || !record.Event.Revision.Valid() {
			return fmt.Errorf("invalid recovered event identity")
		}
		if record.Event.Phase == "" || len(record.History) == 0 {
			return fmt.Errorf("event %s has incomplete recovery state", record.Event.ID)
		}
		record.Event.UpdatedAt = now.UTC()
		state.events[record.Event.ID] = record.Event
		state.history[record.Event.ID] = append([]model.EventPhase(nil), record.History...)
		state.bumpLocked()
	}
	return nil
}

func (state *State) AdvanceProgram(id model.EventID, now time.Time) (model.LiveEvent, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	event, ok := state.events[id]
	if !ok {
		return model.LiveEvent{}, fmt.Errorf("event %s not found", id)
	}
	event = event.NewProgram(now)
	state.events[id] = event
	state.bumpLocked()
	return event, nil
}

func (state *State) AdvanceSource(id model.EventID, now time.Time) (model.LiveEvent, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	event, ok := state.events[id]
	if !ok {
		return model.LiveEvent{}, fmt.Errorf("event %s not found", id)
	}
	event = event.NewSource(now)
	state.events[id] = event
	state.bumpLocked()
	return event, nil
}

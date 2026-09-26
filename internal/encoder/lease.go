package encoder

import (
	"fmt"
	"time"

	"github.com/wyw14/solo-035/internal/model"
)

func (state *State) Acquire(owner model.EventID, purpose string, profile string, now time.Time) (model.EncoderLease, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	for id, resource := range state.resources {
		if !resource.Healthy || resource.Lease.Owner != "" {
			continue
		}
		lease := model.EncoderLease{
			ID:         model.NewLeaseID(),
			EncoderID:  id,
			Owner:      owner,
			Purpose:    purpose,
			Fence:      state.nextFence(id),
			AcquiredAt: now.UTC(),
		}
		resource.Profile = profile
		resource.Lease = lease
		state.resources[id] = resource
		state.bumpLocked()
		return lease, nil
	}
	return model.EncoderLease{}, fmt.Errorf("no healthy encoder is available for %s", purpose)
}

func (state *State) Release(lease model.EncoderLease, now time.Time) error {
	state.mu.Lock()
	defer state.mu.Unlock()
	resource, ok := state.resources[lease.EncoderID]
	if !ok {
		return fmt.Errorf("encoder %s does not exist", lease.EncoderID)
	}
	if resource.Lease.ID != lease.ID || resource.Lease.Fence != lease.Fence {
		return fmt.Errorf("encoder lease is stale")
	}
	released := now.UTC()
	resource.Lease.ReleasedAt = &released
	resource.Lease.Owner = ""
	resource.Profile = ""
	state.resources[lease.EncoderID] = resource
	state.bumpLocked()
	return nil
}

func (state *State) Validate(lease model.EncoderLease) bool {
	state.mu.RLock()
	defer state.mu.RUnlock()
	resource, ok := state.resources[lease.EncoderID]
	return ok && resource.Lease.ID == lease.ID && resource.Lease.Owner == lease.Owner && resource.Lease.Fence == lease.Fence
}

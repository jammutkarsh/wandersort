package statusmanager

import (
	"sync"
)

type StatusManager struct {
	mu            sync.RWMutex
	subscribers   map[chan WorkflowStatus]struct{}
	currentStatus WorkflowStatus
}

// The NewStatusManager function creates a new StatusManager instance with an empty map of subscribers.
func NewStatusManager() *StatusManager {
	return &StatusManager{
		subscribers: make(map[chan WorkflowStatus]struct{}),
	}
}

// Subscribe allows a caller to receive status updates. It returns a channel that will receive WorkflowStatus updates.
// The channel is buffered to prevent blocking the broadcaster, and the current status is sent immediately upon subscription.
func (sm *StatusManager) Subscribe() chan WorkflowStatus {
	ch := make(chan WorkflowStatus, 100)
	sm.mu.Lock()
	sm.subscribers[ch] = struct{}{}
	current := sm.currentStatus
	sm.mu.Unlock()

	// Send current status immediately
	ch <- current
	return ch
}

func (sm *StatusManager) Unsubscribe(ch chan WorkflowStatus) {
	sm.mu.Lock()
	delete(sm.subscribers, ch)
	close(ch)
	sm.mu.Unlock()
}

func (sm *StatusManager) Broadcast(status WorkflowStatus) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.currentStatus = status

	for ch := range sm.subscribers {
		select {
		case ch <- status:
		default:
			// Drop if subscriber is slow
		}
	}
}

// GetCurrent returns the latest broadcasted status.
func (sm *StatusManager) GetCurrent() WorkflowStatus {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.currentStatus
}

package status

import (
	"sync"

	"github.com/google/uuid"
)

type PipelineStatus struct {
	SessionID       uuid.UUID `json:"sessionId"`
	Status          string    `json:"status"` // SCAN, HASH, SCORE, FAILED, CANCELLED
	FilesDiscovered int64     `json:"filesDiscovered"`
	FilesSkipped    int64     `json:"filesSkipped"`
	FilesNew        int64     `json:"filesNew"`
	FilesHashed     int64     `json:"filesHashed"`
	Errors          int64     `json:"errors"`
	LastError       string    `json:"lastError,omitempty"`
}

type StatusManager struct {
	mu            sync.RWMutex
	subscribers   map[chan PipelineStatus]struct{}
	currentStatus PipelineStatus
}

func NewStatusManager() *StatusManager {
	return &StatusManager{
		subscribers: make(map[chan PipelineStatus]struct{}),
	}
}

func (sm *StatusManager) Subscribe() chan PipelineStatus {
	ch := make(chan PipelineStatus, 100)
	sm.mu.Lock()
	sm.subscribers[ch] = struct{}{}
	current := sm.currentStatus
	sm.mu.Unlock()

	// Send current status immediately
	ch <- current
	return ch
}

func (sm *StatusManager) Unsubscribe(ch chan PipelineStatus) {
	sm.mu.Lock()
	delete(sm.subscribers, ch)
	close(ch)
	sm.mu.Unlock()
}

func (sm *StatusManager) Broadcast(status PipelineStatus) {
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
func (sm *StatusManager) GetCurrent() PipelineStatus {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.currentStatus
}

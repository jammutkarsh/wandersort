package vfs

import corevfs "github.com/jammutkarsh/wandersort/pkg/core/vfs"

// TreeResponse is the proposed directory tree for a session (folder names only).
type TreeResponse struct {
	SessionID string         `json:"sessionId"`
	Tree      []corevfs.Node `json:"tree"`
}

// ConfirmRequest carries the (possibly edited) tree back for reconcile.
type ConfirmRequest struct {
	Tree []corevfs.Node `json:"tree" binding:"required"`
}

// ConfirmResponse acknowledges an applied review.
type ConfirmResponse struct {
	SessionID string `json:"sessionId"`
	Status    string `json:"status"`
	Message   string `json:"message"`
}

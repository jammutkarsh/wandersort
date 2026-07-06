// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"errors"
	"net/http"
)

type APIError struct {
	Status  int            `json:"-"`
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func (e APIError) Error() string {
	return e.Message
}

func newAPIError(status int, code, message string, details map[string]any) APIError {
	return APIError{
		Status:  status,
		Code:    code,
		Message: message,
		Details: details,
	}
}

func BadRequest(code, message string, details map[string]any) APIError {
	return newAPIError(http.StatusBadRequest, code, message, details)
}

func NotFound(code, message string, details map[string]any) APIError {
	return newAPIError(http.StatusNotFound, code, message, details)
}

func Conflict(code, message string, details map[string]any) APIError {
	return newAPIError(http.StatusConflict, code, message, details)
}

func InternalError(code, message string, details map[string]any) APIError {
	return newAPIError(http.StatusInternalServerError, code, message, details)
}

func AsAPIError(err error) APIError {
	if err == nil {
		return InternalError("INTERNAL_ERROR", "unexpected error", nil)
	}
	var apiErr APIError
	if errors.As(err, &apiErr) {
		return apiErr
	}
	return InternalError("INTERNAL_ERROR", err.Error(), nil)
}

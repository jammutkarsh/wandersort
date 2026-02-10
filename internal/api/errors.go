package api

import "net/http"

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

func badRequest(code, message string, details map[string]any) APIError {
	return newAPIError(http.StatusBadRequest, code, message, details)
}

func BadRequest(code, message string, details map[string]any) APIError {
	return badRequest(code, message, details)
}

func notFound(code, message string, details map[string]any) APIError {
	return newAPIError(http.StatusNotFound, code, message, details)
}

func NotFound(code, message string, details map[string]any) APIError {
	return notFound(code, message, details)
}

func conflict(code, message string, details map[string]any) APIError {
	return newAPIError(http.StatusConflict, code, message, details)
}

func Conflict(code, message string, details map[string]any) APIError {
	return conflict(code, message, details)
}

func internalError(code, message string, details map[string]any) APIError {
	return newAPIError(http.StatusInternalServerError, code, message, details)
}

func InternalError(code, message string, details map[string]any) APIError {
	return internalError(code, message, details)
}

func notImplemented(message string) APIError {
	return newAPIError(http.StatusNotImplemented, "NOT_IMPLEMENTED", message, nil)
}

func NotImplemented(message string) APIError {
	return notImplemented(message)
}

func asAPIError(err error) APIError {
	if err == nil {
		return internalError("INTERNAL_ERROR", "unexpected error", nil)
	}
	if apiErr, ok := err.(APIError); ok {
		return apiErr
	}
	return internalError("INTERNAL_ERROR", err.Error(), nil)
}

func AsAPIError(err error) APIError {
	return asAPIError(err)
}

package service

import "github.com/loki/gpu-operator-runtime/pkg/contract"

// ValidationError reports that the caller provided an invalid request.
type ValidationError = contract.ValidationError

// ConflictError reports that the request collides with existing state.
type ConflictError struct {
	Message string
}

// Error implements the error interface.
func (e *ConflictError) Error() string {
	return e.Message
}

// CapacityError reports that no matching ready stock is available.
type CapacityError struct {
	Message string
}

// Error implements the error interface.
func (e *CapacityError) Error() string {
	return e.Message
}

// NotFoundError reports that the requested runtime object does not exist.
type NotFoundError struct {
	Message string
}

// Error implements the error interface.
func (e *NotFoundError) Error() string {
	return e.Message
}

// UnavailableError reports that a required backend client is not configured.
type UnavailableError struct {
	Message string
}

// Error implements the error interface.
func (e *UnavailableError) Error() string {
	return e.Message
}

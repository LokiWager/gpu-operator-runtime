package contract

// ValidationError reports that the caller provided an invalid request contract.
type ValidationError struct {
	Message string
}

// Error implements the error interface.
func (e *ValidationError) Error() string {
	return e.Message
}

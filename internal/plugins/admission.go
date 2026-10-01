package plugins

import "fmt"

// AdmissionError preserves a machine-readable reason for startup diagnostics.
// Restart still unwraps the same underlying error and affects only its plugin.
type AdmissionError struct {
	Reason string
	Err    error
}

func (e *AdmissionError) Error() string { return e.Err.Error() }
func (e *AdmissionError) Unwrap() error { return e.Err }

func admissionFailure(reason, format string, args ...any) error {
	return &AdmissionError{Reason: reason, Err: fmt.Errorf(format, args...)}
}

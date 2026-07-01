// Package cerrs defines constant errors
package cerrs

// Error defines a constant error
type Error string

// Error implements the Errors interface
func (e Error) Error() string { return string(e) }

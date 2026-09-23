package svn

import "fmt"

// Error is the representation of a "failure" command response, as sent by
// a server to report that a command could not be completed. It also
// implements the error interface.
type Error struct {
	// AprErr is the APR error code the server reported (an svn-specific
	// numeric error code, not a POSIX errno).
	AprErr int
	// Message is the human-readable error message.
	Message string
	// File is the server-side source file that raised the error, if the
	// server included it. It is empty otherwise.
	File string
	// Line is the source line within File, meaningful only when File is
	// non-empty.
	Line int
}

// Error returns a human-readable representation of e, combining its APR
// error code, message, and source location (when the server provided one).
func (e Error) Error() string {
	msg := ""
	if e.AprErr != 0 {
		msg = fmt.Sprintf("%d ", e.AprErr)
	}
	msg += e.Message
	if e.File != "" {
		msg += fmt.Sprintf(" (%s line %d)", e.File, e.Line)
	}
	return msg
}

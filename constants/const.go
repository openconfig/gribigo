// package constants defines constants that are shared amongst multiple gRIBIgo packages.
package constants

// OpType indicates the type of operation that was performed in contexts where it
// is not available, such as callbacks to user-provided functions.
type OpType int64

const (
	_ OpType = iota
	// ADD indicates that the operation called was an Add.
	ADD
	// DELETE indicates that the operation called was a Delete.
	DELETE
	// MODIFY indicates that the operation called was a Modify.
	MODIFY
)

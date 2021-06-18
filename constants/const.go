// package constants defines constants that are shared amongst multiple gRIBIgo packages.
package constants

import (
	spb "github.com/openconfig/gribi/v1/proto/service"
)

// OpType indicates the type of operation that was performed in contexts where it
// is not available, such as callbacks to user-provided functions.
type OpType int64

const (
	_ OpType = iota
	// ADD indicates that the operation called was an Add.
	ADD
	// DELETE indicates that the operation called was a Delete.
	DELETE
	// REPLACE indicates that the operation called was a Modify.
	REPLACE
)

// aftopMap maps from the gRIBI proto AFT operation to an OpType.
var aftopMap = map[spb.AFTOperation_Operation]OpType{
	spb.AFTOperation_ADD:     ADD,
	spb.AFTOperation_DELETE:  DELETE,
	spb.AFTOperation_REPLACE: REPLACE,
}

// OpFromAFTOp returns an OpType from the AFT operation in the gRIBI
// protobuf.
func OpFromAFTOp(o spb.AFTOperation_Operation) OpType {
	return aftopMap[o]
}

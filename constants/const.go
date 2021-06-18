// Copyright 2021 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

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

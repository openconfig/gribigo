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
	// Add indicates that the operation called was an Add.
	Add
	// Delete indicates that the operation called was a Delete.
	Delete
	// Replace indicates that the operation called was a Modify.
	Replace
)

// String returns a string name for the OpType.
func (o OpType) String() string {
	names := map[OpType]string{
		Add:     "Add",
		Delete:  "Delete",
		Replace: "Replace",
	}
	return names[o]
}

// aftopMap maps from the gRIBI proto AFT operation to an OpType.
var aftopMap = map[spb.AFTOperation_Operation]OpType{
	spb.AFTOperation_ADD:     Add,
	spb.AFTOperation_DELETE:  Delete,
	spb.AFTOperation_REPLACE: Replace,
}

// OpFromAFTOp returns an OpType from the AFT operation in the gRIBI
// protobuf.
func OpFromAFTOp(o spb.AFTOperation_Operation) OpType {
	return aftopMap[o]
}

// AFT is an enumerated type describing the AFTs available within gRIBI.
type AFT int64

const (
	_ AFT = iota
	// All specifies all AFTs.
	All
	// IPv4 specifies the IPv4 AFT.
	IPv4
	// NextHop specifies the next-hop AFT.
	NextHop
	// NextHopGroup specifies the next-hop-group AFT.
	NextHopGroup
	// MPLS specifies the label-entry/MPLS AFT.
	MPLS
	//  IPv6 speciifes the IPv6 AFT.
	IPv6
)

func (a AFT) String() string {
	return map[AFT]string{
		All:          "ALL",
		IPv4:         "IPv4",
		NextHop:      "NextHop",
		NextHopGroup: "NextHopGroup",
		MPLS:         "MPLS",
		IPv6:         "IPv6",
	}[a]
}

// aftMap maps between an AFT enumerated type and the specified type in the
// gRIBI protobuf.
var aftMap = map[AFT]spb.AFTType{
	All:          spb.AFTType_ALL,
	IPv4:         spb.AFTType_IPV4,
	IPv6:         spb.AFTType_IPV6,
	NextHop:      spb.AFTType_NEXTHOP,
	NextHopGroup: spb.AFTType_NEXTHOP_GROUP,
}

// AFTTypeFromAFT returns the gRIBI AFTType from the enumerated AFT type.
func AFTTypeFromAFT(a AFT) spb.AFTType {
	return aftMap[a]
}

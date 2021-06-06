// Package chk implements checks against the gRIBI client return values, it can be
// used to determine whether there are expected results within a specific set of return
// values.
package chk

import (
	"github.com/openconfig/gribigo/client"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// HasElectionID returns true if the result queue supplied contains an update with the election ID
// with the value of the uint128{low,high} value specified.
func HasElectionID(res []*client.OpResult, low, high uint64) bool {
	for _, r := range res {
		if v := r.CurrentServerElectionID; v != nil {
			if v.High == high && v.Low == low {
				return true
			}
		}
	}
	return false
}

// HasSuccessfulSessionParams checks whether the res results array contains a session parameters
// result that is successful.
func HasSuccessfulSessionParams(res []*client.OpResult) bool {
	for _, r := range res {
		if v := r.SessionParameters; v != nil {
			return true
		}
	}
	return false
}

// HasRecvClientErrorWithStatus checks whether the supplied ClientErr ce containa  status with
// the code and details set to the values supplied in want.
func HasRecvClientErrorWithStatus(ce *client.ClientErr, want *status.Status) bool {
	for _, e := range ce.Recv {
		s, ok := status.FromError(e)
		if !ok {
			continue
		}
		ns := s.Proto()
		ns.Message = "" // blank out message so that we don't compare it.
		if proto.Equal(ns, want.Proto()) {
			return true
		}
	}
	return false
}

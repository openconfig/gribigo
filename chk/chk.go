// Package chk implements checks against the gRIBI client return values, it can be
// used to determine whether there are expected results within a specific set of return
// values.
package chk

import "github.com/openconfig/gribigo/client"

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

func HasSuccessfulSessionParams(res []*client.OpResult) bool {
	for _, r := range res {
		if v := r.SessionParameters; v != nil {
			return true
		}
	}
	return false
}

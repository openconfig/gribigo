package chk

import (
	"testing"

	"github.com/openconfig/gribigo/client"

	spb "github.com/openconfig/gribi/v1/proto/service"
)

func TestHasElectionID(t *testing.T) {
	tests := []struct {
		desc     string
		inResult []*client.OpResult
		inLow    uint64
		inHigh   uint64
		want     bool
	}{{
		desc: "is present",
		inResult: []*client.OpResult{{
			CurrentServerElectionID: &spb.Uint128{
				High: 0,
				Low:  42,
			},
		}},
		inLow:  42,
		inHigh: 0,
		want:   true,
	}, {
		desc:     "is not present",
		inResult: []*client.OpResult{},
	}}

	for _, tt := range tests {
		if got, want := HasElectionID(tt.inResult, tt.inLow, tt.inHigh), tt.want; got != want {
			t.Errorf("%s: HasElectionDI(%s, %d, %d): did not get expected result, got: %v, want: %v", tt.desc, tt.inResult, tt.inLow, tt.inHigh, got, want)
		}
	}
}

func TestHasSuccessfulSessionParams(t *testing.T) {
	tests := []struct {
		desc     string
		inResult []*client.OpResult
		want     bool
	}{{
		desc: "is present",
		inResult: []*client.OpResult{{
			SessionParameters: &spb.SessionParametersResult{},
		}},
		want: true,
	}, {
		desc:     "is not present",
		inResult: []*client.OpResult{},
	}}

	for _, tt := range tests {
		if got, want := HasSuccessfulSessionParams(tt.inResult), tt.want; got != want {
			t.Errorf("%s: HasSuccessfulSessionParams(%s): did not get expected result, got: %v, want: %v", tt.desc, tt.inResult, got, want)
		}
	}
}

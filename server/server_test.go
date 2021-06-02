package server

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestNewClient(t *testing.T) {
	tests := []struct {
		desc        string
		inIDs       []string
		wantClients map[string]*clientState
		// wantErrorCode is a map of the operation sequence (zero-indexed)
		// to an error code that is expected.
		wantClientErrCode map[int]codes.Code
	}{{
		desc:  "successfully create single client",
		inIDs: []string{"c1"},
		wantClients: map[string]*clientState{
			"c1": {params: &clientParams{}},
		},
	}, {
		desc:  "fail to create duplicate client",
		inIDs: []string{"c1", "c1"},
		wantClients: map[string]*clientState{
			"c1": {params: &clientParams{}},
		},
		wantClientErrCode: map[int]codes.Code{
			1: codes.Internal,
		},
	}, {
		desc:  "create multiple clients",
		inIDs: []string{"c1", "c2"},
		wantClients: map[string]*clientState{
			"c1": {params: &clientParams{}},
			"c2": {params: &clientParams{}},
		},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			s := New()
			for i, c := range tt.inIDs {
				wantErr := tt.wantClientErrCode[i]
				gotErr := s.newClient(c)
				switch {
				case gotErr == nil && wantErr != codes.OK:
					t.Fatalf("did not get expected error for index %d, got nil", i)
				case !cmp.Equal(wantErr, status.Code(gotErr)):
					t.Fatalf("did not get expected error code, got: %v (%v), want: %v", status.Code(gotErr), gotErr, wantErr)
				}
			}
			if diff := cmp.Diff(tt.wantClients, s.cs, cmp.AllowUnexported(clientState{})); diff != "" {
				t.Fatalf("did not get expected clients, diff(-want,+got):\n%s", diff)
			}
		})
	}
}

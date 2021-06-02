package server

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	spb "github.com/openconfig/gribi/v1/proto/service"
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

func TestUpdateParams(t *testing.T) {
	tests := []struct {
		desc        string
		inServer    *Server
		inID        string
		inParams    *spb.SessionParameters
		wantCode    codes.Code
		wantDetails *spb.ModifyRPCErrorDetails
		wantState   *clientParams
	}{{
		desc:     "uninitialised client",
		inServer: &Server{},
		inID:     "c1",
		inParams: &spb.SessionParameters{},
		wantCode: codes.Internal,
	}, {
		desc: "client with existing state",
		inServer: &Server{
			cs: map[string]*clientState{
				"c1": {
					params: &clientParams{},
				},
			},
		},
		inID:     "c1",
		inParams: &spb.SessionParameters{},
		wantCode: codes.FailedPrecondition,
		wantDetails: &spb.ModifyRPCErrorDetails{
			Reason: spb.ModifyRPCErrorDetails_MODIFY_NOT_ALLOWED,
		},
	}, {
		desc: "new client, with all fields as default",
		inServer: &Server{
			cs: map[string]*clientState{"c1": {}},
		},
		inID:     "c1",
		inParams: &spb.SessionParameters{},
		wantState: &clientParams{
			ExpectElecID: false,
			Persist:      false,
			FIBAck:       false,
		},
	}, {
		desc: "new client, with all fields non-default",
		inServer: &Server{
			cs: map[string]*clientState{"c1": {}},
		},
		inID: "c1",
		inParams: &spb.SessionParameters{
			Persistence: spb.SessionParameters_PRESERVE,
			Redundancy:  spb.SessionParameters_SINGLE_PRIMARY,
			AckType:     spb.SessionParameters_RIB_AND_FIB_ACK,
		},
		wantState: &clientParams{
			ExpectElecID: true,
			Persist:      true,
			FIBAck:       true,
		},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			if err := tt.inServer.updateParams(tt.inID, tt.inParams); err != nil {
				p, ok := status.FromError(err)
				if !ok {
					t.Fatalf("did not get expected error, got: %v but was not a status.Status", err)
				}

				if got, want := p.Code(), tt.wantCode; got != want {
					t.Errorf("did not get expected error code, got: %v, want: %v (error: %v)", got, want, err)
				}

				if got, want := p.Proto().Details, tt.wantDetails; tt.wantDetails != nil {
					if l := len(got); l != 1 {
						t.Fatalf("did not get expected error details, got %d messages, expected 1", l)
					}

					gotErrDet := &spb.ModifyRPCErrorDetails{}
					if err := got[0].UnmarshalTo(gotErrDet); err != nil {
						t.Fatalf("did not get expected error type, got: %T, unmarshal error: %v", got[0], err)
					}

					if diff := cmp.Diff(gotErrDet, want, protocmp.Transform()); diff != "" {
						t.Fatalf("did not got expected details, %s", diff)
					}
				}
				return
			}

			if diff := cmp.Diff(tt.inServer.cs[tt.inID].params, tt.wantState); diff != "" {
				t.Fatalf("did not get expected state, diff(-got,+want):\n%s", diff)
			}
		})
	}
}

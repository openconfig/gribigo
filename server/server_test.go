package server

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
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
			"c1": {},
		},
	}, {
		desc:  "fail to create duplicate client",
		inIDs: []string{"c1", "c1"},
		wantClients: map[string]*clientState{
			"c1": {},
		},
		wantClientErrCode: map[int]codes.Code{
			1: codes.Internal,
		},
	}, {
		desc:  "create multiple clients",
		inIDs: []string{"c1", "c2"},
		wantClients: map[string]*clientState{
			"c1": {},
			"c2": {},
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
			if diff := cmp.Diff(tt.wantClients, s.cs,
				cmp.AllowUnexported(clientState{}),
				cmpopts.EquateEmpty()); diff != "" {
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

func TestCheckParams(t *testing.T) {
	tests := []struct {
		desc         string
		inParams     *spb.SessionParameters
		inGotMsg     bool
		wantResponse *spb.ModifyResponse
		wantErrCode  codes.Code
	}{{
		desc:        "already received message",
		inGotMsg:    true,
		wantErrCode: codes.FailedPrecondition,
	}, {
		desc:     "received OK message",
		inParams: &spb.SessionParameters{},
		wantResponse: &spb.ModifyResponse{
			SessionParamsResult: &spb.SessionParametersResult{
				Status: spb.SessionParametersResult_OK,
			},
		},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			s := New()
			got, err := s.checkParams(tt.inParams, tt.inGotMsg)
			if err != nil {
				st, ok := status.FromError(err)
				if !ok {
					t.Fatalf("got error that was not a status.Status, got: %v", err)
				}
				if st.Code() != tt.wantErrCode {
					t.Fatalf("did not get expected code, got: %s, want: %s", st.Code(), tt.wantErrCode)
				}
				return
			}

			if diff := cmp.Diff(got, tt.wantResponse, protocmp.Transform()); diff != "" {
				t.Fatalf("did not get expected error, diff(-got,+want):\n%s", diff)
			}
		})
	}
}

func TestIsNewMaster(t *testing.T) {
	tests := []struct {
		desc       string
		inCand     *spb.Uint128
		inExist    *spb.Uint128
		wantMaster bool
		wantEqual  bool
		wantErr    bool
	}{{
		desc:       "new master - low only",
		inCand:     &spb.Uint128{Low: 2},
		inExist:    &spb.Uint128{Low: 1},
		wantMaster: true,
	}, {
		desc:       "new master - high only",
		inCand:     &spb.Uint128{High: 2},
		inExist:    &spb.Uint128{High: 1},
		wantMaster: true,
	}, {
		desc:       "new master - high and low",
		inCand:     &spb.Uint128{High: 4, Low: 3},
		inExist:    &spb.Uint128{High: 4, Low: 2},
		wantMaster: true,
	}, {
		desc:      "equal",
		inCand:    &spb.Uint128{High: 42, Low: 42},
		inExist:   &spb.Uint128{High: 42, Low: 42},
		wantEqual: true,
	}, {
		desc:       "nil input",
		inCand:     &spb.Uint128{High: 4242, Low: 4242},
		wantMaster: true,
	}, {
		desc:    "not master",
		inCand:  &spb.Uint128{High: 1, Low: 1},
		inExist: &spb.Uint128{High: 44, Low: 42},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			gotMaster, gotEqual, err := isNewMaster(tt.inCand, tt.inExist)

			if got, want := (err != nil), tt.wantErr; got != want {
				t.Fatalf("did not get expected error, gotErr: %v, want: %v", err, want)
			}

			if got, want := gotMaster, tt.wantMaster; got != want {
				t.Errorf("did not get expected master result, got: %v, want: %v", got, want)
			}

			if got, want := gotEqual, tt.wantEqual; got != want {
				t.Errorf("did not get expected equal result, got: %v, want: %v", got, want)
			}
		})
	}
}

func TestRunElection(t *testing.T) {
	tests := []struct {
		desc             string
		inServer         *Server
		inID             string
		inElecID         *spb.Uint128
		wantResponse     *spb.ModifyResponse
		wantServerElecID *spb.Uint128
		wantServerMaster string
		wantErrCode      codes.Code
	}{{
		desc:     "becomes master - no election ID",
		inServer: &Server{},
		inID:     "c1",
		inElecID: &spb.Uint128{High: 0, Low: 1},
		wantResponse: &spb.ModifyResponse{
			ElectionId: &spb.Uint128{High: 0, Low: 1},
		},
		wantServerElecID: &spb.Uint128{High: 0, Low: 1},
		wantServerMaster: "c1",
	}, {
		desc: "becomes master - election ID present",
		inServer: &Server{
			curElecID: &spb.Uint128{
				High: 0,
				Low:  1,
			},
		},
		inID:     "c1",
		inElecID: &spb.Uint128{High: 0, Low: 2},
		wantResponse: &spb.ModifyResponse{
			ElectionId: &spb.Uint128{High: 0, Low: 2},
		},
		wantServerElecID: &spb.Uint128{High: 0, Low: 2},
		wantServerMaster: "c1",
	}, {
		desc: "does not become master",
		inServer: &Server{
			curElecID: &spb.Uint128{
				High: 0,
				Low:  4000,
			},
			curMaster: "existing",
		},
		inID:     "c1",
		inElecID: &spb.Uint128{High: 0, Low: 2},
		wantResponse: &spb.ModifyResponse{
			ElectionId: &spb.Uint128{High: 0, Low: 4000},
		},
		wantServerElecID: &spb.Uint128{High: 0, Low: 4000},
		wantServerMaster: "existing",
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			s := tt.inServer
			got, err := s.runElection(tt.inID, tt.inElecID)
			if err != nil {
				st, ok := status.FromError(err)
				if !ok {
					t.Fatalf("returned error that was not a status.Status, got:%v", err)
				}
				if got, want := st.Code(), tt.wantErrCode; got != want {
					t.Fatalf("did not get expected error code, got: %s, want: %s", got, want)
				}
				return
			}

			if diff := cmp.Diff(got, tt.wantResponse, protocmp.Transform()); diff != "" {
				t.Errorf("did not get expected response, diff(-got,+want):\n%s", diff)
			}

			if got, want := s.curElecID, tt.wantServerElecID; !cmp.Equal(got, want, protocmp.Transform()) {
				t.Errorf("did not get expected server ID, got: %s, want: %s", got, want)
			}

			if got, want := s.curMaster, tt.wantServerMaster; !cmp.Equal(got, want) {
				t.Errorf("did not get expected master ID, got: %s, want: %s", got, want)
			}
		})
	}
}

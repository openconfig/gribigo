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

package server

import (
	"context"
	"fmt"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"

	aftpb "github.com/openconfig/gribi/v1/proto/gribi_aft"
	spb "github.com/openconfig/gribi/v1/proto/service"
	"github.com/openconfig/gribigo/rib"
	wpb "github.com/openconfig/ygot/proto/ywrapper"
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
			s, err := New(nil)
			if err != nil {
				t.Fatalf("cannot create server, got error: %v", err)
			}

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
				cmp.AllowUnexported(clientState{})); diff != "" {
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
		desc: "client with previously set state",
		inServer: &Server{
			cs: map[string]*clientState{
				"c1": {
					params:    &clientParams{},
					setParams: true,
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
			cs: map[string]*clientState{
				"c1": {
					params: &clientParams{},
				},
			},
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
			cs: map[string]*clientState{
				"c1": {
					params: &clientParams{},
				},
			},
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
	}, {
		desc: "new client, all fields default",
		inServer: &Server{
			cs: map[string]*clientState{
				"c1": {
					params: &clientParams{},
				},
			},
		},
		inID: "c1",
		inParams: &spb.SessionParameters{
			Persistence: spb.SessionParameters_DELETE,
			Redundancy:  spb.SessionParameters_ALL_PRIMARY,
			AckType:     spb.SessionParameters_RIB_ACK,
		},
		wantState: &clientParams{
			ExpectElecID: false,
			Persist:      false,
			FIBAck:       false,
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

func TestCheckClientsConsistent(t *testing.T) {
	tests := []struct {
		desc     string
		inServer *Server
		inID     string
		inParams *clientParams
		want     bool
		wantErr  bool
	}{{
		desc: "inconsistent clients",
		inServer: &Server{
			cs: map[string]*clientState{
				"c1": {
					params: &clientParams{
						ExpectElecID: true,
					},
				},
			},
		},
		inParams: &clientParams{},
		inID:     "c2",
		want:     false,
	}, {
		desc: "consistent clients",
		inServer: &Server{
			cs: map[string]*clientState{
				"c1": {
					params: &clientParams{
						ExpectElecID: true,
					},
				},
			},
		},
		inParams: &clientParams{
			ExpectElecID: true,
		},
		inID: "c2",
		want: true,
	}, {
		desc: "nil parameters for existing client",
		inServer: &Server{
			cs: map[string]*clientState{
				"c1": {},
			},
		},
		inParams: &clientParams{},
		inID:     "c2",
		wantErr:  true,
	}, {
		desc:     "nil parameters for new client",
		inServer: &Server{},
		inID:     "error",
		wantErr:  true,
	}, {
		desc: "skip this client",
		inServer: &Server{
			cs: map[string]*clientState{
				"c1": {},
			},
		},
		inParams: &clientParams{},
		inID:     "c1",
		want:     true,
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got, err := tt.inServer.checkClientsConsistent(tt.inID, tt.inParams)
			if (err != nil) != tt.wantErr {
				t.Fatalf("did not get expected error, got err: %v, wantErr? %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("did not get expected consistency value, got: %v, want: %v", got, tt.want)
			}
		})
	}
}
func TestCheckParams(t *testing.T) {
	tests := []struct {
		desc           string
		inID           string
		inServer       *Server
		inParams       *spb.SessionParameters
		inGotMsg       bool
		wantResponse   *spb.ModifyResponse
		wantErrCode    codes.Code
		wantErrDetails *spb.ModifyRPCErrorDetails
	}{{
		desc: "already received message",
		inServer: &Server{
			cs: map[string]*clientState{
				"c1": {params: &clientParams{}},
			},
		},
		inID:        "c1",
		inGotMsg:    true,
		inParams:    &spb.SessionParameters{},
		wantErrCode: codes.FailedPrecondition,
		wantErrDetails: &spb.ModifyRPCErrorDetails{
			Reason: spb.ModifyRPCErrorDetails_MODIFY_NOT_ALLOWED,
		},
	}, {
		desc: "invalid combination",
		inServer: &Server{
			cs: map[string]*clientState{
				"c1": {params: &clientParams{}},
			},
		},
		inID: "c1",
		inParams: &spb.SessionParameters{
			Persistence: spb.SessionParameters_PRESERVE,
			Redundancy:  spb.SessionParameters_ALL_PRIMARY,
		},
		wantErrCode: codes.FailedPrecondition,
		wantErrDetails: &spb.ModifyRPCErrorDetails{
			Reason: spb.ModifyRPCErrorDetails_UNSUPPORTED_PARAMS,
		},
		// TODO(robjs): test invalid combination, since today we do not actually support >1 mode, we cannot
		// test this directly.

	}, {
		desc: "ALL_PRIMARY unsupported",
		inServer: &Server{
			cs: map[string]*clientState{
				"c1": {params: &clientParams{}},
			},
		},
		inID: "c1",
		inParams: &spb.SessionParameters{
			Redundancy: spb.SessionParameters_ALL_PRIMARY,
		},
		wantErrCode: codes.Unimplemented,
		wantErrDetails: &spb.ModifyRPCErrorDetails{
			Reason: spb.ModifyRPCErrorDetails_UNSUPPORTED_PARAMS,
		},
	}, {
		desc: "nil parameters",
		inServer: &Server{
			cs: map[string]*clientState{
				"c1": {params: &clientParams{}},
			},
		},
		wantErrCode: codes.Internal,
	}, {
		desc: "delete persistence unsupported",
		inServer: &Server{
			cs: map[string]*clientState{
				"c1": {params: &clientParams{}},
			},
		},
		inParams: &spb.SessionParameters{
			Redundancy:  spb.SessionParameters_SINGLE_PRIMARY,
			Persistence: spb.SessionParameters_DELETE,
		},
		wantErrCode: codes.Unimplemented,
		wantErrDetails: &spb.ModifyRPCErrorDetails{
			Reason: spb.ModifyRPCErrorDetails_UNSUPPORTED_PARAMS,
		},
	}, {
		desc: "received OK message",
		inServer: &Server{
			cs: map[string]*clientState{
				"c1": {params: &clientParams{}},
			},
		},
		inID: "c1",
		inParams: &spb.SessionParameters{
			Redundancy:  spb.SessionParameters_SINGLE_PRIMARY,
			Persistence: spb.SessionParameters_PRESERVE,
		},
		wantResponse: &spb.ModifyResponse{
			SessionParamsResult: &spb.SessionParametersResult{
				Status: spb.SessionParametersResult_OK,
			},
		},
	}, {
		desc: "check parameters match - FIB ACK",
		inServer: &Server{
			cs: map[string]*clientState{
				"c1": {
					params: &clientParams{
						FIBAck:       true,
						ExpectElecID: true,
						Persist:      true,
					},
				},
				"c2": {params: &clientParams{}},
			},
		},
		inID: "c2",
		inParams: &spb.SessionParameters{
			AckType:     spb.SessionParameters_RIB_AND_FIB_ACK,
			Redundancy:  spb.SessionParameters_SINGLE_PRIMARY,
			Persistence: spb.SessionParameters_PRESERVE,
		},
		wantResponse: &spb.ModifyResponse{
			SessionParamsResult: &spb.SessionParametersResult{
				Status: spb.SessionParametersResult_OK,
			},
		},
	}, {
		desc: "check parameters mismatch - FIB ACK",
		inServer: &Server{
			cs: map[string]*clientState{
				"c1": {
					params: &clientParams{
						FIBAck:       true,
						ExpectElecID: true,
						Persist:      true,
					},
				},
				"c2": {params: &clientParams{}},
			},
		},
		inID: "c2",
		inParams: &spb.SessionParameters{
			AckType:     spb.SessionParameters_RIB_ACK,
			Redundancy:  spb.SessionParameters_SINGLE_PRIMARY,
			Persistence: spb.SessionParameters_PRESERVE,
		},
		wantErrCode: codes.FailedPrecondition,
		wantErrDetails: &spb.ModifyRPCErrorDetails{
			Reason: spb.ModifyRPCErrorDetails_PARAMS_DIFFER_FROM_OTHER_CLIENTS,
		},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			s := tt.inServer
			got, err := s.checkParams(tt.inID, tt.inParams, tt.inGotMsg)
			if err != nil {
				st, ok := status.FromError(err)
				if !ok {
					t.Fatalf("got error that was not a status.Status, got: %v", err)
				}
				if st.Code() != tt.wantErrCode {
					t.Fatalf("did not get expected code, got: %s, want: %s, error: %v", st.Code(), tt.wantErrCode, err)
				}

				if errS := chkErrDetails(st, tt.wantErrDetails); errS != "" {
					t.Fatal(errS)
				}

				return
			}

			if diff := cmp.Diff(got, tt.wantResponse, protocmp.Transform()); diff != "" {
				t.Fatalf("did not get expected response, diff(-got,+want):\n%s", diff)
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
		desc:       "equal",
		inCand:     &spb.Uint128{High: 42, Low: 42},
		inExist:    &spb.Uint128{High: 42, Low: 42},
		wantMaster: true,
		wantEqual:  true,
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

// chkErrDetails is a helper to check whether a status contains an expected ModifyRPCErrorDetails.
func chkErrDetails(st *status.Status, d *spb.ModifyRPCErrorDetails) string {
	if d == nil {
		// skip check if it's not expected
		return ""
	}
	if got := len(st.Details()); got != 1 {
		return fmt.Sprintf("did not get expected number of details arguments, got: %d (%v), want: 1", got, st.Details())
	}
	if got, want := st.Details()[0], d; !cmp.Equal(got, want, protocmp.Transform()) {
		return fmt.Sprintf("did not get expected error details, got: %s, want: %s", got, want)
	}
	return ""
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
		wantErrDetails   *spb.ModifyRPCErrorDetails
	}{{
		desc: "becomes master - no election ID",
		inServer: &Server{
			cs: map[string]*clientState{
				"c1": {
					params: &clientParams{
						ExpectElecID: true,
					}},
			},
		},
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
			cs: map[string]*clientState{
				"c1": {
					params: &clientParams{
						ExpectElecID: true,
					}},
			},
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
			cs: map[string]*clientState{
				"c1": {
					params: &clientParams{
						ExpectElecID: true,
					},
				},
			},
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
	}, {
		desc: "not expecting election",
		inServer: &Server{
			cs: map[string]*clientState{
				"c1": {
					params: &clientParams{
						ExpectElecID: false,
					},
				},
			},
		},
		inID:        "c1",
		wantErrCode: codes.FailedPrecondition,
		wantErrDetails: &spb.ModifyRPCErrorDetails{
			Reason: spb.ModifyRPCErrorDetails_ELECTION_ID_IN_ALL_PRIMARY,
		},
	}, {
		desc: "zero is an invalid input value",
		inServer: &Server{
			cs: map[string]*clientState{
				"c1": {
					params: &clientParams{
						ExpectElecID: true,
					}},
			},
		},
		inID:        "c1",
		inElecID:    &spb.Uint128{High: 0, Low: 0},
		wantErrCode: codes.InvalidArgument,
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
					t.Fatalf("did not get expected error code, got: %s, want: %s (error: %v)", got, want, err)
				}

				if errS := chkErrDetails(st, tt.wantErrDetails); errS != "" {
					t.Fatal(errS)
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

func checkStatusErr(t *testing.T, err error, wantCode codes.Code, wantReason spb.ModifyRPCErrorDetails_Reason) {
	t.Helper()
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("got an error that was not a status, %v", err)
	}
	if st.Code() != wantCode {
		t.Fatalf("did not get expected code, got: %s (%s), want: %s", st.Code(), st.Proto(), wantCode)
	}

	if wantReason != 0 {
		if g := len(st.Details()); g != 1 {
			t.Fatalf("did not get expected details, got: %d entries, want: 1", g)
		}

		dets, ok := st.Details()[0].(*spb.ModifyRPCErrorDetails)
		if !ok {
			t.Fatalf("got bad proto in details, got: %T, want: *spb.ModifyRPCErrorDetails", st.Details()[0])
		}
		if got, want := dets.Reason, wantReason; got != want {
			t.Fatalf("did not get expected reason, got: %s, want: %s", got, want)
		}
	}
}

func TestDoModify(t *testing.T) {
	type expectedMsg struct {
		result    *spb.ModifyResponse
		errCode   codes.Code
		errReason spb.ModifyRPCErrorDetails_Reason
	}
	tests := []struct {
		desc     string
		inCID    string
		inServer *Server
		inOps    []*spb.AFTOperation
		wantMsg  []*expectedMsg
	}{{
		desc: "unknown client",
		inServer: func() *Server {
			s, err := New()
			if err != nil {
				t.Fatalf("cannot create server, %v", err)
			}
			return s
		}(),
		inCID: "unknown",
		wantMsg: []*expectedMsg{{
			errCode: codes.Internal,
		}},
	}, {
		desc: "unsupported default parameters",
		inServer: func() *Server {
			s, err := New()
			if err != nil {
				t.Fatalf("cannot create server, error: %v", err)
			}
			s.cs["testclient"] = &clientState{}
			return s
		}(),
		inCID: "testclient",
		wantMsg: []*expectedMsg{{
			errCode:   codes.Unimplemented,
			errReason: spb.ModifyRPCErrorDetails_UNSUPPORTED_PARAMS,
		}},
	}, {
		desc: "not expecting election ID",
		inServer: func() *Server {
			s, err := New()
			if err != nil {
				t.Fatalf("cannot create server, error: %v", err)
			}
			s.cs["testclient"] = &clientState{
				params: &clientParams{
					ExpectElecID: false,
				},
			}
			return s
		}(),
		inCID: "testclient",
		wantMsg: []*expectedMsg{{
			errCode:   codes.Unimplemented,
			errReason: spb.ModifyRPCErrorDetails_UNSUPPORTED_PARAMS,
		}},
	}, {
		desc: "not expecting persist=false",
		inServer: func() *Server {
			s, err := New()
			if err != nil {
				t.Fatalf("cannot create server, error: %v", err)
			}
			s.cs["testclient"] = &clientState{
				params: &clientParams{
					Persist: false,
				},
			}
			return s
		}(),
		inCID: "testclient",
		wantMsg: []*expectedMsg{{
			errCode:   codes.Unimplemented,
			errReason: spb.ModifyRPCErrorDetails_UNSUPPORTED_PARAMS,
		}},
	}, {
		desc: "add to default network instance",
		inServer: func() *Server {
			s, err := New()
			if err != nil {
				t.Fatalf("cannot create server, error: %v", err)
			}
			s.cs["testclient"] = &clientState{
				params: &clientParams{
					Persist:      true,
					ExpectElecID: true,
					FIBAck:       true,
				},
				lastElecID: &spb.Uint128{High: 42, Low: 42},
			}
			s.curElecID = &spb.Uint128{High: 42, Low: 42}
			s.curMaster = "testclient"
			return s
		}(),
		inCID: "testclient",
		inOps: []*spb.AFTOperation{{
			Id:              1,
			NetworkInstance: DefaultNetworkInstanceName,
			Op:              spb.AFTOperation_ADD,
			ElectionId:      &spb.Uint128{High: 42, Low: 42},
			Entry: &spb.AFTOperation_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index:   1,
					NextHop: &aftpb.Afts_NextHop{},
				},
			},
		}},
		wantMsg: []*expectedMsg{{
			result: &spb.ModifyResponse{
				Result: []*spb.AFTResult{{
					Id:     1,
					Status: spb.AFTResult_RIB_PROGRAMMED,
				}, {
					Id:     1,
					Status: spb.AFTResult_FIB_PROGRAMMED,
				}},
			},
		}},
	}, {
		desc: "add to network instance added after server",
		inServer: func() *Server {
			s, err := New()
			if err != nil {
				t.Fatalf("cannot create server, error: %v", err)
			}
			s.cs["testclient"] = &clientState{
				params: &clientParams{
					Persist:      true,
					ExpectElecID: true,
					FIBAck:       true,
				},
				lastElecID: &spb.Uint128{High: 42, Low: 42},
			}
			s.curElecID = &spb.Uint128{High: 42, Low: 42}
			s.curMaster = "testclient"
			if err := s.AddNetworkInstance("FISH"); err != nil {
				t.Fatalf("cannot add network instace: %v", err)
			}
			return s
		}(),
		inCID: "testclient",
		inOps: []*spb.AFTOperation{{
			Id:              1,
			NetworkInstance: "FISH",
			Op:              spb.AFTOperation_ADD,
			ElectionId:      &spb.Uint128{High: 42, Low: 42},
			Entry: &spb.AFTOperation_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index:   1,
					NextHop: &aftpb.Afts_NextHop{},
				},
			},
		}},
		wantMsg: []*expectedMsg{{
			result: &spb.ModifyResponse{
				Result: []*spb.AFTResult{{
					Id:     1,
					Status: spb.AFTResult_RIB_PROGRAMMED,
				}, {
					Id:     1,
					Status: spb.AFTResult_FIB_PROGRAMMED,
				}},
			},
		}},
	}, {
		desc: "add to unknown network instance",
		inServer: func() *Server {
			s, err := New()
			if err != nil {
				t.Fatalf("cannot create server, error: %v", err)
			}
			s.cs["testclient"] = &clientState{
				params: &clientParams{
					Persist:      true,
					ExpectElecID: true,
					FIBAck:       true,
				},
				lastElecID: &spb.Uint128{High: 42, Low: 42},
			}
			s.curElecID = &spb.Uint128{High: 42, Low: 42}
			s.curMaster = "testclient"
			return s
		}(),
		inCID: "testclient",
		inOps: []*spb.AFTOperation{{
			Id:              42,
			NetworkInstance: "FISH",
			Op:              spb.AFTOperation_ADD,
			ElectionId:      &spb.Uint128{High: 42, Low: 42},
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix: "1.1.1.1/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{
						NextHopGroup: &wpb.UintValue{Value: 1},
					},
				},
			},
		}},
		wantMsg: []*expectedMsg{{
			result: &spb.ModifyResponse{
				Result: []*spb.AFTResult{{
					Id:     42,
					Status: spb.AFTResult_FAILED,
					ErrorDetails: &spb.AFTErrorDetails{
						ErrorMessage: `unknown network instance "FISH" specified`,
					},
				}},
			},
		}},
	}, {
		desc: "add with forward references, when forward references are disabled",
		inServer: func() *Server {
			s, err := New(WithNoRIBForwardReferences())
			if err != nil {
				t.Fatalf("cannot create server, error: %v", err)
			}
			s.cs["testclient"] = &clientState{
				params: &clientParams{
					Persist:      true,
					ExpectElecID: true,
					FIBAck:       true,
				},
				lastElecID: &spb.Uint128{High: 42, Low: 42},
			}
			s.curElecID = &spb.Uint128{High: 42, Low: 42}
			s.curMaster = "testclient"
			return s
		}(),
		inCID: "testclient",
		inOps: []*spb.AFTOperation{{
			Id:              42,
			NetworkInstance: DefaultNetworkInstanceName,
			Op:              spb.AFTOperation_ADD,
			ElectionId:      &spb.Uint128{High: 42, Low: 42},
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix: "1.1.1.1/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{
						NextHopGroup: &wpb.UintValue{Value: 1},
					},
				},
			},
		}},
		wantMsg: []*expectedMsg{{
			result: &spb.ModifyResponse{
				Result: []*spb.AFTResult{{
					Id:     42,
					Status: spb.AFTResult_FAILED,
					ErrorDetails: &spb.AFTErrorDetails{
						ErrorMessage: `operation 42 has unresolved dependencies`,
					},
				}},
			},
		}},
	}, {
		desc: "invalid operation",
		inServer: func() *Server {
			s, err := New()
			if err != nil {
				t.Fatalf("cannot create server, error: %v", err)
			}
			s.cs["testclient"] = &clientState{
				params: &clientParams{
					Persist:      true,
					ExpectElecID: true,
					FIBAck:       true,
				},
				lastElecID: &spb.Uint128{High: 42, Low: 42},
			}
			s.curElecID = &spb.Uint128{High: 42, Low: 42}
			s.curMaster = "testclient"
			return s
		}(),
		inCID: "testclient",
		inOps: []*spb.AFTOperation{{
			Id:              84,
			NetworkInstance: DefaultNetworkInstanceName,
			Op:              spb.AFTOperation_ADD,
			ElectionId:      &spb.Uint128{High: 42, Low: 42},
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix: "F-I-S-H",
				},
			},
		}},
		wantMsg: []*expectedMsg{{
			result: &spb.ModifyResponse{
				Result: []*spb.AFTResult{{
					Id:     84,
					Status: spb.AFTResult_FAILED,
					ErrorDetails: &spb.AFTErrorDetails{
						ErrorMessage: `invalid IPv4Entry, could not parse a field within the list gribi_aft.Afts.ipv4_entry , nil list member in field gribi_aft.Afts.Ipv4EntryKey.ipv4_entry, <nil>`,
					},
				}},
			},
		}},
	}, {
		desc: "two valid operations",
		inServer: func() *Server {
			s, err := New()
			if err != nil {
				t.Fatalf("cannot create server, error: %v", err)
			}
			s.cs["testclient"] = &clientState{
				params: &clientParams{
					Persist:      true,
					ExpectElecID: true,
					FIBAck:       true,
				},
				lastElecID: &spb.Uint128{High: 42, Low: 42},
			}
			s.curElecID = &spb.Uint128{High: 42, Low: 42}
			s.curMaster = "testclient"
			return s
		}(),
		inCID: "testclient",
		inOps: []*spb.AFTOperation{{
			Id:              1,
			NetworkInstance: DefaultNetworkInstanceName,
			Op:              spb.AFTOperation_ADD,
			ElectionId:      &spb.Uint128{High: 42, Low: 42},
			Entry: &spb.AFTOperation_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index:   1,
					NextHop: &aftpb.Afts_NextHop{},
				},
			},
		}, {
			Id:              2,
			NetworkInstance: DefaultNetworkInstanceName,
			Op:              spb.AFTOperation_ADD,
			ElectionId:      &spb.Uint128{High: 42, Low: 42},
			Entry: &spb.AFTOperation_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index:   2,
					NextHop: &aftpb.Afts_NextHop{},
				},
			},
		}},
		wantMsg: []*expectedMsg{{
			result: &spb.ModifyResponse{
				Result: []*spb.AFTResult{{
					Id:     1,
					Status: spb.AFTResult_RIB_PROGRAMMED,
				}, {
					Id:     1,
					Status: spb.AFTResult_FIB_PROGRAMMED,
				}},
			},
		}, {
			result: &spb.ModifyResponse{
				Result: []*spb.AFTResult{{
					Id:     2,
					Status: spb.AFTResult_RIB_PROGRAMMED,
				}, {
					Id:     2,
					Status: spb.AFTResult_FIB_PROGRAMMED,
				}},
			},
		}},
	}, {
		desc: "ipv4 to one next-hop group containing two next-hops",
		inServer: func() *Server {
			s, err := New()
			if err != nil {
				t.Fatalf("cannot create server, error: %v", err)
			}
			s.cs["testclient"] = &clientState{
				params: &clientParams{
					Persist:      true,
					ExpectElecID: true,
					FIBAck:       true,
				},
				lastElecID: &spb.Uint128{High: 42, Low: 42},
			}
			s.curElecID = &spb.Uint128{High: 42, Low: 42}
			s.curMaster = "testclient"
			return s
		}(),
		inCID: "testclient",
		inOps: []*spb.AFTOperation{{
			Id:              1,
			NetworkInstance: DefaultNetworkInstanceName,
			Op:              spb.AFTOperation_ADD,
			ElectionId:      &spb.Uint128{High: 42, Low: 42},
			Entry: &spb.AFTOperation_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index:   1,
					NextHop: &aftpb.Afts_NextHop{},
				},
			},
		}, {
			Id:              2,
			NetworkInstance: DefaultNetworkInstanceName,
			Op:              spb.AFTOperation_ADD,
			ElectionId:      &spb.Uint128{High: 42, Low: 42},
			Entry: &spb.AFTOperation_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index:   2,
					NextHop: &aftpb.Afts_NextHop{},
				},
			},
		}, {
			Id:              3,
			NetworkInstance: DefaultNetworkInstanceName,
			Op:              spb.AFTOperation_ADD,
			ElectionId:      &spb.Uint128{High: 42, Low: 42},
			Entry: &spb.AFTOperation_NextHopGroup{
				NextHopGroup: &aftpb.Afts_NextHopGroupKey{
					Id: 1,
					NextHopGroup: &aftpb.Afts_NextHopGroup{
						NextHop: []*aftpb.Afts_NextHopGroup_NextHopKey{{
							Index:   1,
							NextHop: &aftpb.Afts_NextHopGroup_NextHop{},
						}, {
							Index:   2,
							NextHop: &aftpb.Afts_NextHopGroup_NextHop{},
						}},
					},
				},
			},
		}, {
			Id:              4,
			NetworkInstance: DefaultNetworkInstanceName,
			Op:              spb.AFTOperation_ADD,
			ElectionId:      &spb.Uint128{High: 42, Low: 42},
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix: "1.0.0.0/8",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{
						NextHopGroup: &wpb.UintValue{Value: 1},
					},
				},
			},
		}},
		wantMsg: []*expectedMsg{{
			result: &spb.ModifyResponse{
				Result: []*spb.AFTResult{{
					Id:     1,
					Status: spb.AFTResult_RIB_PROGRAMMED,
				}, {
					Id:     1,
					Status: spb.AFTResult_FIB_PROGRAMMED,
				}},
			},
		}, {
			result: &spb.ModifyResponse{
				Result: []*spb.AFTResult{{
					Id:     2,
					Status: spb.AFTResult_RIB_PROGRAMMED,
				}, {
					Id:     2,
					Status: spb.AFTResult_FIB_PROGRAMMED,
				}},
			},
		}, {
			result: &spb.ModifyResponse{
				Result: []*spb.AFTResult{{
					Id:     3,
					Status: spb.AFTResult_RIB_PROGRAMMED,
				}, {
					Id:     3,
					Status: spb.AFTResult_FIB_PROGRAMMED,
				}},
			},
		}, {
			result: &spb.ModifyResponse{
				Result: []*spb.AFTResult{{
					Id:     4,
					Status: spb.AFTResult_RIB_PROGRAMMED,
				}, {
					Id:     4,
					Status: spb.AFTResult_FIB_PROGRAMMED,
				}},
			},
		}},
	}, {
		desc: "one invalid operation (invalid NI) followed by one valid operation",
		inServer: func() *Server {
			s, err := New()
			if err != nil {
				t.Fatalf("cannot create server, error: %v", err)
			}
			s.cs["testclient"] = &clientState{
				params: &clientParams{
					Persist:      true,
					ExpectElecID: true,
					FIBAck:       true,
				},
				lastElecID: &spb.Uint128{High: 42, Low: 42},
			}
			s.curElecID = &spb.Uint128{High: 42, Low: 42}
			s.curMaster = "testclient"
			return s
		}(),
		inCID: "testclient",
		inOps: []*spb.AFTOperation{{
			Id:              84,
			NetworkInstance: "FISH",
			Op:              spb.AFTOperation_ADD,
			ElectionId:      &spb.Uint128{High: 42, Low: 42},
			Entry: &spb.AFTOperation_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index: 1,
					NextHop: &aftpb.Afts_NextHop{
						IpAddress: &wpb.StringValue{Value: "192.0.2.1"},
					},
				},
			},
		}, {
			Id:              96,
			NetworkInstance: DefaultNetworkInstanceName,
			Op:              spb.AFTOperation_ADD,
			ElectionId:      &spb.Uint128{High: 42, Low: 42},
			Entry: &spb.AFTOperation_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index: 1,
					NextHop: &aftpb.Afts_NextHop{
						IpAddress: &wpb.StringValue{Value: "192.0.2.1"},
					},
				},
			},
		}},
		wantMsg: []*expectedMsg{{
			result: &spb.ModifyResponse{
				Result: []*spb.AFTResult{{
					Id:     84,
					Status: spb.AFTResult_FAILED,
					ErrorDetails: &spb.AFTErrorDetails{
						ErrorMessage: `unknown network instance "FISH" specified`,
					},
				}},
			},
		}, {
			result: &spb.ModifyResponse{
				Result: []*spb.AFTResult{{
					Id:     96,
					Status: spb.AFTResult_RIB_PROGRAMMED,
				}, {
					Id:     96,
					Status: spb.AFTResult_FIB_PROGRAMMED,
				}},
			},
		}},
	}}

	type recvMsg struct {
		result *spb.ModifyResponse
		err    error
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			errCh := make(chan error)
			resCh := make(chan *spb.ModifyResponse)

			go tt.inServer.doModify(tt.inCID, tt.inOps, resCh, errCh)
			got := []*recvMsg{}
			for i := 0; i < len(tt.wantMsg); i++ {
				gotMsg := &recvMsg{}
				select {
				case err := <-errCh:
					gotMsg.err = err
				case got := <-resCh:
					gotMsg.result = got
				}
				got = append(got, gotMsg)
			}

			lessFn := func(i, j int) bool {
				switch {
				case got[i].err != nil && got[j].err != nil:
					return fmt.Sprintf("%v", got[i].err) < fmt.Sprintf("%v", got[j].err)
				case got[i].err != nil:
					return true
				case got[j].err != nil:
					return false
				default:
					iid := got[i].result.GetResult()[0].GetId()
					jid := got[j].result.GetResult()[0].GetId()
					return iid < jid
				}
			}
			sort.Slice(got, lessFn)
			sort.Slice(tt.wantMsg, lessFn)

			if len(got) != len(tt.wantMsg) {
				t.Fatalf("did not get expected number of messages, got: %d, want: %d", len(got), len(tt.wantMsg))
			}

			for i := 0; i < len(tt.wantMsg); i++ {
				wantMsg := tt.wantMsg[i]
				gotMsg := got[i]
				if err := gotMsg.err; err != nil {
					checkStatusErr(t, err, wantMsg.errCode, wantMsg.errReason)
				}
				if wantMsg.result != nil {
					if diff := cmp.Diff(gotMsg.result, wantMsg.result, protocmp.Transform()); diff != "" {
						t.Fatalf("did not get expected response, diff(-got,+want):\n%s", diff)
					}
				}
			}
		})
	}
}

func TestModifyEntry(t *testing.T) {
	defName := DefaultNetworkInstanceName
	tests := []struct {
		desc           string
		inRIB          *rib.RIB
		inNI           string
		inOp           *spb.AFTOperation
		inFIBACK       bool
		inElection     *electionDetails
		wantResponse   *spb.ModifyResponse
		wantErrCode    codes.Code
		wantErrDetails spb.ModifyRPCErrorDetails_Reason
	}{{
		desc:        "nil election ID",
		inRIB:       rib.New(defName),
		inOp:        &spb.AFTOperation{},
		wantErrCode: codes.FailedPrecondition,
	}, {
		desc:  "invalid election",
		inRIB: rib.New(defName),
		inOp: &spb.AFTOperation{
			ElectionId: &spb.Uint128{High: 0, Low: 1},
		},
		wantErrCode: codes.Internal,
	}, {
		desc:  "client hasn't sent election ID",
		inRIB: rib.New(defName),
		inOp: &spb.AFTOperation{
			ElectionId: &spb.Uint128{High: 0, Low: 1},
		},
		inElection: &electionDetails{
			master: "some-client",
			ID:     &spb.Uint128{High: 1, Low: 1},
		},
		wantErrCode: codes.FailedPrecondition,
	}, {
		desc:  "client gives higher ID than known master",
		inRIB: rib.New(defName),
		inOp: &spb.AFTOperation{
			ElectionId: &spb.Uint128{High: 2, Low: 0},
			Id:         1,
		},
		inElection: &electionDetails{
			master: "this-client",
			// note: this is an internal error that should never happen :-)
			// so of course we test what happens when it does. In this case
			// we have got to the stage whereby we decided that our client sent
			// us a later ID than the one that we think is the master, even though
			// this client is the master. In this case, we've missed updating
			// the master ID somehow as new elections happen.
			ID:           &spb.Uint128{High: 1, Low: 1},
			client:       "this-client",
			clientLatest: &spb.Uint128{High: 2, Low: 0},
		},
		wantErrCode: codes.FailedPrecondition,
	}, {
		desc:  "client gives lower ID than known master",
		inRIB: rib.New(defName),
		inOp: &spb.AFTOperation{
			ElectionId: &spb.Uint128{High: 1, Low: 0},
			Id:         1,
		},
		inElection: &electionDetails{
			master:       "this-client",
			ID:           &spb.Uint128{High: 1, Low: 1},
			client:       "this-client",
			clientLatest: &spb.Uint128{High: 1, Low: 0},
		},
		wantResponse: &spb.ModifyResponse{
			Result: []*spb.AFTResult{{
				Id:     1,
				Status: spb.AFTResult_FAILED,
			}},
		},
	}, {
		desc:  "client is not master - by name",
		inRIB: rib.New(defName),
		inOp: &spb.AFTOperation{
			ElectionId: &spb.Uint128{High: 0, Low: 1},
			Id:         2,
		},
		inElection: &electionDetails{
			master:       "not-this-client",
			ID:           &spb.Uint128{High: 1, Low: 1},
			client:       "this-client",
			clientLatest: &spb.Uint128{High: 2, Low: 0},
		},
		wantResponse: &spb.ModifyResponse{
			Result: []*spb.AFTResult{{
				Id:     2,
				Status: spb.AFTResult_FAILED,
			}},
		},
	}, {
		desc:  "client is not master - by mismatched latest",
		inRIB: rib.New(defName),
		inOp: &spb.AFTOperation{
			ElectionId: &spb.Uint128{High: 0, Low: 2},
			Id:         2,
		},
		inElection: &electionDetails{
			master:       "this-client",
			ID:           &spb.Uint128{High: 0, Low: 1},
			client:       "this-client",
			clientLatest: &spb.Uint128{High: 0, Low: 1},
		},
		wantResponse: &spb.ModifyResponse{
			Result: []*spb.AFTResult{{
				Id:     2,
				Status: spb.AFTResult_FAILED,
			}},
		},
	}, {
		desc:        "nil operation",
		inRIB:       rib.New(defName),
		wantErrCode: codes.Internal,
	}, {
		desc:  "unsupported operation type",
		inRIB: rib.New(defName, rib.DisableRIBCheckFn()),
		inNI:  defName,
		inOp: &spb.AFTOperation{
			ElectionId: &spb.Uint128{High: 0, Low: 2},
			Id:         2,
			Op:         spb.AFTOperation_INVALID,
		},
		inElection: &electionDetails{
			master:       "this-client",
			ID:           &spb.Uint128{High: 0, Low: 2},
			client:       "this-client",
			clientLatest: &spb.Uint128{High: 0, Low: 2},
		},
		wantResponse: &spb.ModifyResponse{
			Result: []*spb.AFTResult{{
				Id:     2,
				Status: spb.AFTResult_FAILED,
				ErrorDetails: &spb.AFTErrorDetails{
					ErrorMessage: "unsupported operation type supplied, INVALID",
				},
			}},
		},
	}, {
		desc:        "nil RIB",
		wantErrCode: codes.Internal,
	}, {
		desc:  "ADD v4: rib ACK",
		inRIB: rib.New(defName, rib.DisableRIBCheckFn()),
		inNI:  defName,
		inOp: &spb.AFTOperation{
			ElectionId: &spb.Uint128{High: 4, Low: 2},
			Id:         2,
			Op:         spb.AFTOperation_ADD,
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix: "2.2.2.2/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{
						NextHopGroup: &wpb.UintValue{Value: 1},
					},
				},
			},
		},
		inElection: &electionDetails{
			master:       "this-client",
			ID:           &spb.Uint128{High: 4, Low: 2},
			client:       "this-client",
			clientLatest: &spb.Uint128{High: 4, Low: 2},
		},
		wantResponse: &spb.ModifyResponse{
			Result: []*spb.AFTResult{{
				Id:     2,
				Status: spb.AFTResult_RIB_PROGRAMMED,
			}},
		},
	}, {
		desc:  "ADD v4: fib ACK",
		inRIB: rib.New(defName, rib.DisableRIBCheckFn()),
		inNI:  defName,
		inOp: &spb.AFTOperation{
			ElectionId: &spb.Uint128{High: 4, Low: 2},
			Id:         2,
			Op:         spb.AFTOperation_ADD,
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix: "2.2.2.2/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{
						NextHopGroup: &wpb.UintValue{Value: 1},
					},
				},
			},
		},
		inElection: &electionDetails{
			master:       "this-client",
			ID:           &spb.Uint128{High: 4, Low: 2},
			client:       "this-client",
			clientLatest: &spb.Uint128{High: 4, Low: 2},
		},
		inFIBACK: true,
		wantResponse: &spb.ModifyResponse{
			Result: []*spb.AFTResult{{
				Id:     2,
				Status: spb.AFTResult_RIB_PROGRAMMED,
			}, {
				Id:     2,
				Status: spb.AFTResult_FIB_PROGRAMMED,
			}},
		},
	}, {
		desc:  "ADD v6: rib ACK",
		inRIB: rib.New(defName, rib.DisableRIBCheckFn()),
		inNI:  defName,
		inOp: &spb.AFTOperation{
			ElectionId: &spb.Uint128{High: 4, Low: 2},
			Id:         2,
			Op:         spb.AFTOperation_ADD,
			Entry: &spb.AFTOperation_Ipv6{
				Ipv6: &aftpb.Afts_Ipv6EntryKey{
					Prefix: "2001:db8::/32",
					Ipv6Entry: &aftpb.Afts_Ipv6Entry{
						NextHopGroup: &wpb.UintValue{Value: 1},
					},
				},
			},
		},
		inElection: &electionDetails{
			master:       "this-client",
			ID:           &spb.Uint128{High: 4, Low: 2},
			client:       "this-client",
			clientLatest: &spb.Uint128{High: 4, Low: 2},
		},
		wantResponse: &spb.ModifyResponse{
			Result: []*spb.AFTResult{{
				Id:     2,
				Status: spb.AFTResult_RIB_PROGRAMMED,
			}},
		},
	}, {
		desc:  "ADD v6: fib ACK",
		inRIB: rib.New(defName, rib.DisableRIBCheckFn()),
		inNI:  defName,
		inOp: &spb.AFTOperation{
			ElectionId: &spb.Uint128{High: 4, Low: 2},
			Id:         2,
			Op:         spb.AFTOperation_ADD,
			Entry: &spb.AFTOperation_Ipv6{
				Ipv6: &aftpb.Afts_Ipv6EntryKey{
					Prefix: "2001:db8:cafe::/48",
					Ipv6Entry: &aftpb.Afts_Ipv6Entry{
						NextHopGroup: &wpb.UintValue{Value: 1},
					},
				},
			},
		},
		inElection: &electionDetails{
			master:       "this-client",
			ID:           &spb.Uint128{High: 4, Low: 2},
			client:       "this-client",
			clientLatest: &spb.Uint128{High: 4, Low: 2},
		},
		inFIBACK: true,
		wantResponse: &spb.ModifyResponse{
			Result: []*spb.AFTResult{{
				Id:     2,
				Status: spb.AFTResult_RIB_PROGRAMMED,
			}, {
				Id:     2,
				Status: spb.AFTResult_FIB_PROGRAMMED,
			}},
		},
	}, {
		desc:  "ADD MPLS: rib ACK",
		inRIB: rib.New(defName, rib.DisableRIBCheckFn()),
		inNI:  defName,
		inOp: &spb.AFTOperation{
			ElectionId: &spb.Uint128{High: 4, Low: 2},
			Id:         2,
			Op:         spb.AFTOperation_ADD,
			Entry: &spb.AFTOperation_Mpls{
				Mpls: &aftpb.Afts_LabelEntryKey{
					Label: &aftpb.Afts_LabelEntryKey_LabelUint64{
						LabelUint64: 42,
					},
					LabelEntry: &aftpb.Afts_LabelEntry{
						NextHopGroup: &wpb.UintValue{Value: 1},
					},
				},
			},
		},
		inElection: &electionDetails{
			master:       "this-client",
			ID:           &spb.Uint128{High: 4, Low: 2},
			client:       "this-client",
			clientLatest: &spb.Uint128{High: 4, Low: 2},
		},
		wantResponse: &spb.ModifyResponse{
			Result: []*spb.AFTResult{{
				Id:     2,
				Status: spb.AFTResult_RIB_PROGRAMMED,
			}},
		},
	}, {
		desc:  "ADD NHG",
		inRIB: rib.New(defName, rib.DisableRIBCheckFn()),
		inNI:  defName,
		inOp: &spb.AFTOperation{
			ElectionId: &spb.Uint128{High: 4, Low: 2},
			Id:         2,
			Op:         spb.AFTOperation_ADD,
			Entry: &spb.AFTOperation_NextHopGroup{
				NextHopGroup: &aftpb.Afts_NextHopGroupKey{
					Id:           2,
					NextHopGroup: &aftpb.Afts_NextHopGroup{},
				},
			},
		},
		inElection: &electionDetails{
			master:       "this-client",
			ID:           &spb.Uint128{High: 4, Low: 2},
			client:       "this-client",
			clientLatest: &spb.Uint128{High: 4, Low: 2},
		},
		wantResponse: &spb.ModifyResponse{
			Result: []*spb.AFTResult{{
				Id:     2,
				Status: spb.AFTResult_RIB_PROGRAMMED,
			}},
		},
	}, {
		desc:  "ADD NH",
		inRIB: rib.New(defName, rib.DisableRIBCheckFn()),
		inNI:  defName,
		inOp: &spb.AFTOperation{
			ElectionId: &spb.Uint128{High: 4, Low: 2},
			Id:         2,
			Op:         spb.AFTOperation_ADD,
			Entry: &spb.AFTOperation_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index:   2,
					NextHop: &aftpb.Afts_NextHop{},
				},
			},
		},
		inElection: &electionDetails{
			master:       "this-client",
			ID:           &spb.Uint128{High: 4, Low: 2},
			client:       "this-client",
			clientLatest: &spb.Uint128{High: 4, Low: 2},
		},
		wantResponse: &spb.ModifyResponse{
			Result: []*spb.AFTResult{{
				Id:     2,
				Status: spb.AFTResult_RIB_PROGRAMMED,
			}},
		},
	}, {
		desc:        "nil RIB",
		inRIB:       nil,
		wantErrCode: codes.Internal,
	}, {
		desc:        "invalid RIB",
		inRIB:       &rib.RIB{},
		inNI:        "fish",
		wantErrCode: codes.Internal,
	}, {
		desc:  "ADD NHG",
		inRIB: rib.New(defName, rib.DisableRIBCheckFn()),
		inNI:  defName,
		inOp: &spb.AFTOperation{
			ElectionId: &spb.Uint128{High: 4, Low: 2},
			Id:         2,
			Op:         spb.AFTOperation_ADD,
			Entry: &spb.AFTOperation_NextHopGroup{
				NextHopGroup: &aftpb.Afts_NextHopGroupKey{
					Id:           2,
					NextHopGroup: &aftpb.Afts_NextHopGroup{},
				},
			},
		},
		inElection: &electionDetails{
			master:       "this-client",
			ID:           &spb.Uint128{High: 4, Low: 2},
			client:       "this-client",
			clientLatest: &spb.Uint128{High: 4, Low: 2},
		},
		wantResponse: &spb.ModifyResponse{
			Result: []*spb.AFTResult{{
				Id:     2,
				Status: spb.AFTResult_RIB_PROGRAMMED,
			}},
		},
	}, {
		desc: "DELETE NH",
		inRIB: func() *rib.RIB {
			r := rib.New(defName)
			if _, _, err := r.AddEntry(defName, &spb.AFTOperation{
				Id: 3,
				Op: spb.AFTOperation_ADD,
				Entry: &spb.AFTOperation_NextHop{
					NextHop: &aftpb.Afts_NextHopKey{
						Index:   2,
						NextHop: &aftpb.Afts_NextHop{},
					},
				},
			}); err != nil {
				panic(fmt.Sprintf("cannot build test case, got err: %v", err))
			}
			return r
		}(),
		inNI: defName,
		inOp: &spb.AFTOperation{
			ElectionId: &spb.Uint128{High: 4, Low: 2},
			Id:         2,
			Op:         spb.AFTOperation_DELETE,
			Entry: &spb.AFTOperation_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index: 2,
				},
			},
		},
		inElection: &electionDetails{
			master:       "this-client",
			ID:           &spb.Uint128{High: 4, Low: 2},
			client:       "this-client",
			clientLatest: &spb.Uint128{High: 4, Low: 2},
		},
		wantResponse: &spb.ModifyResponse{
			Result: []*spb.AFTResult{{
				Id:     2,
				Status: spb.AFTResult_RIB_PROGRAMMED,
			}},
		},
	}, {
		desc:  "DELETE Missing NHG",
		inRIB: rib.New(defName),
		inNI:  defName,
		inOp: &spb.AFTOperation{
			ElectionId: &spb.Uint128{High: 4, Low: 2},
			Id:         2,
			Op:         spb.AFTOperation_DELETE,
			Entry: &spb.AFTOperation_NextHopGroup{
				NextHopGroup: &aftpb.Afts_NextHopGroupKey{
					Id: 2,
				},
			},
		},
		inElection: &electionDetails{
			master:       "this-client",
			ID:           &spb.Uint128{High: 4, Low: 2},
			client:       "this-client",
			clientLatest: &spb.Uint128{High: 4, Low: 2},
		},
		wantResponse: &spb.ModifyResponse{
			Result: []*spb.AFTResult{{
				Id:     2,
				Status: spb.AFTResult_RIB_PROGRAMMED,
			}},
		},
	}, {
		desc: "DELETE NH failed due to refcount",
		inRIB: func() *rib.RIB {
			r := rib.New(defName)
			if _, fails, err := r.AddEntry(defName, &spb.AFTOperation{
				Id: 3,
				Op: spb.AFTOperation_ADD,
				Entry: &spb.AFTOperation_NextHop{
					NextHop: &aftpb.Afts_NextHopKey{
						Index:   2,
						NextHop: &aftpb.Afts_NextHop{},
					},
				},
			}); err != nil || len(fails) != 0 {
				panic(fmt.Sprintf("cannot build test case, can't add NH, got err: %v, fails: %v", err, fails[0]))
			}

			if _, fails, err := r.AddEntry(defName, &spb.AFTOperation{
				Id: 3,
				Op: spb.AFTOperation_ADD,
				Entry: &spb.AFTOperation_NextHopGroup{
					NextHopGroup: &aftpb.Afts_NextHopGroupKey{
						Id: 2,
						NextHopGroup: &aftpb.Afts_NextHopGroup{
							NextHop: []*aftpb.Afts_NextHopGroup_NextHopKey{{
								Index:   2,
								NextHop: &aftpb.Afts_NextHopGroup_NextHop{},
							}},
						},
					},
				},
			}); err != nil || len(fails) != 0 {
				panic(fmt.Sprintf("cannot build test case, can't add NHG, got err: %v, fails: %v", err, fails[0]))

			}

			return r
		}(),
		inNI: defName,
		inOp: &spb.AFTOperation{
			ElectionId: &spb.Uint128{High: 4, Low: 2},
			Id:         2,
			Op:         spb.AFTOperation_DELETE,
			Entry: &spb.AFTOperation_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index: 2,
				},
			},
		},
		inElection: &electionDetails{
			master:       "this-client",
			ID:           &spb.Uint128{High: 4, Low: 2},
			client:       "this-client",
			clientLatest: &spb.Uint128{High: 4, Low: 2},
		},
		wantResponse: &spb.ModifyResponse{
			Result: []*spb.AFTResult{{
				Id:     2,
				Status: spb.AFTResult_FAILED,
				ErrorDetails: &spb.AFTErrorDetails{
					ErrorMessage: "",
				},
			}},
		},
	}, {
		desc: "REPLACE NH",
		inRIB: func() *rib.RIB {
			r := rib.New(defName)
			if _, _, err := r.AddEntry(defName, &spb.AFTOperation{
				Id: 3,
				Op: spb.AFTOperation_ADD,
				Entry: &spb.AFTOperation_NextHop{
					NextHop: &aftpb.Afts_NextHopKey{
						Index:   2,
						NextHop: &aftpb.Afts_NextHop{},
					},
				},
			}); err != nil {
				panic(fmt.Sprintf("cannot build test case, got err: %v", err))
			}
			return r
		}(),
		inNI: defName,
		inOp: &spb.AFTOperation{
			ElectionId: &spb.Uint128{High: 4, Low: 2},
			Id:         2,
			Op:         spb.AFTOperation_REPLACE,
			Entry: &spb.AFTOperation_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index: 2,
					NextHop: &aftpb.Afts_NextHop{
						IpAddress: &wpb.StringValue{Value: "NOT VALID"},
					},
				},
			},
		},
		inElection: &electionDetails{
			master:       "this-client",
			ID:           &spb.Uint128{High: 4, Low: 2},
			client:       "this-client",
			clientLatest: &spb.Uint128{High: 4, Low: 2},
		},
		wantResponse: &spb.ModifyResponse{
			Result: []*spb.AFTResult{{
				Id:     2,
				Status: spb.AFTResult_FAILED,
			}},
		},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got, err := modifyEntry(tt.inRIB, tt.inNI, tt.inOp, tt.inFIBACK, tt.inElection)
			if err != nil {
				checkStatusErr(t, err, tt.wantErrCode, tt.wantErrDetails)
			}
			if diff := cmp.Diff(got, tt.wantResponse, protocmp.Transform(), protocmp.IgnoreFields(&spb.AFTResult{}, "error_details")); diff != "" {
				t.Fatalf("did not get expected response, diff(-got,+want):\n%s", diff)
			}
		})
	}
}

func TestDoGet(t *testing.T) {
	// serverAllRIBs is a function that returns a server with one entry in each RIB
	// populated.
	serverAllRIBs := func() *Server {
		// use a nil function to check the RIB so that addEntry always succeeds
		s, err := New(DisableRIBCheckFn())
		if err != nil {
			t.Fatalf("cannot create server, %v", err)
		}

		if _, _, err := s.masterRIB.AddEntry(DefaultNetworkInstanceName, &spb.AFTOperation{
			Id:              1,
			NetworkInstance: DefaultNetworkInstanceName,
			Op:              spb.AFTOperation_ADD,
			Entry: &spb.AFTOperation_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index: 1,
					NextHop: &aftpb.Afts_NextHop{
						PushedMplsLabelStack: []*aftpb.Afts_NextHop_PushedMplsLabelStackUnion{{
							PushedMplsLabelStackUint64: 42,
						}},
					},
				},
			},
		}); err != nil {
			t.Fatalf("cannot build test case, NH, err: %v", err)
		}

		if _, _, err := s.masterRIB.AddEntry(DefaultNetworkInstanceName, &spb.AFTOperation{
			Id:              1,
			NetworkInstance: DefaultNetworkInstanceName,
			Op:              spb.AFTOperation_ADD,
			Entry: &spb.AFTOperation_NextHopGroup{
				NextHopGroup: &aftpb.Afts_NextHopGroupKey{
					Id:           1,
					NextHopGroup: &aftpb.Afts_NextHopGroup{},
				},
			},
		}); err != nil {
			t.Fatalf("cannot build test case, NHG, err: %v", err)
		}

		if _, _, err := s.masterRIB.AddEntry(DefaultNetworkInstanceName, &spb.AFTOperation{
			Id:              1,
			NetworkInstance: DefaultNetworkInstanceName,
			Op:              spb.AFTOperation_ADD,
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix:    "1.1.1.1/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
				},
			},
		}); err != nil {
			t.Fatalf("cannot build test case, IPv4, err: %v", err)
		}

		if _, _, err := s.masterRIB.AddEntry(DefaultNetworkInstanceName, &spb.AFTOperation{
			Id:              2,
			NetworkInstance: DefaultNetworkInstanceName,
			Op:              spb.AFTOperation_ADD,
			Entry: &spb.AFTOperation_Ipv6{
				Ipv6: &aftpb.Afts_Ipv6EntryKey{
					Prefix: "2001:db8::/32",
					Ipv6Entry: &aftpb.Afts_Ipv6Entry{
						NextHopGroup: &wpb.UintValue{Value: 1},
					},
				},
			},
		}); err != nil {
			t.Fatalf("cannot build test case, IPv6, err: %v", err)
		}

		if _, _, err := s.masterRIB.AddEntry(DefaultNetworkInstanceName, &spb.AFTOperation{
			Id:              2,
			NetworkInstance: DefaultNetworkInstanceName,
			Op:              spb.AFTOperation_ADD,
			Entry: &spb.AFTOperation_Mpls{
				Mpls: &aftpb.Afts_LabelEntryKey{
					Label: &aftpb.Afts_LabelEntryKey_LabelUint64{
						LabelUint64: 42,
					},
					LabelEntry: &aftpb.Afts_LabelEntry{},
				},
			},
		}); err != nil {
			t.Fatalf("cannot build test case, MPLS, err: %v", err)
		}

		return s
	}()

	tests := []struct {
		desc          string
		inReq         *spb.GetRequest
		inServer      *Server
		wantResponses []*spb.GetResponse
		wantErr       bool
	}{{
		desc: "single network-instance get, with empty RIB",
		inReq: &spb.GetRequest{
			NetworkInstance: &spb.GetRequest_Name{
				Name: DefaultNetworkInstanceName,
			},
			Aft: spb.AFTType_ALL,
		},
	}, {
		desc: "empty network instance name",
		inReq: &spb.GetRequest{
			NetworkInstance: &spb.GetRequest_Name{
				Name: "",
			},
			Aft: spb.AFTType_ALL,
		},
		wantErr: true,
	}, {
		desc: "all network instances",
		inReq: &spb.GetRequest{
			Aft: spb.AFTType_ALL,
			NetworkInstance: &spb.GetRequest_All{
				All: &spb.Empty{},
			},
		},
		inServer: func() *Server {
			vrfNames := []string{"ONE", "EIGHT", "FOUR"}
			s, err := New(
				DisableRIBCheckFn(),
				WithVRFs(vrfNames),
			)
			if err != nil {
				t.Fatalf("cannot create server, err: %v", err)
			}

			prefixes := []string{"1.1.1.1/32", "8.8.8.8/32", "8.8.4.4/32"}

			for i, pfx := range prefixes {
				if _, _, err := s.masterRIB.AddEntry(vrfNames[i], &spb.AFTOperation{
					Id:              uint64(i),
					NetworkInstance: vrfNames[i],
					Op:              spb.AFTOperation_ADD,
					Entry: &spb.AFTOperation_Ipv4{
						Ipv4: &aftpb.Afts_Ipv4EntryKey{
							Prefix:    pfx,
							Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
						},
					},
				}); err != nil {
					panic(fmt.Sprintf("cannot build testcase, %v", err))
				}
			}
			return s
		}(),
		wantResponses: []*spb.GetResponse{{
			Entry: []*spb.AFTEntry{{
				NetworkInstance: "ONE",
				Entry: &spb.AFTEntry_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix:    "1.1.1.1/32",
						Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
					},
				},
			}},
		}, {
			Entry: []*spb.AFTEntry{{
				NetworkInstance: "EIGHT",
				Entry: &spb.AFTEntry_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix:    "8.8.8.8/32",
						Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
					},
				},
			}},
		}, {
			Entry: []*spb.AFTEntry{{
				NetworkInstance: "FOUR",
				Entry: &spb.AFTEntry_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix:    "8.8.4.4/32",
						Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
					},
				},
			}},
		}},
	}, {
		desc: "single network-instance get with one entry in each table",
		inReq: &spb.GetRequest{
			NetworkInstance: &spb.GetRequest_Name{
				Name: DefaultNetworkInstanceName,
			},
			Aft: spb.AFTType_ALL,
		},
		inServer: serverAllRIBs,
		wantResponses: []*spb.GetResponse{{
			Entry: []*spb.AFTEntry{{
				NetworkInstance: DefaultNetworkInstanceName,
				Entry: &spb.AFTEntry_NextHopGroup{
					NextHopGroup: &aftpb.Afts_NextHopGroupKey{
						Id:           1,
						NextHopGroup: &aftpb.Afts_NextHopGroup{},
					},
				},
			}},
		}, {
			Entry: []*spb.AFTEntry{{
				NetworkInstance: DefaultNetworkInstanceName,
				Entry: &spb.AFTEntry_NextHop{
					NextHop: &aftpb.Afts_NextHopKey{
						Index: 1,
						NextHop: &aftpb.Afts_NextHop{
							PushedMplsLabelStack: []*aftpb.Afts_NextHop_PushedMplsLabelStackUnion{{
								PushedMplsLabelStackUint64: 42,
							}},
						},
					},
				},
			}},
		}, {
			Entry: []*spb.AFTEntry{{
				NetworkInstance: DefaultNetworkInstanceName,
				Entry: &spb.AFTEntry_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix:    "1.1.1.1/32",
						Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
					},
				},
			}},
		}, {
			Entry: []*spb.AFTEntry{{
				NetworkInstance: DefaultNetworkInstanceName,
				Entry: &spb.AFTEntry_Ipv6{
					Ipv6: &aftpb.Afts_Ipv6EntryKey{
						Prefix: "2001:db8::/32",
						Ipv6Entry: &aftpb.Afts_Ipv6Entry{
							NextHopGroup: &wpb.UintValue{Value: 1},
						},
					},
				},
			}},
		}, {
			Entry: []*spb.AFTEntry{{
				NetworkInstance: DefaultNetworkInstanceName,
				Entry: &spb.AFTEntry_Mpls{
					Mpls: &aftpb.Afts_LabelEntryKey{
						Label: &aftpb.Afts_LabelEntryKey_LabelUint64{
							LabelUint64: 42,
						},
						LabelEntry: &aftpb.Afts_LabelEntry{},
					},
				},
			}},
		}},
	}, {
		desc: "unsupported AFT",
		inReq: &spb.GetRequest{
			NetworkInstance: &spb.GetRequest_Name{
				Name: DefaultNetworkInstanceName,
			},
			Aft: spb.AFTType_MAC,
		},
		wantErr: true,
	}, {
		desc: "filter to ipv4",
		inReq: &spb.GetRequest{
			NetworkInstance: &spb.GetRequest_Name{
				Name: DefaultNetworkInstanceName,
			},
			Aft: spb.AFTType_IPV4,
		},
		inServer: serverAllRIBs,
		wantResponses: []*spb.GetResponse{{
			Entry: []*spb.AFTEntry{{
				NetworkInstance: DefaultNetworkInstanceName,
				Entry: &spb.AFTEntry_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix:    "1.1.1.1/32",
						Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
					},
				},
			}},
		}},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			errCh := make(chan error)
			doneCh := make(chan struct{})
			stopCh := make(chan struct{})
			msgCh := make(chan *spb.GetResponse)

			s := tt.inServer
			if tt.inServer == nil {
				var err error
				s, err = New()
				if err != nil {
					t.Fatalf("cannot create server, %v", err)
				}
			}
			go s.doGet(tt.inReq, msgCh, doneCh, stopCh, errCh)

			var gotErr error
			var done bool
			got := []*spb.GetResponse{}
			for !done {
				select {
				case r := <-msgCh:
					got = append(got, r)
				case err := <-errCh:
					gotErr = err
					done = true
				case <-doneCh:
					done = true
				}
			}

			if (gotErr != nil) != tt.wantErr {
				t.Fatalf("did not get expected error, got: %v, wantErr? %v", gotErr, tt.wantErr)
			}

			if diff := cmp.Diff(got, tt.wantResponses,
				cmpopts.EquateEmpty(),
				protocmp.Transform(),
				cmpopts.SortSlices(func(a, b *spb.GetResponse) bool {
					return prototext.Format(a) < prototext.Format(b)
				}),
				protocmp.SortRepeated(func(a, b *spb.AFTEntry) bool {
					return prototext.Format(a) < prototext.Format(b)
				})); diff != "" {
				t.Fatalf("did not get expected responses, diff(-got,+want):\n%s", diff)
			}
		})
	}
}

func TestCheckFlushRequest(t *testing.T) {
	tests := []struct {
		desc           string
		inServer       *Server
		inRequest      *spb.FlushRequest
		wantErrCode    codes.Code
		wantErrDetails *spb.FlushResponseError
	}{{
		desc: "network instance is not specified",
		inServer: &Server{
			curElecID: &spb.Uint128{High: 0, Low: 3},
		},
		inRequest: &spb.FlushRequest{
			Election: &spb.FlushRequest_Id{
				Id: &spb.Uint128{High: 0, Low: 3},
			},
		},
		wantErrCode: codes.InvalidArgument,
		wantErrDetails: &spb.FlushResponseError{
			Status: spb.FlushResponseError_UNSPECIFIED_NETWORK_INSTANCE,
		},
	}, {
		desc: "election unspecified, but server is in SINGLE_PRIMARY",
		inServer: &Server{
			curElecID: &spb.Uint128{High: 1, Low: 1},
		},
		inRequest: &spb.FlushRequest{
			NetworkInstance: &spb.FlushRequest_Name{
				Name: DefaultNetworkInstanceName,
			},
		},
		wantErrCode: codes.FailedPrecondition,
		wantErrDetails: &spb.FlushResponseError{
			Status: spb.FlushResponseError_UNSPECIFIED_ELECTION_BEHAVIOR,
		},
	}, {
		desc:     "server is ALL_PRIMARY, but election ID is specified",
		inServer: &Server{},
		inRequest: &spb.FlushRequest{
			Election: &spb.FlushRequest_Id{
				Id: &spb.Uint128{High: 42, Low: 42},
			},
			NetworkInstance: &spb.FlushRequest_Name{
				Name: DefaultNetworkInstanceName,
			},
		},
		wantErrCode: codes.FailedPrecondition,
		wantErrDetails: &spb.FlushResponseError{
			Status: spb.FlushResponseError_ELECTION_ID_IN_ALL_PRIMARY,
		},
	}, {
		desc: "zero election ID specified",
		inServer: &Server{
			curElecID: &spb.Uint128{High: 1, Low: 1},
		},
		inRequest: &spb.FlushRequest{
			Election: &spb.FlushRequest_Id{
				Id: &spb.Uint128{High: 0, Low: 0},
			},
			NetworkInstance: &spb.FlushRequest_Name{
				Name: DefaultNetworkInstanceName,
			},
		},
		wantErrCode: codes.InvalidArgument,
		wantErrDetails: &spb.FlushResponseError{
			Status: spb.FlushResponseError_INVALID_ELECTION_ID,
		},
	}, {
		desc: "specified ID is not master",
		inServer: &Server{
			curElecID: &spb.Uint128{High: 0, Low: 2},
		},
		inRequest: &spb.FlushRequest{
			Election: &spb.FlushRequest_Id{
				Id: &spb.Uint128{High: 0, Low: 1},
			},
			NetworkInstance: &spb.FlushRequest_Name{
				Name: DefaultNetworkInstanceName,
			},
		},
		wantErrCode: codes.FailedPrecondition,
		wantErrDetails: &spb.FlushResponseError{
			Status: spb.FlushResponseError_NOT_PRIMARY,
		},
	}, {
		desc: "specified ID is OK - equal",
		inServer: &Server{
			curElecID: &spb.Uint128{High: 0, Low: 3},
		},
		inRequest: &spb.FlushRequest{
			Election: &spb.FlushRequest_Id{
				Id: &spb.Uint128{High: 0, Low: 3},
			},
			NetworkInstance: &spb.FlushRequest_Name{
				Name: DefaultNetworkInstanceName,
			},
		},
	}, {
		desc: "specified ID is OK - greater than",
		inServer: &Server{
			curElecID: &spb.Uint128{High: 0, Low: 3},
		},
		inRequest: &spb.FlushRequest{
			Election: &spb.FlushRequest_Id{
				Id: &spb.Uint128{High: 0, Low: 4},
			},
			NetworkInstance: &spb.FlushRequest_Name{
				Name: DefaultNetworkInstanceName,
			},
		},
	}, {
		desc: "override specified",
		inServer: &Server{
			curElecID: &spb.Uint128{High: 0, Low: 3},
		},
		inRequest: &spb.FlushRequest{
			Election: &spb.FlushRequest_Override{
				Override: &spb.Empty{},
			},
			NetworkInstance: &spb.FlushRequest_Name{
				Name: DefaultNetworkInstanceName,
			},
		},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			err := tt.inServer.checkFlushRequest(tt.inRequest)
			if err == nil && tt.wantErrCode != codes.OK {
				t.Fatalf("got unexpected nil error, want: (code: %s, details: %v)", tt.wantErrCode, tt.wantErrDetails)
			}

			gotErr, ok := status.FromError(err)
			if !ok {
				t.Fatalf("got unexpected non-status.Status error, got: %T, want: status.Status", err)
			}

			if got, want := gotErr.Code(), tt.wantErrCode; got != want {
				t.Fatalf("did not get expected error code, got: %s, want: %s", gotErr.Proto(), want)
			}

			if tt.wantErrDetails != nil {
				gotDets := gotErr.Details()
				if len(gotDets) != 1 {
					t.Fatalf("did not get error details, got: %v, want: 1 FlushErrorDetails", gotDets)
				}
				gotD, ok := gotDets[0].(*spb.FlushResponseError)
				if !ok {
					t.Fatalf("did not get expected details type, got: %T, want: *spb.FlushResponseError", gotDets[0])
				}
				if got, want := gotD, tt.wantErrDetails; !proto.Equal(got, want) {
					t.Fatalf("did not get expected error details, got: %s, want: %s", prototext.Format(got), prototext.Format(want))
				}
			}
		})
	}
}

func TestFlush(t *testing.T) {
	addEntry := func(r *rib.RIB, ni string) {
		if oks, _, err := r.AddEntry(ni, &spb.AFTOperation{
			Op: spb.AFTOperation_ADD,
			Entry: &spb.AFTOperation_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index:   1,
					NextHop: &aftpb.Afts_NextHop{},
				},
			},
		}); err != nil || len(oks) != 1 {
			t.Fatalf("cannot add NextHop to server, %v", err)
		}

		if oks, _, err := r.AddEntry(ni, &spb.AFTOperation{
			Op: spb.AFTOperation_ADD,
			Entry: &spb.AFTOperation_NextHopGroup{
				NextHopGroup: &aftpb.Afts_NextHopGroupKey{
					Id: 1,
					NextHopGroup: &aftpb.Afts_NextHopGroup{
						NextHop: []*aftpb.Afts_NextHopGroup_NextHopKey{{
							Index:   1,
							NextHop: &aftpb.Afts_NextHopGroup_NextHop{},
						}},
					},
				},
			},
		}); err != nil || len(oks) != 1 {
			t.Fatalf("cannot add NextHopGroup to server, %v", err)
		}

		if oks, _, err := r.AddEntry(ni, &spb.AFTOperation{
			Op: spb.AFTOperation_ADD,
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix: "1.1.1.1/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{
						NextHopGroup: &wpb.UintValue{Value: 1},
					},
				},
			},
		}); err != nil || len(oks) != 1 {
			t.Fatalf("cannot add IPv4Entry to server, %v", err)
		}
	}

	// singleNI creates a server with the default network instance with one entry.
	singleNI := func() *Server {
		s, err := NewFake()
		if err != nil {
			t.Fatalf("cannot create server, error: %v", err)
		}
		r := rib.New(DefaultNetworkInstanceName)
		addEntry(r, DefaultNetworkInstanceName)
		s.InjectRIB(r)
		return s.Server
	}

	// singleNIWithElection creates a server with a default network instance with
	// one entry, and sets the specified election ID.
	singleNIWithElection := func(id *spb.Uint128) *Server {
		s := singleNI()
		s.curElecID = id
		return s
	}

	// multiNI creates a server with a default network instance along with the
	// other network instances specified, it contains one entry per network
	// instance.
	multiNI := func(names []string) *Server {
		s, err := NewFake(WithVRFs(names))
		if err != nil {
			t.Fatalf("cannot create server, error: %v", err)
		}
		addEntry(s.masterRIB, DefaultNetworkInstanceName)
		for _, n := range names {
			addEntry(s.masterRIB, n)
		}

		return s.Server
	}

	// multiNI creates a server with a default network instance along with the
	// other network instances specified. It also set the election ID.
	// Each network instance contains one entry.
	multiNIWithElection := func(names []string, id *spb.Uint128) *Server {
		s := multiNI(names)
		s.curElecID = id
		return s
	}

	tests := []struct {
		desc            string
		inServer        *Server
		inReq           *spb.FlushRequest
		wantErrCode     codes.Code
		wantResult      spb.FlushResponse_Result
		wantEntriesInNI map[string]int
	}{{
		desc:        "error, nil request received",
		inServer:    multiNI([]string{"two"}),
		wantErrCode: codes.Internal,
		wantEntriesInNI: map[string]int{
			DefaultNetworkInstanceName: 3,
			"two":                      3,
		},
	}, {
		desc:        "error, empty request received",
		inServer:    multiNI([]string{"two"}),
		inReq:       &spb.FlushRequest{},
		wantErrCode: codes.InvalidArgument,
		wantEntriesInNI: map[string]int{
			DefaultNetworkInstanceName: 3,
			"two":                      3,
		},
	}, {
		desc:     "error, network instance is not specified",
		inServer: singleNIWithElection(&spb.Uint128{High: 0, Low: 1}),
		inReq: &spb.FlushRequest{
			Election: &spb.FlushRequest_Id{
				Id: &spb.Uint128{High: 0, Low: 1},
			},
		},
		wantErrCode: codes.InvalidArgument,
		wantEntriesInNI: map[string]int{
			DefaultNetworkInstanceName: 3,
		},
	}, {
		desc:     "error, network instance does not exist",
		inServer: singleNI(),
		inReq: &spb.FlushRequest{
			NetworkInstance: &spb.FlushRequest_Name{
				Name: "does-not-exist",
			},
		},
		wantErrCode: codes.InvalidArgument,
		wantEntriesInNI: map[string]int{
			DefaultNetworkInstanceName: 3,
		},
	}, {
		desc:     "error, election ID specified but not master",
		inServer: singleNIWithElection(&spb.Uint128{High: 0, Low: 2}),
		inReq: &spb.FlushRequest{
			NetworkInstance: &spb.FlushRequest_All{
				All: &spb.Empty{},
			},
			Election: &spb.FlushRequest_Id{
				Id: &spb.Uint128{High: 0, Low: 1},
			},
		},
		wantEntriesInNI: map[string]int{
			DefaultNetworkInstanceName: 3,
		},
		wantErrCode: codes.FailedPrecondition,
	}, {
		desc:     "error, election ID is specified in ALL_PRIMARY mode",
		inServer: singleNI(),
		inReq: &spb.FlushRequest{
			Election: &spb.FlushRequest_Id{
				Id: &spb.Uint128{High: 1, Low: 1},
			},
			NetworkInstance: &spb.FlushRequest_Name{
				Name: "does-not-exist",
			},
		},
		wantErrCode: codes.FailedPrecondition,
		wantEntriesInNI: map[string]int{
			DefaultNetworkInstanceName: 3,
		},
	}, {
		desc:     "success, flush the default NI",
		inServer: singleNI(),
		inReq: &spb.FlushRequest{
			NetworkInstance: &spb.FlushRequest_Name{
				Name: DefaultNetworkInstanceName,
			},
		},
		wantResult: spb.FlushResponse_OK,
		wantEntriesInNI: map[string]int{
			DefaultNetworkInstanceName: 0,
		},
	}, {
		desc:     "success, flush a non default NI",
		inServer: multiNI([]string{"two"}),
		inReq: &spb.FlushRequest{
			NetworkInstance: &spb.FlushRequest_Name{
				Name: "two",
			},
		},
		wantResult: spb.FlushResponse_OK,
		wantEntriesInNI: map[string]int{
			DefaultNetworkInstanceName: 3,
			"two":                      0,
		},
	}, {
		desc:     "success, flush all NIs",
		inServer: multiNI([]string{"two"}),
		inReq: &spb.FlushRequest{
			NetworkInstance: &spb.FlushRequest_All{
				All: &spb.Empty{},
			},
		},
		wantResult: spb.FlushResponse_OK,
		wantEntriesInNI: map[string]int{
			DefaultNetworkInstanceName: 0,
			"two":                      0,
		},
	}, {
		desc:     "success, flush all NIs with a valid election ID",
		inServer: multiNIWithElection([]string{"two"}, &spb.Uint128{High: 0, Low: 1}),
		inReq: &spb.FlushRequest{
			NetworkInstance: &spb.FlushRequest_All{
				All: &spb.Empty{},
			},
			Election: &spb.FlushRequest_Id{
				Id: &spb.Uint128{High: 0, Low: 1},
			},
		},
		wantResult: spb.FlushResponse_OK,
		wantEntriesInNI: map[string]int{
			DefaultNetworkInstanceName: 0,
			"two":                      0,
		},
	}, {
		desc:     "success, election ID specified with override",
		inServer: singleNIWithElection(&spb.Uint128{High: 0, Low: 42}),
		inReq: &spb.FlushRequest{
			NetworkInstance: &spb.FlushRequest_All{
				All: &spb.Empty{},
			},
			Election: &spb.FlushRequest_Override{
				Override: &spb.Empty{},
			},
		},
		wantResult: spb.FlushResponse_OK,
		wantEntriesInNI: map[string]int{
			DefaultNetworkInstanceName: 0,
		},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			resp, err := tt.inServer.Flush(context.Background(), tt.inReq)
			if err != nil {
				s, ok := status.FromError(err)
				if !ok {
					t.Fatalf("did not get status.Status as error, got: %T %v", err, err)
				}
				if got, want := s.Code(), tt.wantErrCode; got != want {
					t.Fatalf("did not get expected error code, got: %s, want: %s", got, want)
				}
				return
			}
			if tt.wantResult != resp.GetResult() {
				t.Fatalf("got unexpected result, got: %v, want: %v", resp.Result.String(), tt.wantResult.String())
				return
			}

			for ni, wantEntries := range tt.wantEntriesInNI {
				r, ok := tt.inServer.masterRIB.NetworkInstanceRIB(ni)
				if !ok {
					t.Fatalf("cannot find RIB %s on server", ni)
				}

				msgCh := make(chan *spb.GetResponse)
				stopCh := make(chan struct{})

				doneCh := make(chan struct{})
				defer close(doneCh)

				got := []*spb.GetResponse{}
				go func() {
					for {
						select {
						case r := <-msgCh:
							got = append(got, r)
						case <-doneCh:
							return
						}
					}
				}()

				if err := r.GetRIB(map[spb.AFTType]bool{
					spb.AFTType_ALL: true,
				}, msgCh, stopCh); err != nil {
					t.Fatalf("could not perform Get on RIB, %v", err)
				}
				doneCh <- struct{}{}

				if len(got) != wantEntries {
					t.Fatalf("got unexpected entries in NI %s, got: %d entries, want: %d, contents:\n%+v", ni, len(got), wantEntries, got)
				}
			}
		})
	}
}

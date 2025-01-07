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

package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/openconfig/gribigo/constants"
	"github.com/openconfig/gribigo/rib"
	"github.com/openconfig/gribigo/server"
	"github.com/openconfig/gribigo/testcommon"
	"go.uber.org/atomic"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/testing/protocmp"

	aftpb "github.com/openconfig/gribi/v1/proto/gribi_aft"
	spb "github.com/openconfig/gribi/v1/proto/service"
)

func TestMain(m *testing.M) {
	debug = true
	defer func() { debug = false }()
	os.Exit(m.Run())
}

func TestHandleParams(t *testing.T) {
	tests := []struct {
		desc      string
		inOpts    []Opt
		wantState *clientState
		wantErr   bool
	}{{
		desc:      "client with default parameters",
		inOpts:    nil,
		wantState: &clientState{},
	}, {
		desc: "ALL_PRIMARY client",
		inOpts: []Opt{
			AllPrimaryClients(),
		},
		wantState: &clientState{
			SessParams: &spb.SessionParameters{
				Redundancy: spb.SessionParameters_ALL_PRIMARY,
			},
		},
	}, {
		desc: "SINGLE_PRIMARY client",
		inOpts: []Opt{
			ElectedPrimaryClient(&spb.Uint128{High: 0, Low: 1}),
		},
		wantState: &clientState{
			SessParams: &spb.SessionParameters{
				Redundancy: spb.SessionParameters_SINGLE_PRIMARY,
			},
			ElectionID: &spb.Uint128{High: 0, Low: 1},
		},
	}, {
		desc: "SINGLE_PRIMARY and ALL_PRIMARY both included",
		inOpts: []Opt{
			ElectedPrimaryClient(&spb.Uint128{High: 0, Low: 1}),
			AllPrimaryClients(),
		},
		wantErr: true,
	}, {
		desc: "Persistence requested",
		inOpts: []Opt{
			PersistEntries(),
		},
		wantState: &clientState{
			SessParams: &spb.SessionParameters{
				Persistence: spb.SessionParameters_PRESERVE,
			},
		},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got, err := handleParams(tt.inOpts...)
			if (err != nil) != tt.wantErr {
				t.Fatalf("did not get expected error, wanted error? %v got error: %v", tt.wantErr, err)
			}
			if diff := cmp.Diff(tt.wantState, got, protocmp.Transform()); diff != "" {
				t.Fatalf("did not get expected state, diff(-want,+got):\n%s", diff)
			}
		})
	}
}

func TestReplaceStub(t *testing.T) {
	tests := []struct {
		desc      string
		inSending bool
		wantErr   bool
	}{
		{
			desc:      "sending",
			inSending: true,
			wantErr:   true,
		},
		{
			desc: "not-sending",
		},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			c, err := New()
			if err != nil {
				t.Fatalf("cannot create client, %v", err)
			}

			c.StartSending()
			if !tt.inSending {
				c.StopSending()
			}

			if err := c.ReplaceStub(nil); err != nil {
				if tt.wantErr {
					return
				}
				t.Errorf("ReplaceStub(...) returned unexpected err: %v", err)
			}
		})
	}
}

func TestQ(t *testing.T) {
	tests := []struct {
		desc      string
		inReqs    []*spb.ModifyRequest
		inSending bool
		wantQ     []*spb.ModifyRequest
	}{{
		desc: "single enqueued input",
		inReqs: []*spb.ModifyRequest{{
			ElectionId: &spb.Uint128{
				Low:  1,
				High: 1,
			},
		}},
		wantQ: []*spb.ModifyRequest{{
			ElectionId: &spb.Uint128{
				Low:  1,
				High: 1,
			},
		}},
	}, {
		desc: "multiple enqueued input",
		inReqs: []*spb.ModifyRequest{{
			ElectionId: &spb.Uint128{
				Low:  1,
				High: 1,
			},
		}, {
			ElectionId: &spb.Uint128{
				Low:  2,
				High: 2,
			},
		}},
		wantQ: []*spb.ModifyRequest{{
			ElectionId: &spb.Uint128{
				Low:  1,
				High: 1,
			},
		}, {
			ElectionId: &spb.Uint128{
				Low:  2,
				High: 2,
			},
		}},
	}, {
		desc: "enqueue whilst sending",
		inReqs: []*spb.ModifyRequest{{
			ElectionId: &spb.Uint128{
				Low:  1,
				High: 1,
			},
		}},
		inSending: true,
		wantQ:     []*spb.ModifyRequest{},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			c, err := New()
			if err != nil {
				t.Fatalf("cannot create client, %v", err)
			}
			doneCh := make(chan struct{})
			if tt.inSending {
				c.qs.sending = atomic.NewBool(tt.inSending)
				// avoid test deadlock by emptying the queue if we're sending.
				go func() {
					for {
						select {
						case <-c.qs.modifyCh:
						case <-doneCh:
							return
						}
					}
				}()
			}

			for _, r := range tt.inReqs {
				c.Q(r)
			}
			if diff := cmp.Diff(c.qs.sendq, tt.wantQ, protocmp.Transform()); diff != "" {
				t.Fatalf("did not get expected send queue, %s", diff)
			}
			close(doneCh)
		})
	}
}

func TestPending(t *testing.T) {
	tests := []struct {
		desc     string
		inClient *Client
		want     []PendingRequest
		wantErr  bool
	}{{
		desc: "empty queue",
		inClient: &Client{
			qs: &clientQs{
				pendq: &pendingQueue{},
			},
		},
		want: []PendingRequest{},
	}, {
		desc: "populated operations queue",
		inClient: &Client{
			qs: &clientQs{
				pendq: &pendingQueue{
					Ops: map[uint64]*PendingOp{
						1:  {Timestamp: 1, Op: &spb.AFTOperation{Id: 1}},
						42: {Timestamp: 42, Op: &spb.AFTOperation{Id: 42}},
						84: {Timestamp: 84, Op: &spb.AFTOperation{Id: 84}},
					},
				},
			},
		},
		want: []PendingRequest{
			&PendingOp{Timestamp: 1, Op: &spb.AFTOperation{Id: 1}},
			&PendingOp{Timestamp: 42, Op: &spb.AFTOperation{Id: 42}},
			&PendingOp{Timestamp: 84, Op: &spb.AFTOperation{Id: 84}},
		},
	}, {
		desc: "populated election ID",
		inClient: &Client{
			qs: &clientQs{
				pendq: &pendingQueue{
					Election: &ElectionReqDetails{
						Timestamp: 21,
						ID:        &spb.Uint128{High: 1, Low: 1},
					},
				},
			},
		},
		want: []PendingRequest{
			&ElectionReqDetails{
				Timestamp: 21,
				ID:        &spb.Uint128{High: 1, Low: 1},
			},
		},
	}, {
		desc: "populated session parameters",
		inClient: &Client{
			qs: &clientQs{
				pendq: &pendingQueue{
					SessionParams: &SessionParamReqDetails{
						Timestamp: 42,
						Outgoing: &spb.SessionParameters{
							AckType: spb.SessionParameters_RIB_AND_FIB_ACK,
						},
					},
				},
			},
		},
		want: []PendingRequest{
			&SessionParamReqDetails{
				Timestamp: 42,
				Outgoing: &spb.SessionParameters{
					AckType: spb.SessionParameters_RIB_AND_FIB_ACK,
				},
			},
		},
	}, {
		desc: "invalid operation in Ops queue",
		inClient: &Client{
			qs: &clientQs{
				pendq: &pendingQueue{
					Ops: map[uint64]*PendingOp{
						0: {Timestamp: 42},
					},
				},
			},
		},
		wantErr: true,
	}, {
		desc: "all queues populated",
		inClient: &Client{
			qs: &clientQs{
				pendq: &pendingQueue{
					Election:      &ElectionReqDetails{Timestamp: 0},
					SessionParams: &SessionParamReqDetails{Timestamp: 1},
					Ops: map[uint64]*PendingOp{
						0: {Timestamp: 3, Op: &spb.AFTOperation{Id: 42}},
					},
				},
			},
		},
		want: []PendingRequest{
			&PendingOp{Timestamp: 3, Op: &spb.AFTOperation{Id: 42}},
			&ElectionReqDetails{Timestamp: 0},
			&SessionParamReqDetails{Timestamp: 1},
		},
	}, {
		desc:     "nil queues",
		inClient: &Client{},
		wantErr:  true,
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got, err := tt.inClient.Pending()
			if (err != nil) != tt.wantErr {
				t.Fatalf("did not get expected error, got: %v, wantErr? %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(got, tt.want, protocmp.Transform()); diff != "" {
				t.Fatalf("did not get expected queue, diff(-got,+want):\n%s", diff)
			}
		})
	}
}

func TestResults(t *testing.T) {
	tests := []struct {
		desc     string
		inClient *Client
		want     []*OpResult
		wantErr  bool
	}{{
		desc: "empty queue",
		inClient: &Client{
			qs: &clientQs{
				resultq: []*OpResult{},
			},
		},
		want: []*OpResult{},
	}, {
		desc: "populated queue",
		inClient: &Client{
			qs: &clientQs{
				resultq: []*OpResult{{
					CurrentServerElectionID: &spb.Uint128{
						Low:  0,
						High: 1,
					},
				}},
			},
		},
		want: []*OpResult{{
			CurrentServerElectionID: &spb.Uint128{
				Low:  0,
				High: 1,
			},
		}},
	}, {
		desc:     "nil queues",
		inClient: &Client{},
		wantErr:  true,
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got, err := tt.inClient.Results()
			if (err != nil) != tt.wantErr {
				t.Fatalf("did not get expected error, got: %v, wantErr? %v", err, tt.wantErr)
			}
			if !cmp.Equal(got, tt.want, protocmp.Transform()) {
				t.Fatalf("did not get expected queue, got: %v, want: %v", got, tt.want)
			}
		})
	}
}

func TestStatus(t *testing.T) {
	// overload unixTS so that it always returns 42.
	unixTS = func() int64 { return 42 }

	tests := []struct {
		desc       string
		inClient   *Client
		wantStatus *ClientStatus
		wantErr    bool
	}{{
		desc: "empty queues",
		inClient: &Client{
			qs: &clientQs{
				pendq:   &pendingQueue{},
				resultq: []*OpResult{},
			},
		},
		wantStatus: &ClientStatus{
			Timestamp:           42,
			PendingTransactions: []PendingRequest{},
			Results:             []*OpResult{},
			SendErrs:            []error{},
			ReadErrs:            []error{},
		},
	}, {
		desc: "populated queues",
		inClient: &Client{
			qs: &clientQs{
				pendq: &pendingQueue{
					Ops: map[uint64]*PendingOp{
						0: {Timestamp: 1, Op: &spb.AFTOperation{Id: 0}},
					},
					Election:      &ElectionReqDetails{Timestamp: 2},
					SessionParams: &SessionParamReqDetails{Timestamp: 3},
				},
				resultq: []*OpResult{{
					Timestamp: 50,
				}},
			},
		},
		wantStatus: &ClientStatus{
			Timestamp: 42,
			PendingTransactions: []PendingRequest{
				&PendingOp{Timestamp: 1, Op: &spb.AFTOperation{Id: 0}},
				&ElectionReqDetails{Timestamp: 2},
				&SessionParamReqDetails{Timestamp: 3},
			},
			Results: []*OpResult{{
				Timestamp: 50,
			}},
			SendErrs: []error{},
			ReadErrs: []error{},
		},
	}, {
		desc:     "erroneous queues",
		inClient: &Client{},
		wantErr:  true,
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got, err := tt.inClient.Status()
			if (err != nil) != tt.wantErr {
				t.Fatalf("did not get expected error, got: %v, wantErr? %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(got, tt.wantStatus,
				protocmp.Transform(),
				cmpopts.IgnoreFields(ClientStatus{}, "SendErrs", "ReadErrs"),
				cmpopts.EquateEmpty()); diff != "" {
				t.Fatalf("did not get expected status, diff(-got,+want):\n%s", diff)
			}
		})
	}
}

func TestHandleModifyResponse(t *testing.T) {
	unixTS = func() int64 { return 42 }

	tests := []struct {
		desc        string
		inClient    *Client
		inResponse  *spb.ModifyResponse
		wantResults []*OpResult
		wantErr     bool
	}{{
		desc: "invalid combination of fields populated",
		inClient: &Client{
			qs: &clientQs{
				sending: &atomic.Bool{},
			},
		},
		inResponse: &spb.ModifyResponse{
			Result: []*spb.AFTResult{{
				Id: 42,
			}},
			ElectionId: &spb.Uint128{High: 42, Low: 0},
		},
		wantErr: true,
	}, {
		desc: "election ID populated",
		inClient: &Client{
			qs: &clientQs{
				pendq: &pendingQueue{
					Election: &ElectionReqDetails{
						Timestamp: 2,
					},
				},
				sending: &atomic.Bool{},
			},
		},
		inResponse: &spb.ModifyResponse{
			ElectionId: &spb.Uint128{High: 0, Low: 42},
		},
		wantResults: []*OpResult{{
			Timestamp:               42,
			Latency:                 40,
			CurrentServerElectionID: &spb.Uint128{High: 0, Low: 42},
		}},
	}, {
		desc: "invalid ModifyResponse",
		inClient: &Client{
			qs: &clientQs{
				pendq:   &pendingQueue{},
				sending: &atomic.Bool{},
			},
		},
		wantErr: true,
	}, {
		desc: "no populated election ID",
		inClient: &Client{
			qs: &clientQs{
				pendq:   &pendingQueue{},
				sending: &atomic.Bool{},
			},
		},
		inResponse: &spb.ModifyResponse{
			ElectionId: &spb.Uint128{Low: 1},
		},
		wantResults: []*OpResult{{
			Timestamp:               42,
			CurrentServerElectionID: &spb.Uint128{Low: 1},
			ClientError:             "received a election update when there was none pending",
		}},
	}, {
		desc: "session parameters populated",
		inClient: &Client{
			qs: &clientQs{
				pendq: &pendingQueue{
					SessionParams: &SessionParamReqDetails{
						Timestamp: 20,
					},
				},
				sending: &atomic.Bool{},
			},
		},
		inResponse: &spb.ModifyResponse{
			SessionParamsResult: &spb.SessionParametersResult{
				Status: spb.SessionParametersResult_OK,
			},
		},
		wantResults: []*OpResult{{
			Timestamp: 42,
			Latency:   22,
			SessionParameters: &spb.SessionParametersResult{
				Status: spb.SessionParametersResult_OK,
			},
		}},
	}, {
		desc: "session parameters received but not pending",
		inClient: &Client{
			qs: &clientQs{
				pendq:   &pendingQueue{},
				sending: &atomic.Bool{},
			},
		},
		inResponse: &spb.ModifyResponse{
			SessionParamsResult: &spb.SessionParametersResult{
				Status: spb.SessionParametersResult_OK,
			},
		},
		wantResults: []*OpResult{{
			Timestamp: 42,
			SessionParameters: &spb.SessionParametersResult{
				Status: spb.SessionParametersResult_OK,
			},
			ClientError: "received a session parameter result when there was none pending",
		}},
	}, {
		desc: "AckType set to FIB_ACK, receive AFTResult_RIB_PROGRAMMED and AFTResult_FIB_PROGRAMMED ",
		inClient: &Client{
			qs: &clientQs{
				pendq: &pendingQueue{
					Ops: map[uint64]*PendingOp{
						1: {
							Timestamp: 42,
							Op:        &spb.AFTOperation{Id: 1},
						},
					},
				},
				sending: &atomic.Bool{},
			},
			state: &clientState{
				SessParams: &spb.SessionParameters{
					AckType: spb.SessionParameters_RIB_AND_FIB_ACK,
				},
			},
		},
		inResponse: &spb.ModifyResponse{
			Result: []*spb.AFTResult{
				{
					Id:     1,
					Status: spb.AFTResult_RIB_PROGRAMMED,
				}, {
					Id:     1,
					Status: spb.AFTResult_FIB_PROGRAMMED,
				}},
		},
		wantResults: []*OpResult{{
			Timestamp:         42,
			OperationID:       1,
			ProgrammingResult: spb.AFTResult_RIB_PROGRAMMED,
			Details:           &OpDetailsResults{},
		}, {
			Timestamp:         42,
			OperationID:       1,
			ProgrammingResult: spb.AFTResult_FIB_PROGRAMMED,
			Details:           &OpDetailsResults{},
		}},
	}, {
		desc: "AckType set to FIB_ACK, receive Ops#1. AFTResult_FAILED Ops#2. AFTResult_RIB_PROGRAMMED and AFTResult_FIB_FAILED ",
		inClient: &Client{
			qs: &clientQs{
				pendq: &pendingQueue{
					Ops: map[uint64]*PendingOp{
						1: {
							Timestamp: 42,
							Op:        &spb.AFTOperation{Id: 1},
						},
						2: {
							Timestamp: 42,
							Op:        &spb.AFTOperation{Id: 2},
						},
					},
				},
				sending: &atomic.Bool{},
			},
			state: &clientState{
				SessParams: &spb.SessionParameters{
					AckType: spb.SessionParameters_RIB_AND_FIB_ACK,
				},
			},
		},
		inResponse: &spb.ModifyResponse{
			Result: []*spb.AFTResult{
				{
					Id:     1,
					Status: spb.AFTResult_FAILED,
				}, {
					Id:     2,
					Status: spb.AFTResult_RIB_PROGRAMMED,
				}, {
					Id:     2,
					Status: spb.AFTResult_FIB_FAILED,
				}},
		},
		wantResults: []*OpResult{{
			Timestamp:         42,
			OperationID:       1,
			ProgrammingResult: spb.AFTResult_FAILED,
			Details:           &OpDetailsResults{},
		}, {
			Timestamp:         42,
			OperationID:       2,
			ProgrammingResult: spb.AFTResult_RIB_PROGRAMMED,
			Details:           &OpDetailsResults{},
		}, {
			Timestamp:         42,
			OperationID:       2,
			ProgrammingResult: spb.AFTResult_FIB_FAILED,
			Details:           &OpDetailsResults{},
		}},
	}, {
		desc: "AckType set to RIB_ACK, receive AFTResult_RIB_PROGRAMMED",
		inClient: &Client{
			qs: &clientQs{
				pendq: &pendingQueue{
					Ops: map[uint64]*PendingOp{
						1: {
							Timestamp: 42,
							Op:        &spb.AFTOperation{Id: 1},
						},
					},
				},
				sending: &atomic.Bool{},
			},
			state: &clientState{
				SessParams: &spb.SessionParameters{
					AckType: spb.SessionParameters_RIB_ACK,
				},
			},
		},
		inResponse: &spb.ModifyResponse{
			Result: []*spb.AFTResult{
				{
					Id:     1,
					Status: spb.AFTResult_RIB_PROGRAMMED,
				}},
		},
		wantResults: []*OpResult{{
			Timestamp:         42,
			OperationID:       1,
			ProgrammingResult: spb.AFTResult_RIB_PROGRAMMED,
			Details:           &OpDetailsResults{},
		}},
	}, {
		desc: "AckType set to RIB_ACK, receive AFTResult_FAILED",
		inClient: &Client{
			qs: &clientQs{
				pendq: &pendingQueue{
					Ops: map[uint64]*PendingOp{
						1: {
							Timestamp: 42,
							Op:        &spb.AFTOperation{Id: 1},
						},
					},
				},
				sending: &atomic.Bool{},
			},
			state: &clientState{
				SessParams: &spb.SessionParameters{
					AckType: spb.SessionParameters_RIB_ACK,
				},
			},
		},
		inResponse: &spb.ModifyResponse{
			Result: []*spb.AFTResult{
				{
					Id:     1,
					Status: spb.AFTResult_FAILED,
				}},
		},
		wantResults: []*OpResult{{
			Timestamp:         42,
			OperationID:       1,
			ProgrammingResult: spb.AFTResult_FAILED,
			Details:           &OpDetailsResults{},
		}},
	}, {
		desc: "AckType RIB_AND_FIB_ACK, receive AFTResult_FIB_PROGRAMMED before AFTResult_RIB_PROGRAMMED",
		inClient: &Client{
			qs: &clientQs{
				pendq: &pendingQueue{
					Ops: map[uint64]*PendingOp{
						1: {
							Timestamp: 42,
							Op:        &spb.AFTOperation{Id: 1},
						},
					},
				},
				sending: &atomic.Bool{},
			},
			state: &clientState{
				SessParams: &spb.SessionParameters{
					AckType: spb.SessionParameters_RIB_AND_FIB_ACK,
				},
			},
		},
		inResponse: &spb.ModifyResponse{
			Result: []*spb.AFTResult{
				{
					Id:     1,
					Status: spb.AFTResult_FIB_PROGRAMMED,
				}, {
					Id:     1,
					Status: spb.AFTResult_RIB_PROGRAMMED,
				}},
		},
		wantResults: []*OpResult{{
			Timestamp:         42,
			OperationID:       1,
			ProgrammingResult: spb.AFTResult_FIB_PROGRAMMED,
			Details:           &OpDetailsResults{},
		}, {
			Timestamp:         42,
			OperationID:       1,
			ProgrammingResult: spb.AFTResult_RIB_PROGRAMMED,
		}},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			c := tt.inClient

			err := c.handleModifyResponse(tt.inResponse)
			if (err != nil) != tt.wantErr {
				t.Fatalf("did not get expected error status, got: %v, wantErr: %v?", err, tt.wantErr)
			}
			if err != nil {
				return
			}

			if diff := cmp.Diff(
				c.qs.resultq, tt.wantResults,
				protocmp.Transform(),
				cmpopts.EquateEmpty(),
				cmpopts.EquateErrors()); diff != "" {
				t.Fatalf("did not get expected result queue, diff(-got,+want):\n%s", diff)
			}
		})
	}
}

func TestHandleModifyRequest(t *testing.T) {
	// overload unix timestamp function to ensure output is deterministic.
	unixTS = func() int64 { return 42 }

	tests := []struct {
		desc        string
		inRequest   *spb.ModifyRequest
		inClient    *Client
		wantPending *pendingQueue
		wantErr     bool
	}{{
		desc: "valid input",
		inRequest: &spb.ModifyRequest{
			Operation: []*spb.AFTOperation{{
				Id: 1,
			}},
		},
		inClient: func() *Client { c, _ := New(); return c }(),
		wantPending: &pendingQueue{
			Ops: map[uint64]*PendingOp{
				1: {
					Timestamp: 42,
					Op:        &spb.AFTOperation{Id: 1},
				},
			},
		},
	}, {
		desc: "clashing transaction IDs",
		inRequest: &spb.ModifyRequest{
			Operation: []*spb.AFTOperation{{
				Id: 128,
			}},
		},
		inClient: &Client{
			qs: &clientQs{
				pendq: &pendingQueue{
					Ops: map[uint64]*PendingOp{
						128: {},
					},
				},
				sending: &atomic.Bool{},
			},
		},
		wantPending: &pendingQueue{
			Ops: map[uint64]*PendingOp{
				128: {},
			},
		},
		wantErr: true,
	}, {
		desc: "election ID update",
		inClient: &Client{
			qs: &clientQs{
				pendq:   &pendingQueue{},
				sending: &atomic.Bool{},
			},
		},
		inRequest: &spb.ModifyRequest{ElectionId: &spb.Uint128{Low: 1}},
		wantPending: &pendingQueue{
			Election: &ElectionReqDetails{
				Timestamp: 42,
				ID:        &spb.Uint128{Low: 1},
			},
		},
	}, {
		desc: "session params update",
		inClient: &Client{
			qs: &clientQs{
				pendq:   &pendingQueue{},
				sending: &atomic.Bool{},
			},
		},
		inRequest: &spb.ModifyRequest{
			Params: &spb.SessionParameters{
				AckType: spb.SessionParameters_RIB_AND_FIB_ACK,
			},
		},
		wantPending: &pendingQueue{
			SessionParams: &SessionParamReqDetails{
				Timestamp: 42,
				Outgoing: &spb.SessionParameters{
					AckType: spb.SessionParameters_RIB_AND_FIB_ACK,
				},
			},
		},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			if err := tt.inClient.handleModifyRequest(tt.inRequest); (err != nil) != tt.wantErr {
				t.Fatalf("did not get expected error, got: %v, wantErr? %v", err, tt.wantErr)
			}

			if diff := cmp.Diff(tt.inClient.qs.pendq, tt.wantPending, protocmp.Transform(), cmpopts.EquateEmpty()); diff != "" {
				t.Fatalf("did not get expected pending queue, diff(-got,+want):\n%s", diff)
			}
		})
	}
}

func TestConverged(t *testing.T) {
	tests := []struct {
		desc     string
		inClient *Client
		want     bool
	}{{
		desc: "converged - uninitialised",
		inClient: &Client{
			qs: &clientQs{},
		},
		want: true,
	}, {
		desc: "converged",
		inClient: &Client{
			qs: &clientQs{
				pendq: &pendingQueue{},
			},
		},
		want: true,
	}, {
		desc: "not converged - send queued",
		inClient: &Client{
			qs: &clientQs{
				sendq: []*spb.ModifyRequest{{}},
			},
		},
	}, {
		desc: "not converged - pending queue, ops",
		inClient: &Client{
			qs: &clientQs{
				pendq: &pendingQueue{
					Ops: map[uint64]*PendingOp{
						0: {},
					},
				},
			},
		},
	}, {
		desc: "not converged - pending queue, election",
		inClient: &Client{
			qs: &clientQs{
				pendq: &pendingQueue{
					Election: &ElectionReqDetails{},
				},
			},
		},
	}, {
		desc: "not converged - pending queue, params",
		inClient: &Client{
			qs: &clientQs{
				pendq: &pendingQueue{
					SessionParams: &SessionParamReqDetails{},
				},
			},
		},
	}, {
		desc: "not converged - running",
		inClient: &Client{
			qs: &clientQs{},
		},
		want: true,
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			if got, want := tt.inClient.isConverged(), tt.want; got != want {
				t.Fatalf("did not get expected converged status, got: %v, want: %v", got, want)
			}
		})
	}

}

func TestHasErrors(t *testing.T) {
	e := errors.New("error")
	tests := []struct {
		desc        string
		inClient    *Client
		wantSendErr []error
		wantRecvErr []error
	}{{
		desc:     "no errors",
		inClient: &Client{},
	}, {
		desc: "send error",
		inClient: &Client{
			sendErr: []error{e},
		},
		wantSendErr: []error{e},
	}, {
		desc: "recv error",
		inClient: &Client{
			readErr: []error{e},
		},
		wantRecvErr: []error{e},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			gotSend, gotRecv := tt.inClient.hasErrors()
			if diff := cmp.Diff(gotSend, tt.wantSendErr, cmpopts.EquateErrors()); diff != "" {
				t.Fatalf("did not get expected send errors, diff(-got,+want):\n%s", diff)
			}
			if diff := cmp.Diff(gotRecv, tt.wantRecvErr, cmpopts.EquateErrors()); diff != "" {
				t.Fatalf("did not get expected recv errors, diff(-got,+want):\n%s", diff)
			}
		})
	}
}

func TestGet(t *testing.T) {
	tests := []struct {
		desc string
		// Operations to perform on the server before we make the request.
		inOperations []*spb.AFTOperation
		inGetRequest *spb.GetRequest
		wantResponse *spb.GetResponse
		wantErr      bool
	}{{
		desc: "empty operations",
		inGetRequest: &spb.GetRequest{
			NetworkInstance: &spb.GetRequest_Name{
				Name: server.DefaultNetworkInstanceName,
			},
			Aft: spb.AFTType_ALL,
		},
		wantResponse: &spb.GetResponse{},
	}, {
		desc: "single entry in Server - specific NI",
		inOperations: []*spb.AFTOperation{{
			NetworkInstance: server.DefaultNetworkInstanceName,
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix:    "1.1.1.1/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
				},
			},
		}},
		inGetRequest: &spb.GetRequest{
			NetworkInstance: &spb.GetRequest_Name{
				Name: server.DefaultNetworkInstanceName,
			},
			Aft: spb.AFTType_ALL,
		},
		wantResponse: &spb.GetResponse{
			Entry: []*spb.AFTEntry{{
				NetworkInstance: server.DefaultNetworkInstanceName,
				Entry: &spb.AFTEntry_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix:    "1.1.1.1/32",
						Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
					},
				},
			}},
		},
	}, {
		desc: "single IPv6 entry in Server - specific NI",
		inOperations: []*spb.AFTOperation{{
			NetworkInstance: server.DefaultNetworkInstanceName,
			Entry: &spb.AFTOperation_Ipv6{
				Ipv6: &aftpb.Afts_Ipv6EntryKey{
					Prefix:    "2001:db8::/32",
					Ipv6Entry: &aftpb.Afts_Ipv6Entry{},
				},
			},
		}},
		inGetRequest: &spb.GetRequest{
			NetworkInstance: &spb.GetRequest_Name{
				Name: server.DefaultNetworkInstanceName,
			},
			Aft: spb.AFTType_ALL,
		},
		wantResponse: &spb.GetResponse{
			Entry: []*spb.AFTEntry{{
				NetworkInstance: server.DefaultNetworkInstanceName,
				Entry: &spb.AFTEntry_Ipv6{
					Ipv6: &aftpb.Afts_Ipv6EntryKey{
						Prefix:    "2001:db8::/32",
						Ipv6Entry: &aftpb.Afts_Ipv6Entry{},
					},
				},
			}},
		},
	}, {
		desc: "single MPLS entry in Server - specific NI",
		inOperations: []*spb.AFTOperation{{
			NetworkInstance: server.DefaultNetworkInstanceName,
			Entry: &spb.AFTOperation_Mpls{
				Mpls: &aftpb.Afts_LabelEntryKey{
					Label: &aftpb.Afts_LabelEntryKey_LabelUint64{
						LabelUint64: 42,
					},
					LabelEntry: &aftpb.Afts_LabelEntry{},
				},
			},
		}},
		inGetRequest: &spb.GetRequest{
			NetworkInstance: &spb.GetRequest_Name{
				Name: server.DefaultNetworkInstanceName,
			},
			Aft: spb.AFTType_ALL,
		},
		wantResponse: &spb.GetResponse{
			Entry: []*spb.AFTEntry{{
				NetworkInstance: server.DefaultNetworkInstanceName,
				Entry: &spb.AFTEntry_Mpls{
					Mpls: &aftpb.Afts_LabelEntryKey{
						Label: &aftpb.Afts_LabelEntryKey_LabelUint64{
							LabelUint64: 42,
						},
						LabelEntry: &aftpb.Afts_LabelEntry{},
					},
				},
			}},
		},
	}, {
		desc: "multiple entries - single NI",
		inOperations: []*spb.AFTOperation{{
			NetworkInstance: server.DefaultNetworkInstanceName,
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix:    "1.1.1.1/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
				},
			},
		}, {
			NetworkInstance: server.DefaultNetworkInstanceName,
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix:    "2.2.2.2/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
				},
			},
		}},
		inGetRequest: &spb.GetRequest{
			NetworkInstance: &spb.GetRequest_Name{
				Name: server.DefaultNetworkInstanceName,
			},
			Aft: spb.AFTType_ALL,
		},
		wantResponse: &spb.GetResponse{
			Entry: []*spb.AFTEntry{{
				NetworkInstance: server.DefaultNetworkInstanceName,
				Entry: &spb.AFTEntry_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix:    "1.1.1.1/32",
						Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
					},
				},
			}, {
				NetworkInstance: server.DefaultNetworkInstanceName,
				Entry: &spb.AFTEntry_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix:    "2.2.2.2/32",
						Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
					},
				},
			}},
		},
	}, {
		desc: "multiple entries - different NIs, but only one requested",
		inOperations: []*spb.AFTOperation{{
			NetworkInstance: server.DefaultNetworkInstanceName,
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix:    "1.1.1.1/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
				},
			},
		}, {
			// this entry should not be returned.
			NetworkInstance: "VRF-42",
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix:    "2.2.2.2/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
				},
			},
		}},
		inGetRequest: &spb.GetRequest{
			NetworkInstance: &spb.GetRequest_Name{
				Name: server.DefaultNetworkInstanceName,
			},
			Aft: spb.AFTType_ALL,
		},
		wantResponse: &spb.GetResponse{
			Entry: []*spb.AFTEntry{{
				NetworkInstance: server.DefaultNetworkInstanceName,
				Entry: &spb.AFTEntry_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix:    "1.1.1.1/32",
						Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
					},
				},
			}},
		},
	}, {
		desc: "single entry in Server - non-default NI",
		inOperations: []*spb.AFTOperation{{
			NetworkInstance: "VRF-FOO",
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix:    "1.1.1.1/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
				},
			},
		}},
		inGetRequest: &spb.GetRequest{
			NetworkInstance: &spb.GetRequest_Name{
				Name: "VRF-FOO",
			},
			Aft: spb.AFTType_ALL,
		},
		wantResponse: &spb.GetResponse{
			Entry: []*spb.AFTEntry{{
				NetworkInstance: "VRF-FOO",
				Entry: &spb.AFTEntry_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix:    "1.1.1.1/32",
						Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
					},
				},
			}},
		},
	}, {
		desc: "multiple entries - different NI - with All",
		inOperations: []*spb.AFTOperation{{
			NetworkInstance: server.DefaultNetworkInstanceName,
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix:    "1.1.1.1/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
				},
			},
		}, {
			NetworkInstance: "VRF-42",
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix:    "2.2.2.2/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
				},
			},
		}},
		inGetRequest: &spb.GetRequest{
			NetworkInstance: &spb.GetRequest_All{
				All: &spb.Empty{},
			},
			Aft: spb.AFTType_ALL,
		},
		wantResponse: &spb.GetResponse{
			Entry: []*spb.AFTEntry{{
				NetworkInstance: server.DefaultNetworkInstanceName,
				Entry: &spb.AFTEntry_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix:    "1.1.1.1/32",
						Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
					},
				},
			}, {
				NetworkInstance: "VRF-42",
				Entry: &spb.AFTEntry_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix:    "2.2.2.2/32",
						Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
					},
				},
			}},
		},
	}, {
		desc:         "invalid request - nothing specified",
		inGetRequest: &spb.GetRequest{},
		wantErr:      true,
	}, {
		desc: "invalid request, unsupported AFT",
		inGetRequest: &spb.GetRequest{
			NetworkInstance: &spb.GetRequest_Name{
				Name: "foo",
			},
		},
		wantErr: true,
	}, {
		desc: "multiple entries - single NI - fltered",
		inOperations: []*spb.AFTOperation{{
			NetworkInstance: server.DefaultNetworkInstanceName,
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix:    "1.1.1.1/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
				},
			},
		}, {
			NetworkInstance: server.DefaultNetworkInstanceName,
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix:    "2.2.2.2/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
				},
			},
		}, {
			NetworkInstance: server.DefaultNetworkInstanceName,
			Entry: &spb.AFTOperation_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index:   1,
					NextHop: &aftpb.Afts_NextHop{},
				},
			},
		}},
		inGetRequest: &spb.GetRequest{
			NetworkInstance: &spb.GetRequest_Name{
				Name: server.DefaultNetworkInstanceName,
			},
			Aft: spb.AFTType_IPV4,
		},
		wantResponse: &spb.GetResponse{
			Entry: []*spb.AFTEntry{{
				NetworkInstance: server.DefaultNetworkInstanceName,
				Entry: &spb.AFTEntry_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix:    "1.1.1.1/32",
						Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
					},
				},
			}, {
				NetworkInstance: server.DefaultNetworkInstanceName,
				Entry: &spb.AFTEntry_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix:    "2.2.2.2/32",
						Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
					},
				},
			}},
		},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			nr := rib.New(server.DefaultNetworkInstanceName, rib.DisableRIBCheckFn())
			addedNIs := map[string]bool{server.DefaultNetworkInstanceName: true}

			for _, op := range tt.inOperations {
				ni := op.GetNetworkInstance()
				if !addedNIs[ni] {
					if err := nr.AddNetworkInstance(ni); err != nil {
						t.Fatalf("invalid operations, cannot add NI %s", ni)
					}
					addedNIs[ni] = true
				}
				if _, _, err := nr.AddEntry(ni, op); err != nil {
					t.Fatalf("invalid operations, cannot add entry to NI %s, (entry: %s), err: %v", ni, op, err)
				}
			}

			creds, err := credentials.NewServerTLSFromFile(testcommon.TLSCreds())
			if err != nil {
				t.Fatalf("cannot load TLS credentials, got err: %v", err)
			}
			srv := grpc.NewServer(grpc.Creds(creds))
			s, err := server.NewFake(server.DisableRIBCheckFn())
			if err != nil {
				t.Fatalf("cannot create server, error: %v", err)
			}

			s.InjectRIB(nr)

			l, err := net.Listen("tcp", "localhost:0")
			if err != nil {
				t.Fatalf("cannot create gRIBI server, %v", err)
			}

			spb.RegisterGRIBIServer(srv, s)

			go srv.Serve(l)
			defer srv.Stop()

			c, err := New()
			if err != nil {
				t.Fatalf("cannot create client, %v", err)
			}
			dctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			if err := c.Dial(dctx, l.Addr().String()); err != nil {
				t.Fatalf("cannot connect to fake server, %v", err)
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			got, err := c.Get(ctx, tt.inGetRequest)
			if (err != nil) != tt.wantErr {
				t.Fatalf("did not get expected error, got: %v, wantErr? %v", err, tt.wantErr)
			}

			if diff := cmp.Diff(got, tt.wantResponse,
				cmpopts.EquateEmpty(),
				protocmp.Transform(),
				protocmp.SortRepeated(func(a, b *spb.AFTEntry) bool {
					return prototext.Format(a) < prototext.Format(b)
				})); diff != "" {
				t.Fatalf("did not get expected responses, diff(-got,+want):\n%s", diff)
			}
		})
	}
}

func TestOpResultString(t *testing.T) {
	tests := []struct {
		desc     string
		inResult *OpResult
		want     string
	}{{
		desc:     "nil input",
		inResult: nil,
		want:     "<nil>",
	}, {
		desc:     "all fields nil",
		inResult: &OpResult{},
		want:     "<0 (0 nsec):>",
	}, {
		desc: "nil type in details",
		inResult: &OpResult{
			OperationID: 42,
		},
		want: "<0 (0 nsec): AFTOperation { ID: 42, Details: <nil>, Status: UNSET }>",
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			if got := tt.inResult.String(); got != tt.want {
				t.Fatalf("did not get expected string, got: %s, want: %s", got, tt.want)
			}
		})
	}
}

func TestFlush(t *testing.T) {
	tests := []struct {
		desc         string
		inClient     *Client
		inReq        *spb.FlushRequest
		inRIB        *rib.RIB
		inElectionID *spb.Uint128
		wantResponse *spb.FlushResponse
		wantErr      bool
	}{{
		desc: "missing election ID when client is SINGLE_PRIMARY",
		inClient: func() *Client {
			c, err := New(
				ElectedPrimaryClient(&spb.Uint128{
					High: 0,
					Low:  1,
				}),
			)
			if err != nil {
				t.Fatalf("can't initialise client, %v", err)
			}
			return c
		}(),
		inReq: &spb.FlushRequest{
			NetworkInstance: &spb.FlushRequest_All{
				All: &spb.Empty{},
			},
		},
		wantErr: true,
	}, {
		desc: "specified election ID when client is SINGLE_PRIMARY",
		inClient: func() *Client {
			c, err := New(
				ElectedPrimaryClient(&spb.Uint128{
					High: 0,
					Low:  1,
				}),
			)
			if err != nil {
				t.Fatalf("can't initialise client, %v", err)
			}
			return c
		}(),
		inReq: &spb.FlushRequest{
			Election: &spb.FlushRequest_Id{
				Id: &spb.Uint128{High: 0, Low: 1},
			},
			NetworkInstance: &spb.FlushRequest_All{
				All: &spb.Empty{},
			},
		},
		inElectionID: &spb.Uint128{High: 0, Low: 1},
	}, {
		desc: "overridden Election ID when client is SINGLE_PRIMARY",
		inClient: func() *Client {
			c, err := New(
				ElectedPrimaryClient(&spb.Uint128{
					High: 0,
					Low:  1,
				}),
			)
			if err != nil {
				t.Fatalf("can't initialise client, %v", err)
			}
			return c
		}(),
		inReq: &spb.FlushRequest{
			Election: &spb.FlushRequest_Override{
				Override: &spb.Empty{},
			},
			NetworkInstance: &spb.FlushRequest_All{
				All: &spb.Empty{},
			},
		},
		inElectionID: &spb.Uint128{High: 0, Low: 1},
	}, {
		desc: "named NI - exists",
		inClient: func() *Client {
			c, err := New()
			if err != nil {
				t.Fatalf("can't initialise client, %v", err)
			}
			return c
		}(),
		inReq: &spb.FlushRequest{
			NetworkInstance: &spb.FlushRequest_Name{
				Name: server.DefaultNetworkInstanceName,
			},
		},
	}, {
		desc: "named NI - missing",
		inClient: func() *Client {
			c, err := New()
			if err != nil {
				t.Fatalf("can't initialise client, %v", err)
			}
			return c
		}(),
		inReq: &spb.FlushRequest{
			NetworkInstance: &spb.FlushRequest_Name{
				Name: "doesn't-exist",
			},
		},
		wantErr: true,
	}, {
		desc: "named NI - empty string",
		inClient: func() *Client {
			c, err := New()
			if err != nil {
				t.Fatalf("can't initialise client, %v", err)
			}
			return c
		}(),
		inReq: &spb.FlushRequest{
			NetworkInstance: &spb.FlushRequest_Name{
				Name: "",
			},
		},
		wantErr: true,
	}, {
		desc: "election ID when client is not single primary",
		inClient: func() *Client {
			c, err := New()
			if err != nil {
				t.Fatalf("can't initialise client, %v", err)
			}
			return c
		}(),
		inReq: &spb.FlushRequest{
			NetworkInstance: &spb.FlushRequest_Name{
				Name: server.DefaultNetworkInstanceName,
			},
			Election: &spb.FlushRequest_Id{
				Id: &spb.Uint128{
					High: 1,
					Low:  1,
				},
			},
		},
		wantErr: true,
	}, {
		desc: "override with non-SINGLE_PRIMARY client",
		inClient: func() *Client {
			c, err := New()
			if err != nil {
				t.Fatalf("can't initialise client, %v", err)
			}
			return c
		}(),
		inReq: &spb.FlushRequest{
			NetworkInstance: &spb.FlushRequest_Name{
				Name: server.DefaultNetworkInstanceName,
			},
			Election: &spb.FlushRequest_Override{
				Override: &spb.Empty{},
			},
		},
		wantErr: true,
	}, {
		desc: "nil flush request",
		inClient: func() *Client {
			c, err := New()
			if err != nil {
				t.Fatalf("can't initialise client, %v", err)
			}
			return c
		}(),
		wantErr: true,
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			defer tt.inClient.Close()
			creds, err := credentials.NewServerTLSFromFile(testcommon.TLSCreds())
			if err != nil {
				t.Fatalf("cannot load TLS credentials, got err: %v", err)
			}
			srv := grpc.NewServer(grpc.Creds(creds))
			s, err := server.NewFake(
				server.DisableRIBCheckFn(),
			)
			if err != nil {
				t.Fatalf("cannot create server, error: %v", err)
			}

			s.InjectRIB(rib.New(server.DefaultNetworkInstanceName))
			if tt.inRIB != nil {
				s.InjectRIB(tt.inRIB)
			}

			if tt.inElectionID != nil {
				s.InjectElectionID(tt.inElectionID)
			}

			l, err := net.Listen("tcp", "localhost:0")
			if err != nil {
				t.Fatalf("cannot create gRIBI server, %v", err)
			}

			spb.RegisterGRIBIServer(srv, s)

			go srv.Serve(l)
			defer srv.Stop()

			dctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			if err := tt.inClient.Dial(dctx, l.Addr().String()); err != nil {
				t.Fatalf("cannot connect to fake server, %v", err)
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			res, err := tt.inClient.Flush(ctx, tt.inReq)
			if (err != nil) != tt.wantErr {
				t.Fatalf("did not get expected error, got: %v, wantErr? %v", err, tt.wantErr)
			}

			if res == nil {
				return
			}

			// ensure that the timestamp is numerically before the current time.
			if got, now := res.Timestamp, time.Now().UnixNano(); got > now {
				t.Fatalf("received impossible timestamp, got: %v, want: timestamp < %d", res, now)
			}
		})
	}
}

// TestServerModifyIntegration performs a basic integration test for the Modify
// RPC between the server and client to ensure that
// methods are covered by a test local to the client package.
func TestServerModifyIntegration(t *testing.T) {
	tests := []struct {
		desc   string
		testFn func(context.Context, *Client) error
	}{{
		desc: "connection",
		testFn: func(ctx context.Context, c *Client) error {
			defer c.Close()
			if err := c.Connect(ctx); err != nil {
				return fmt.Errorf("Connect(): cannot connect to server, %v", err)
			}
			return nil
		},
	}, {
		desc: "connect and await converged - no messages",
		testFn: func(ctx context.Context, c *Client) error {
			defer c.Close()
			if err := c.Connect(ctx); err != nil {
				return fmt.Errorf("Connect(): cannot connect to server, %v", err)
			}

			if err := c.AwaitConverged(ctx); err != nil {
				return fmt.Errorf("AwaitConverged(): returned error, %v", err)
			}
			return nil
		},
	}, {
		desc: "connect and start sending",
		testFn: func(ctx context.Context, c *Client) error {
			defer c.Close()
			if err := c.Connect(ctx); err != nil {
				return fmt.Errorf("Connect(): cannot connect to server, %v", err)
			}

			c.Q(&spb.ModifyRequest{
				Params: &spb.SessionParameters{
					AckType:     spb.SessionParameters_RIB_ACK,
					Redundancy:  spb.SessionParameters_SINGLE_PRIMARY,
					Persistence: spb.SessionParameters_PRESERVE,
				},
			})
			c.StartSending()

			if err := c.AwaitConverged(ctx); err != nil {
				return fmt.Errorf("AwaitConverged(): returned error, %v", err)
			}
			return nil
		},
	}, {
		desc: "test benchmarking parameters",
		testFn: func(ctx context.Context, c *Client) error {
			defer func() {
				TreatRIBACKAsCompletedInFIBACKMode = false
				BusyLoopDelay = 100 * time.Millisecond
				c.Close()
			}()

			if err := c.Connect(ctx); err != nil {
				return fmt.Errorf("Connect(): cannot connect to server, %v", err)
			}

			TreatRIBACKAsCompletedInFIBACKMode = true
			BusyLoopDelay = 0 * time.Millisecond

			// Explicitly set our session parameters for the test.
			c.state.SessParams = &spb.SessionParameters{
				AckType:     spb.SessionParameters_RIB_AND_FIB_ACK,
				Redundancy:  spb.SessionParameters_SINGLE_PRIMARY,
				Persistence: spb.SessionParameters_PRESERVE,
			}

			c.Q(&spb.ModifyRequest{
				ElectionId: &spb.Uint128{
					Low:  1,
					High: 0,
				},
			})

			c.StartSending()

			c.Q(&spb.ModifyRequest{
				Operation: []*spb.AFTOperation{{
					ElectionId: &spb.Uint128{
						Low:  1,
						High: 0,
					},
					Id:              1,
					NetworkInstance: server.DefaultNetworkInstanceName,
					Op:              spb.AFTOperation_ADD,
					Entry: &spb.AFTOperation_NextHop{
						NextHop: &aftpb.Afts_NextHopKey{
							Index:   1,
							NextHop: &aftpb.Afts_NextHop{},
						},
					},
				}},
			})

			time.Sleep(100 * time.Millisecond) // Ensure that we got RIB and FIB ACK.

			ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			if err := c.AwaitConverged(ctx); err != nil {
				return fmt.Errorf("AwaitConverged(): returned error, %v", err)
			}
			return nil
		},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			nr := rib.New(server.DefaultNetworkInstanceName, rib.DisableRIBCheckFn())
			creds, err := credentials.NewServerTLSFromFile(testcommon.TLSCreds())
			if err != nil {
				t.Fatalf("cannot load TLS credentials, got err: %v", err)
			}
			srv := grpc.NewServer(grpc.Creds(creds))
			s, err := server.NewFake(server.DisableRIBCheckFn())
			if err != nil {
				t.Fatalf("cannot create server, error: %v", err)
			}

			s.InjectRIB(nr)
			l, err := net.Listen("tcp", "localhost:0")
			if err != nil {
				t.Fatalf("cannot create gRIBI server, %v", err)
			}

			spb.RegisterGRIBIServer(srv, s)

			go srv.Serve(l)
			defer srv.Stop()

			c, err := New()
			if err != nil {
				t.Fatalf("cannot create client, %v", err)
			}
			dctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := c.Dial(dctx, l.Addr().String()); err != nil {
				t.Fatalf("Dial(_, %s): cannot dial fake server, err: %v", l.Addr().String(), err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			if err := tt.testFn(ctx, c); err != nil {
				t.Fatalf("failed test function, %v", err)
			}
		})
	}

}

type disconnectMode int64

const (
	_ disconnectMode = iota
	EOF
	PauseEOF
	PauseError
	LongPauseError
	NoError
	PauseNoError
)

func (d disconnectMode) String() string {
	switch d {
	case EOF:
		return "EOF"
	case PauseEOF:
		return "EOF after pause"
	case PauseError:
		return "Server error after pause"
	case LongPauseError:
		return "Server error after 100s pause"
	case NoError:
		return "nil error"
	case PauseNoError:
		return "nil error after pause"
	default:
		return fmt.Sprintf("%d", d)
	}
}

type disconnectingGRIBI struct {
	spb.UnimplementedGRIBIServer
	mode disconnectMode
	// Set to true if we should also read the channel.
	receiver bool

	mu   sync.Mutex
	msgs []*spb.ModifyRequest
}

func (d *disconnectingGRIBI) Modify(stream spb.GRIBI_ModifyServer) error {
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			m, err := stream.Recv()
			if err != nil {
				return
			}
			d.mu.Lock()
			d.msgs = append(d.msgs, m)
			d.mu.Unlock()
		}
	}()

	var err error
	switch d.mode {
	case EOF:
		err = io.EOF
	case PauseEOF:
		time.Sleep(2 * time.Second)
		err = io.EOF
	case PauseError:
		time.Sleep(2 * time.Second)
		err = status.Errorf(codes.ResourceExhausted, "yawn!")
	case LongPauseError:
		time.Sleep(100 * time.Second)
		err = status.Errorf(codes.ResourceExhausted, "slept 100 seconds")
	case NoError:
	case PauseNoError:
		time.Sleep(2 * time.Second)
	}
	if d.receiver {
		wg.Wait()
	}
	return err
}

func (d *disconnectingGRIBI) Flush(_ context.Context, _ *spb.FlushRequest) (*spb.FlushResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "unimplemented")
}

func (d *disconnectingGRIBI) Get(_ *spb.GetRequest, _ spb.GRIBI_GetServer) error {
	return status.Errorf(codes.Unimplemented, "unimplemented")
}

func newDisconnectingFake(t testing.TB, mode disconnectMode) (*disconnectingGRIBI, string, func()) {
	creds, err := credentials.NewServerTLSFromFile(testcommon.TLSCreds())
	if err != nil {
		t.Fatalf("cannot load TLS credentials, got err: %v", err)
	}
	srv := grpc.NewServer(grpc.Creds(creds))
	if err != nil {
		t.Fatalf("cannot create server, err: %v", err)
	}
	s := &disconnectingGRIBI{mode: mode}
	spb.RegisterGRIBIServer(srv, s)

	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("cannot listen, err: %v", err)
	}

	go srv.Serve(l)
	return s, l.Addr().String(), srv.Stop
}

func TestDone(t *testing.T) {
	tests := []struct {
		desc                 string
		inMode               disconnectMode
		inReconnects         int
		inTimeoutSeconds     int
		inWaitTimeoutSeconds int
		wantDone             bool
	}{{
		desc:         "EOF returned",
		inMode:       EOF,
		inReconnects: 5,
		wantDone:     true,
	}, {
		desc:         "pause",
		inMode:       PauseError,
		inReconnects: 1,
		wantDone:     true,
	}, {
		desc:                 "inner context is cancelled",
		inMode:               LongPauseError,
		inTimeoutSeconds:     10,
		inWaitTimeoutSeconds: 1,
		inReconnects:         1,
		wantDone:             true,
	}, {
		desc:         "no error returned",
		inMode:       NoError,
		inReconnects: 1,
		wantDone:     true,
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {

			_, addr, stopSrv := newDisconnectingFake(t, tt.inMode)
			defer stopSrv()

			c, err := New()
			if err != nil {
				t.Fatalf("cannot create client, err: %v", err)
			}

			dur := 5 * time.Second
			if tt.inTimeoutSeconds != 0 {
				dur = time.Duration(tt.inTimeoutSeconds) * time.Second
			}

			dctx, cancel := context.WithTimeout(context.Background(), dur)
			defer cancel()
			if err := c.Dial(dctx, addr); err != nil {
				t.Fatalf("c.Dial(_, %s): cannot dial fake server, got err: %v", addr, err)
			}

			for i := 0; i <= tt.inReconnects; i++ {
				t.Logf("running reconnect %d for %s\n", i, tt.desc)
				var (
					wg     sync.WaitGroup
					retErr error
				)

				innerDur := 5 * time.Second
				if tt.inWaitTimeoutSeconds != 0 {
					innerDur = time.Duration(tt.inWaitTimeoutSeconds) * time.Second
				}

				waitCtx, cancel := context.WithTimeout(dctx, innerDur)
				defer cancel()

				if err := c.Connect(waitCtx); err != nil {
					retErr = fmt.Errorf("Connect(): cannot connect to server, %v", err)
				}

				c.Q(&spb.ModifyRequest{})
				c.StartSending()

				var got bool
				wg.Add(1)
				go func(ctx context.Context) {
					defer wg.Done()
					select {
					case <-c.Done():
						got = true
						return
					case <-ctx.Done():
						// This case only happens if the overall test times out.
					}
				}(dctx)

				wg.Wait()
				if retErr != nil {
					t.Fatalf("did not connect to server, got: %v, want: nil", retErr)
				}
				if got != tt.wantDone {
					t.Fatalf("did not get done signal, got: %v, want: %v", got, tt.wantDone)
				}
				c.StopSending()
				c.Reset()
			}
		})
	}
}

func TestReconnect(t *testing.T) {
	// How many runs to do for unstable tests. Note that some tests have relatively long runtime
	// so this might result in test target timeouts if too high.
	manyRuns := 30
	tests := []struct {
		desc           string
		inModes        []disconnectMode // Must use Pause modes if results are expected.
		inReconnects   int
		inClientOpts   []Opt
		inMsgs         []*spb.ModifyRequest
		inMsgFn        func(int) []*spb.ModifyRequest
		inCallStop     bool
		inQBeforeStart bool
		inRunTime      time.Duration
		inSkipReset    bool
		inStopServer   bool
		wantMsgs       []*spb.ModifyRequest
		wantConnectErr bool
	}{{
		desc:         "reconnect - stop sending not called",
		inModes:      []disconnectMode{PauseNoError, PauseError, PauseEOF},
		inReconnects: 1,
		inClientOpts: []Opt{
			PersistEntries(),
		},
		inMsgs: []*spb.ModifyRequest{{
			ElectionId: &spb.Uint128{Low: 1},
		}},
		wantMsgs: []*spb.ModifyRequest{
			// Must get parameters before any other message.
			{Params: &spb.SessionParameters{Persistence: spb.SessionParameters_PRESERVE}},
			{ElectionId: &spb.Uint128{Low: 1}},
			{Params: &spb.SessionParameters{Persistence: spb.SessionParameters_PRESERVE}},
			{ElectionId: &spb.Uint128{Low: 1}},
		},
	}, {
		desc:         "reconnect - stop sending called",
		inModes:      []disconnectMode{PauseNoError, PauseError, EOF},
		inReconnects: 1,
		inClientOpts: []Opt{
			PersistEntries(),
		},
		inMsgs: []*spb.ModifyRequest{{
			ElectionId: &spb.Uint128{Low: 1},
		}},
		inCallStop: true,
		wantMsgs: []*spb.ModifyRequest{
			// Must get parameters before any other message.
			{Params: &spb.SessionParameters{Persistence: spb.SessionParameters_PRESERVE}},
			{ElectionId: &spb.Uint128{Low: 1}},
			{Params: &spb.SessionParameters{Persistence: spb.SessionParameters_PRESERVE}},
			{ElectionId: &spb.Uint128{Low: 1}},
		},
	}, {
		desc:         "reconnect - client qs before calling start",
		inModes:      []disconnectMode{PauseNoError, PauseError, PauseEOF},
		inReconnects: 1,
		inClientOpts: []Opt{
			PersistEntries(),
		},
		inMsgs: []*spb.ModifyRequest{{
			ElectionId: &spb.Uint128{Low: 1},
		}},
		inQBeforeStart: true,
		wantMsgs: []*spb.ModifyRequest{
			// Must get parameters before any other message.
			{Params: &spb.SessionParameters{Persistence: spb.SessionParameters_PRESERVE}},
			{ElectionId: &spb.Uint128{Low: 1}},
			{Params: &spb.SessionParameters{Persistence: spb.SessionParameters_PRESERVE}},
			{ElectionId: &spb.Uint128{Low: 1}},
		},
	}, {
		desc:         "reconnect - client qs before calling start but has stopped",
		inModes:      []disconnectMode{PauseNoError, PauseError, PauseEOF},
		inReconnects: 1,
		inClientOpts: []Opt{
			PersistEntries(),
		},
		inMsgs: []*spb.ModifyRequest{{
			ElectionId: &spb.Uint128{Low: 1},
		}},
		inCallStop:     true,
		inQBeforeStart: true,
		wantMsgs: []*spb.ModifyRequest{
			// Must get parameters before any other message.
			{Params: &spb.SessionParameters{Persistence: spb.SessionParameters_PRESERVE}},
			{ElectionId: &spb.Uint128{Low: 1}},
			{Params: &spb.SessionParameters{Persistence: spb.SessionParameters_PRESERVE}},
			{ElectionId: &spb.Uint128{Low: 1}},
		},
	}, {
		desc:         "very unstable server",
		inModes:      []disconnectMode{PauseNoError, PauseEOF, PauseError},
		inReconnects: manyRuns,
		inClientOpts: []Opt{
			PersistEntries(),
			ElectedPrimaryClient(&spb.Uint128{Low: 42}),
		},
		inMsgFn: func(attempt int) []*spb.ModifyRequest {
			return []*spb.ModifyRequest{
				{ElectionId: &spb.Uint128{Low: uint64(42 + attempt)}},
			}
		},
		inRunTime: time.Duration(manyRuns) * 2 * time.Second,
		wantMsgs: func() []*spb.ModifyRequest {
			msgs := []*spb.ModifyRequest{}
			for i := 0; i <= manyRuns; i++ {
				msgs = append(msgs,
					[]*spb.ModifyRequest{
						{Params: &spb.SessionParameters{Persistence: spb.SessionParameters_PRESERVE, Redundancy: spb.SessionParameters_SINGLE_PRIMARY}},
						{ElectionId: &spb.Uint128{Low: 42}},
						{ElectionId: &spb.Uint128{Low: uint64(42 + i)}},
					}...)
			}
			return msgs
		}(),
	}, {
		desc:         "very unstable server - don't check results",
		inModes:      []disconnectMode{NoError, EOF},
		inReconnects: manyRuns,
		inClientOpts: []Opt{
			PersistEntries(),
			ElectedPrimaryClient(&spb.Uint128{Low: 42}),
		},
		inMsgFn: func(attempt int) []*spb.ModifyRequest {
			return []*spb.ModifyRequest{
				{ElectionId: &spb.Uint128{Low: uint64(42 + attempt)}},
			}
		},
		inRunTime: time.Duration(manyRuns) * 2 * time.Second,
	}, {
		desc:         "server that isn't even listening",
		inModes:      []disconnectMode{EOF}, // Doesn't matter, we are not going to dial it.
		inReconnects: 10,                    // More than the buffer depth of modifyCh.
		inRunTime:    5 * time.Second,
		inStopServer: true,
	}}

	for _, tt := range tests {
		for _, mode := range tt.inModes {
			t.Run(fmt.Sprintf("%s - mode %s", tt.desc, mode), func(t *testing.T) {
				server, addr, stop := newDisconnectingFake(t, mode)
				server.receiver = true
				defer stop()

				c, err := New(tt.inClientOpts...)
				if err != nil {
					t.Fatalf("cannot create client, err: %v", err)
				}
				to := tt.inRunTime
				if to.Seconds() < 2.0 {
					to = 2 * time.Second
				}
				dctx, cancel := context.WithTimeout(context.Background(), to)
				defer cancel()

				if err := c.Dial(dctx, addr); err != nil {
					t.Fatalf("c.Dial(_, %s): cannot dial fake server, got err: %v", addr, err)
				}

				for i := 0; i <= tt.inReconnects; i++ {
					if tt.inStopServer && i > 0 {
						// Kill the server so that its no longer listening.
						stop()
					}
					t.Logf("reconnecting to server (mode: %s), attempt: %d", mode, i)
					var (
						wg     sync.WaitGroup
						retErr error
					)

					select {
					case <-dctx.Done():
						t.Fatalf("dial context timed out, %v", err)
					default:
					}

					waitCtx, stopWait := context.WithTimeout(dctx, 200*time.Millisecond)
					defer stopWait()

					if err := c.Connect(waitCtx); err != nil {
						switch {
						case tt.inStopServer && i == 0, !tt.inStopServer:
							retErr = fmt.Errorf("Connect(): cannot connect to server, %v", err)
						default:
						}
					}

					runMsg := tt.inMsgs
					if tt.inMsgFn != nil {
						runMsg = tt.inMsgFn(i)
					}

					if tt.inQBeforeStart {
						for _, m := range runMsg {
							c.Q(m)
						}
					}

					c.StartSending()
					if tt.inCallStop {
						defer c.StopSending()
					}

					if !tt.inQBeforeStart {
						for _, m := range runMsg {
							c.Q(m)
						}
					}

					var reconnected bool
					wg.Add(1)
					go func(ctx context.Context) {
						defer wg.Done()
						select {
						case <-c.Done():
							reconnected = true
							return
						case <-ctx.Done():
							reconnected = true
							return
						}
					}(waitCtx)

					wg.Wait()
					// Sanity check - we should have no error, and have the server go away.
					if retErr != nil {
						t.Fatalf("did not connect to server, got: %v, want: nil", retErr)
					}
					if !reconnected {
						t.Fatalf("did not reconnect to server, got: %v, want: true", reconnected)
					}
					c.Reset()
				}

				if tt.wantMsgs != nil {
					// Now check the messages that we saw at the server.
					server.mu.Lock()
					defer server.mu.Unlock()
					if diff := cmp.Diff(server.msgs, tt.wantMsgs, protocmp.Transform()); diff != "" {
						t.Fatalf("did not get expected messages, diff(-got,+want):\n%s", diff)
					}
				}
			})
		}
	}
}

func TestReset(t *testing.T) {
	newC := func() *Client {
		c, _ := New()
		return c
	}
	tests := []struct {
		desc     string
		inClient *Client
	}{{
		desc:     "no fields set",
		inClient: newC(),
	}, {
		desc: "set fields",
		inClient: func() *Client {
			c, _ := New()
			c.sendErr = []error{errors.New("fish")}
			c.readErr = []error{errors.New("chips")}
			c.qs.sendq = []*spb.ModifyRequest{{}}
			c.qs.pendq = &pendingQueue{
				Ops: map[uint64]*PendingOp{1: {}},
			}
			c.qs.resultq = []*OpResult{{}}
			return c
		}(),
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			// Setup, make sure it looks like our goroutines are started.
			tt.inClient.sendExitCh = make(chan struct{}, 1)
			tt.inClient.sendExitCh <- struct{}{}
			close(tt.inClient.sendExitCh)

			tt.inClient.Reset()
			c := tt.inClient
			// Don't compare the whole struct because there are unexported fields and it is not for public consumption.

			if len(c.sendErr) != 0 {
				t.Errorf("unexpected send errors, got: %d, want: 0", len(c.sendErr))
			}
			if len(c.readErr) != 0 {
				t.Errorf("unexpected read errors, got: %d, want: 0", len(c.readErr))
			}
			if len(c.qs.sendq) != 0 {
				t.Errorf("unexpected send queue got: %d, want: 0", len(c.qs.sendq))
			}
			if len(c.qs.pendq.Ops) != 0 {
				t.Errorf("unexpected pending queue, got: %d, want: 0", len(c.qs.pendq.Ops))
			}
			if len(c.qs.resultq) != 0 {
				t.Errorf("unexpected results queue, got: %d, want: 0", len(c.qs.resultq))
			}
		})
	}
}

func TestAckResult(t *testing.T) {
	tests := []struct {
		desc        string
		inClient    *Client
		inACK       []*OpResult
		wantResultQ []*OpResult
		wantErr     bool
	}{{
		desc: "ACK single result",
		inClient: &Client{
			qs: &clientQs{
				resultq: []*OpResult{{
					OperationID: 42,
				}},
			},
		},
		inACK: []*OpResult{{
			OperationID: 42,
		}},
		wantResultQ: []*OpResult{},
	}, {
		desc: "ACK single result with details",
		inClient: &Client{
			qs: &clientQs{
				resultq: []*OpResult{{
					OperationID:             42,
					Timestamp:               42,
					CurrentServerElectionID: &spb.Uint128{Low: 42},
					Details: &OpDetailsResults{
						Type:         constants.Add,
						NextHopIndex: 42,
					},
				}},
			},
		},
		inACK: []*OpResult{{
			OperationID: 42,
		}},
		wantResultQ: []*OpResult{},
	}, {
		desc: "ACK does not remove another operation",
		inClient: &Client{
			qs: &clientQs{
				resultq: []*OpResult{{
					OperationID: 42,
				}, {
					OperationID: 21,
				}},
			},
		},
		inACK: []*OpResult{{
			OperationID: 42,
		}},
		wantResultQ: []*OpResult{{
			OperationID: 21,
		}},
	}, {
		desc: "ACK removes multiple operations",
		inClient: &Client{
			qs: &clientQs{
				resultq: []*OpResult{{
					OperationID: 42,
				}, {
					OperationID: 21,
				}},
			},
		},
		inACK: []*OpResult{{
			OperationID: 42,
		}, {
			OperationID: 21,
		}},
		wantResultQ: []*OpResult{},
	}, {
		desc: "requested ACK not found",
		inClient: &Client{
			qs: &clientQs{
				resultq: []*OpResult{{
					OperationID: 42,
				}, {
					OperationID: 21,
				}},
			},
		},
		inACK: []*OpResult{{
			OperationID: 1528,
		}},
		wantResultQ: []*OpResult{{
			OperationID: 42,
		}, {
			OperationID: 21,
		}},
		wantErr: true,
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			if err := tt.inClient.AckResult(tt.inACK...); (err != nil) != tt.wantErr {
				t.Fatalf("(client).AckResult(%v): did not get expected error, got: %v, wantErr? %v", tt.inACK, err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.inClient.qs.resultq, tt.wantResultQ); diff != "" {
				t.Fatalf("(client).AckResult(%v): did not get expected results q, diff(-got,+want):\n%s", tt.inACK, diff)
			}
		})
	}
}

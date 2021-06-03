package client

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	spb "github.com/openconfig/gribi/v1/proto/service"
	"google.golang.org/protobuf/testing/protocmp"
)

func TestHandleParams(t *testing.T) {
	tests := []struct {
		desc      string
		inOpts    []ClientOpt
		wantState *clientState
		wantErr   bool
	}{{
		desc:   "client with default parameters",
		inOpts: nil,
		wantState: &clientState{
			SessParams: &spb.SessionParameters{},
		},
	}, {
		desc: "ALL_PRIMARY client",
		inOpts: []ClientOpt{
			AllPrimaryClients(),
		},
		wantState: &clientState{
			SessParams: &spb.SessionParameters{
				Redundancy: spb.SessionParameters_ALL_PRIMARY,
			},
		},
	}, {
		desc: "SINGLE_PRIMARY client",
		inOpts: []ClientOpt{
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
		inOpts: []ClientOpt{
			ElectedPrimaryClient(&spb.Uint128{High: 0, Low: 1}),
			AllPrimaryClients(),
		},
		wantErr: true,
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
			c.qs.sending = tt.inSending
			for _, r := range tt.inReqs {
				c.Q(r)
			}
			if diff := cmp.Diff(c.qs.sendq, tt.wantQ, protocmp.Transform()); diff != "" {
				t.Fatalf("did not get expected send queue, %s", diff)
			}
		})
	}
}

func TestPending(t *testing.T) {
	tests := []struct {
		desc     string
		inClient *Client
		want     map[uint64]*PendingOp
		wantErr  bool
	}{{
		desc: "empty queue",
		inClient: &Client{
			qs: &clientQs{
				pendq: map[uint64]*PendingOp{},
			},
		},
		want: map[uint64]*PendingOp{},
	}, {
		desc: "populated queue",
		inClient: &Client{
			qs: &clientQs{
				pendq: map[uint64]*PendingOp{
					1:  {Op: &spb.AFTOperation{Id: 1}},
					42: {Op: &spb.AFTOperation{Id: 42}},
					84: {Op: &spb.AFTOperation{Id: 84}},
				},
			},
		},
		want: map[uint64]*PendingOp{
			1:  {Op: &spb.AFTOperation{Id: 1}},
			42: {Op: &spb.AFTOperation{Id: 42}},
			84: {Op: &spb.AFTOperation{Id: 84}},
		},
	}, {
		desc:     "nil queues",
		inClient: &Client{},
		wantErr:  true,
	}, {
		desc: "nil operation in queue",
		inClient: &Client{
			qs: &clientQs{
				pendq: map[uint64]*PendingOp{
					1: {},
				},
			},
		},
		wantErr: true,
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
				pendq:   map[uint64]*PendingOp{},
				resultq: []*OpResult{},
			},
		},
		wantStatus: &ClientStatus{
			Timestamp:           42,
			PendingTransactions: map[uint64]*PendingOp{},
			Results:             []*OpResult{},
			SendErrs:            []error{},
			ReadErrs:            []error{},
		},
	}, {
		desc: "populated queues",
		inClient: &Client{
			qs: &clientQs{
				pendq: map[uint64]*PendingOp{
					1: {Timestamp: 100, Op: &spb.AFTOperation{Id: 42}},
				},
				resultq: []*OpResult{{
					Timestamp: 50,
				}},
			},
		},
		wantStatus: &ClientStatus{
			Timestamp: 42,
			PendingTransactions: map[uint64]*PendingOp{
				42: {
					Timestamp: 100,
					Op: &spb.AFTOperation{
						Id: 42,
					},
				},
			},
			Results: []*OpResult{{
				Timestamp: 50,
			}},
			SendErrs: []error{},
			ReadErrs: []error{},
		},
	}, {
		desc: "erroneous pending queue",
		inClient: &Client{
			qs: &clientQs{
				pendq: map[uint64]*PendingOp{
					1: {},
				},
			},
		},
		wantErr: true,
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

func TestPostProcModifyResponse(t *testing.T) {
	tests := []struct {
		desc        string
		inResponse  *spb.ModifyResponse
		wantResults []*OpResult
		wantErr     bool
	}{{
		desc: "invalid combination of fields populated",
		inResponse: &spb.ModifyResponse{
			Result: []*spb.AFTResult{{
				Id: 42,
			}},
			ElectionId: &spb.Uint128{High: 42, Low: 0},
		},
		wantErr: true,
	}, {
		desc: "election ID populated",
		inResponse: &spb.ModifyResponse{
			ElectionId: &spb.Uint128{High: 0, Low: 42},
		},
		wantResults: []*OpResult{{
			CurrentServerElectionID: &spb.Uint128{High: 0, Low: 42},
		}},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			c, err := New()
			if err != nil {
				t.Fatalf("got error initialising client, %v", err)
			}
			if err := c.handleModifyResponse(tt.inResponse); (err != nil) != tt.wantErr {
				t.Fatalf("did not get expected error status, got: %v, wantErr: %v?", err, tt.wantErr)
			}

			if diff := cmp.Diff(c.qs.resultq, tt.wantResults, protocmp.Transform(), cmpopts.EquateEmpty()); diff != "" {
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
		wantPending map[uint64]*PendingOp
		wantErr     bool
	}{{
		desc: "valid input",
		inRequest: &spb.ModifyRequest{
			Operation: []*spb.AFTOperation{{
				Id: 1,
			}},
		},
		inClient: func() *Client { c, _ := New(); return c }(),
		wantPending: map[uint64]*PendingOp{
			1: {
				Timestamp: 42,
				Op:        &spb.AFTOperation{Id: 1},
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
				pendq: map[uint64]*PendingOp{
					128: {},
				},
			},
		},
		wantPending: map[uint64]*PendingOp{
			128: {},
		},
		wantErr: true,
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

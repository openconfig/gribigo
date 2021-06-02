package client

import (
	"testing"

	"github.com/google/go-cmp/cmp"
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
		want     []int
	}{{
		desc: "empty queue",
		inClient: &Client{
			qs: &clientQs{
				pendq: map[int]bool{},
			},
		},
		want: []int{},
	}, {
		desc: "populated queue",
		inClient: &Client{
			qs: &clientQs{
				pendq: map[int]bool{
					1:  true,
					42: true,
					84: true,
				},
			},
		},
		want: []int{1, 42, 84},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			if got, want := tt.inClient.Pending(), tt.want; !cmp.Equal(got, want) {
				t.Fatalf("did not get expected queue, got: %v, want: %q", got, want)
			}
		})
	}
}

func TestResults(t *testing.T) {
	tests := []struct {
		desc     string
		inClient *Client
		want     []*spb.ModifyResponse
	}{{
		desc: "empty queue",
		inClient: &Client{
			qs: &clientQs{
				resultq: []*spb.ModifyResponse{},
			},
		},
		want: []*spb.ModifyResponse{},
	}, {
		desc: "populated queue",
		inClient: &Client{
			qs: &clientQs{
				resultq: []*spb.ModifyResponse{{
					ElectionId: &spb.Uint128{
						Low:  0,
						High: 1,
					},
				}},
			},
		},
		want: []*spb.ModifyResponse{{
			ElectionId: &spb.Uint128{
				Low:  0,
				High: 1,
			},
		}},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			if got, want := tt.inClient.Results(), tt.want; !cmp.Equal(got, want, protocmp.Transform()) {
				t.Fatalf("did not get expected queue, got: %v, want: %v", got, want)
			}
		})
	}
}

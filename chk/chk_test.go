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

package chk

import (
	"strings"
	"testing"

	"github.com/openconfig/gribigo/client"
	"github.com/openconfig/gribigo/constants"
	"github.com/openconfig/gribigo/fluent"
	"github.com/openconfig/testt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	aftpb "github.com/openconfig/gribi/v1/proto/gribi_aft"
	spb "github.com/openconfig/gribi/v1/proto/service"
)

func TestHasMessage(t *testing.T) {
	tests := []struct {
		desc           string
		inResults      []*client.OpResult
		inMsg          *client.OpResult
		expectFatalMsg string
	}{{
		desc: "election ID is present",
		inResults: []*client.OpResult{{
			CurrentServerElectionID: &spb.Uint128{
				High: 0,
				Low:  42,
			},
		}},
		inMsg: fluent.OperationResult().WithCurrentServerElectionID(42, 0).AsResult(),
	}, {
		desc:           "election ID is not present",
		inResults:      []*client.OpResult{},
		inMsg:          fluent.OperationResult().WithCurrentServerElectionID(1, 1).AsResult(),
		expectFatalMsg: "results did not contain a result of value",
	}, {
		desc: "successful session params",
		inResults: []*client.OpResult{{
			SessionParameters: &spb.SessionParametersResult{
				Status: spb.SessionParametersResult_OK,
			},
		}},
		inMsg: fluent.OperationResult().WithSuccessfulSessionParams().AsResult(),
	}, {
		desc: "with specific operation ID",
		inResults: []*client.OpResult{{
			OperationID: 42,
		}},
		inMsg: fluent.OperationResult().WithOperationID(42).AsResult(),
	}, {
		desc: "with specific operation ID, ignore details",
		inResults: []*client.OpResult{{
			OperationID: 42,
			Details: &client.OpDetailsResults{
				Type: constants.Add,
			},
		}},
		inMsg: fluent.OperationResult().WithOperationID(42).AsResult(),
	}, {
		desc: "with specific operation ID, and details",
		inResults: []*client.OpResult{{
			OperationID: 42,
			Details: &client.OpDetailsResults{
				Type: constants.Add,
			},
		}},
		inMsg: fluent.OperationResult().
			WithOperationID(42).
			WithOperationType(constants.Add).
			AsResult(),
	}, {
		desc: "check for ipv4 prefix",
		inResults: []*client.OpResult{{
			OperationID: 42,
			Details: &client.OpDetailsResults{
				Type:       constants.Add,
				IPv4Prefix: "1.1.1.1/32",
			},
		}},
		inMsg: fluent.OperationResult().
			WithOperationID(42).
			WithIPv4Operation("1.1.1.1/32").
			WithOperationType(constants.Add).
			AsResult(),
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			if tt.expectFatalMsg != "" {
				got := testt.ExpectFatal(t, func(t testing.TB) {
					HasResult(t, tt.inResults, tt.inMsg)
				})
				if !strings.Contains(got, tt.expectFatalMsg) {
					t.Fatalf("did not get expected fatal message, but test called Fatal, got: %s, want: %s", got, tt.expectFatalMsg)
				}
				return
			}
			HasResult(t, tt.inResults, tt.inMsg)
		})
	}
}

func TestHasResultsCache(t *testing.T) {
	tests := []struct {
		desc           string
		inResults      []*client.OpResult
		inWants        []*client.OpResult
		expectFatalMsg string
		inOpt          []resultOpt
	}{{
		desc: "single want, single result by operation ID",
		inResults: []*client.OpResult{{
			OperationID: 42,
		}},
		inWants: []*client.OpResult{{
			OperationID: 42,
		}},
	}, {
		desc: "multiple results, single want by operation ID",
		inResults: []*client.OpResult{{
			OperationID: 42,
		}, {
			OperationID: 128,
		}},
		inWants: []*client.OpResult{{
			OperationID: 128,
		}},
	}, {
		desc: "multiple results, multiple want by opeation ID",
		inResults: []*client.OpResult{{
			OperationID: 42,
		}, {
			OperationID: 128,
		}},
		inWants: []*client.OpResult{{
			OperationID: 42,
		}, {
			OperationID: 128,
		}},
	}, {
		desc: "missing result by operation ID",
		inResults: []*client.OpResult{{
			OperationID: 52,
		}, {
			OperationID: 124,
		}},
		inWants: []*client.OpResult{{
			OperationID: 42,
		}},
		expectFatalMsg: "results did not contain a result of value",
	}, {
		desc: "result by nhg ID - found",
		inResults: []*client.OpResult{{
			OperationID: 42,
			Details: &client.OpDetailsResults{
				NextHopGroupID: 242,
			},
		}, {
			OperationID: 44,
			Details: &client.OpDetailsResults{
				NextHopGroupID: 244,
			},
		}},
		inWants: []*client.OpResult{{
			Details: &client.OpDetailsResults{
				NextHopGroupID: 242,
			},
		}, {
			Details: &client.OpDetailsResults{
				NextHopGroupID: 244,
			},
		}},
		inOpt: []resultOpt{IgnoreOperationID()},
	}, {
		desc: "result by nhg ID - missing",
		inResults: []*client.OpResult{{
			OperationID: 42,
			Details: &client.OpDetailsResults{
				NextHopGroupID: 242,
			},
		}, {
			OperationID: 44,
			Details: &client.OpDetailsResults{
				NextHopGroupID: 256,
			},
		}},
		inWants: []*client.OpResult{{
			Details: &client.OpDetailsResults{
				NextHopGroupID: 257,
			},
		}},
		inOpt:          []resultOpt{IgnoreOperationID()},
		expectFatalMsg: "results did not contain a result",
	}, {
		desc: "result by NH ID - found",
		inResults: []*client.OpResult{{
			OperationID: 42,
			Details: &client.OpDetailsResults{
				NextHopIndex: 420,
			},
		}},
		inWants: []*client.OpResult{{
			Details: &client.OpDetailsResults{
				NextHopIndex: 420,
			},
		}},
		inOpt: []resultOpt{IgnoreOperationID()},
	}, {
		desc: "result by NH ID - missing",
		inResults: []*client.OpResult{{
			OperationID: 42,
			Details: &client.OpDetailsResults{
				NextHopIndex: 420,
			},
		}},
		inWants: []*client.OpResult{{
			Details: &client.OpDetailsResults{
				NextHopIndex: 1280,
			},
		}},
		inOpt:          []resultOpt{IgnoreOperationID()},
		expectFatalMsg: "results did not contain a result",
	}, {
		desc: "result by IPv4 prefix - found",
		inResults: []*client.OpResult{{
			OperationID: 42,
			Details: &client.OpDetailsResults{
				IPv4Prefix: "1.1.1.1/32",
			},
		}},
		inWants: []*client.OpResult{{
			Details: &client.OpDetailsResults{
				IPv4Prefix: "1.1.1.1/32",
			},
		}},
		inOpt: []resultOpt{IgnoreOperationID()},
	}, {
		desc: "result by IPv4 prefix - missing",
		inResults: []*client.OpResult{{
			OperationID: 42,
			Details: &client.OpDetailsResults{
				IPv4Prefix: "1.1.1.1/32",
			},
		}},
		inWants: []*client.OpResult{{
			Details: &client.OpDetailsResults{
				IPv4Prefix: "4.4.4.0/24",
			},
		}},
		inOpt:          []resultOpt{IgnoreOperationID()},
		expectFatalMsg: "results did not contain a result",
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			if tt.expectFatalMsg != "" {
				got := testt.ExpectFatal(t, func(t testing.TB) {
					HasResultsCache(t, tt.inResults, tt.inWants, tt.inOpt...)
				})
				if !strings.Contains(got, tt.expectFatalMsg) {
					t.Fatalf("did not get expected fatal message, but test called Fatal, got: %s, want: %s", got, tt.expectFatalMsg)
				}
				return
			}
			HasResultsCache(t, tt.inResults, tt.inWants, tt.inOpt...)
		})
	}
}

func TestGetResponseHasEntries(t *testing.T) {
	tests := []struct {
		desc           string
		inGetRes       *spb.GetResponse
		inWants        []fluent.GRIBIEntry
		expectFatalMsg string
	}{{
		desc: "found IPv4 entry in default",
		inGetRes: &spb.GetResponse{
			Entry: []*spb.AFTEntry{{
				NetworkInstance: "default",
				Entry: &spb.AFTEntry_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix: "1.1.1.1/32",
					},
				},
			}},
		},
		inWants: []fluent.GRIBIEntry{
			fluent.IPv4Entry().WithNetworkInstance("default").WithPrefix("1.1.1.1/32"),
		},
	}, {
		desc: "missing IPv4 entry in default",
		inGetRes: &spb.GetResponse{
			Entry: []*spb.AFTEntry{{
				NetworkInstance: "default",
				Entry: &spb.AFTEntry_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix: "1.1.1.1/32",
					},
				},
			}},
		},
		inWants: []fluent.GRIBIEntry{
			fluent.IPv4Entry().WithNetworkInstance("default").WithPrefix("2.2.2.2/32"),
		},
		expectFatalMsg: `did not find ipv4: prefix:"2.2.2.2/32"`,
	}, {
		desc: "missing IPv4 entry - missing NI",
		inGetRes: &spb.GetResponse{
			Entry: []*spb.AFTEntry{{
				NetworkInstance: "FOO",
				Entry: &spb.AFTEntry_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix: "1.1.1.1/32",
					},
				},
			}},
		},
		inWants: []fluent.GRIBIEntry{
			fluent.IPv4Entry().WithNetworkInstance("default").WithPrefix("1.1.1.1/32"),
		},
		expectFatalMsg: `did not find network instance in want: entry:{network_instance:"FOO"`,
	}, {
		desc: "missing IPv4 entry - known NI - prefix not in this NI",
		inGetRes: &spb.GetResponse{
			Entry: []*spb.AFTEntry{{
				NetworkInstance: "FOO",
				Entry: &spb.AFTEntry_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix: "1.1.1.1/32",
					},
				},
			}, {
				NetworkInstance: "default",
				Entry: &spb.AFTEntry_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix: "2.2.2.2/32",
					},
				},
			}},
		},
		inWants: []fluent.GRIBIEntry{
			fluent.IPv4Entry().WithNetworkInstance("default").WithPrefix("1.1.1.1/32"),
		},
		expectFatalMsg: `did not find entry, did not find ipv4: prefix:"1.1.1.1/32"`,
	}, {
		desc: "missing NHG entry",
		inGetRes: &spb.GetResponse{
			Entry: []*spb.AFTEntry{{
				NetworkInstance: "default",
				Entry: &spb.AFTEntry_NextHopGroup{
					NextHopGroup: &aftpb.Afts_NextHopGroupKey{
						Id: 42,
					},
				},
			}},
		},
		inWants: []fluent.GRIBIEntry{
			fluent.NextHopGroupEntry().WithNetworkInstance("default").WithID(1000),
		},
		expectFatalMsg: `did not find entry, did not find nexthop group`,
	}, {
		desc: "found NHG entry",
		inGetRes: &spb.GetResponse{
			Entry: []*spb.AFTEntry{{
				NetworkInstance: "default",
				Entry: &spb.AFTEntry_NextHopGroup{
					NextHopGroup: &aftpb.Afts_NextHopGroupKey{
						Id: 42,
					},
				},
			}},
		},
		inWants: []fluent.GRIBIEntry{
			fluent.NextHopGroupEntry().WithNetworkInstance("default").WithID(42),
		},
	}, {
		desc: "missing NH entry",
		inGetRes: &spb.GetResponse{
			Entry: []*spb.AFTEntry{{
				NetworkInstance: "default",
				Entry: &spb.AFTEntry_NextHop{
					NextHop: &aftpb.Afts_NextHopKey{
						Index: 42,
					},
				},
			}},
		},
		inWants: []fluent.GRIBIEntry{
			fluent.NextHopEntry().WithNetworkInstance("default").WithIndex(728),
		},
		expectFatalMsg: `did not find entry, did not find nexthop`,
	}, {
		desc: "found NH entry",
		inGetRes: &spb.GetResponse{
			Entry: []*spb.AFTEntry{{
				NetworkInstance: "default",
				Entry: &spb.AFTEntry_NextHop{
					NextHop: &aftpb.Afts_NextHopKey{
						Index: 728,
					},
				},
			}},
		},
		inWants: []fluent.GRIBIEntry{
			fluent.NextHopEntry().WithNetworkInstance("default").WithIndex(728),
		},
	}, {
		desc: "no network instance in want",
		inWants: []fluent.GRIBIEntry{
			fluent.IPv4Entry(),
		},
		expectFatalMsg: `got nil network instance`,
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			if tt.expectFatalMsg != "" {
				got := testt.ExpectFatal(t, func(t testing.TB) {
					GetResponseHasEntries(t, tt.inGetRes, tt.inWants...)
				})
				if !strings.Contains(got, tt.expectFatalMsg) {
					t.Fatalf("did not get expected fatal message, but test called Fatal, got: %s, want: %s", got, tt.expectFatalMsg)
				}
				return
			}
			GetResponseHasEntries(t, tt.inGetRes, tt.inWants...)
		})
	}
}

func TestHasRecvClientErrorWithStatus(t *testing.T) {
	tests := []struct {
		desc           string
		inError        error
		inWant         *status.Status
		inOpts         []ErrorOpt
		expectFatalMsg string
	}{{
		desc: "has error - message checked",
		inError: &client.ClientErr{
			Recv: []error{
				status.New(codes.InvalidArgument, "bad argument").Err(),
			},
		},
		inWant: status.New(codes.InvalidArgument, "bad argument"),
	}, {
		desc: "has error - message not checked",
		inError: &client.ClientErr{
			Recv: []error{
				status.New(codes.Aborted, "message that is ignored").Err(),
			},
		},
		inWant: status.New(codes.Aborted, ""),
	}, {
		desc:           "does not have error",
		inError:        &client.ClientErr{},
		inWant:         status.New(codes.NotFound, ""),
		expectFatalMsg: "client does not have receive error with status code:5",
	}, {
		desc: "has error - details specified",
		inError: func() error {
			s, err := status.New(codes.InvalidArgument, "bad argument").WithDetails(&spb.ModifyRPCErrorDetails{
				Reason: spb.ModifyRPCErrorDetails_ELECTION_ID_IN_ALL_PRIMARY,
			})
			if err != nil {
				panic("invalid generated error")
			}
			return &client.ClientErr{
				Recv: []error{s.Err()},
			}
		}(),
		inWant: func() *status.Status {
			s, err := status.New(codes.InvalidArgument, "bad argument").WithDetails(&spb.ModifyRPCErrorDetails{
				Reason: spb.ModifyRPCErrorDetails_ELECTION_ID_IN_ALL_PRIMARY,
			})
			if err != nil {
				panic("invalid generated error")
			}
			return s
		}(),
	}, {
		desc: "mismatched details specified",
		inError: func() error {
			s, err := status.New(codes.InvalidArgument, "bad argument").WithDetails(&spb.ModifyRPCErrorDetails{
				Reason: spb.ModifyRPCErrorDetails_MODIFY_NOT_ALLOWED,
			})
			if err != nil {
				panic("invalid generated error")
			}
			return &client.ClientErr{
				Recv: []error{s.Err()},
			}
		}(),
		inWant: func() *status.Status {
			s, err := status.New(codes.InvalidArgument, "bad argument").WithDetails(&spb.ModifyRPCErrorDetails{
				Reason: spb.ModifyRPCErrorDetails_ELECTION_ID_IN_ALL_PRIMARY,
			})
			if err != nil {
				panic("invalid generated error")
			}
			return s
		}(),
		expectFatalMsg: `client does not have receive error with status code:3`,
	}, {
		desc: "allow unimplemented specified",
		inError: &client.ClientErr{
			Recv: []error{
				status.New(codes.Unimplemented, "").Err(),
			},
		},
		inWant: status.New(codes.InvalidArgument, ""),
		inOpts: []ErrorOpt{AllowUnimplemented()},
	}, {
		desc: "allow unimplemented when details are specified",
		inError: func() error {
			s, err := status.New(codes.Unimplemented, "").WithDetails(&spb.ModifyRPCErrorDetails{
				Reason: spb.ModifyRPCErrorDetails_MODIFY_NOT_ALLOWED,
			})
			if err != nil {
				panic("invalid generated error")
			}
			return &client.ClientErr{
				Recv: []error{s.Err()},
			}
		}(),
		inWant: status.New(codes.InvalidArgument, ""),
		inOpts: []ErrorOpt{AllowUnimplemented()},
	}, {
		desc: "mismatched details specified but ignored",
		inError: func() error {
			s, err := status.New(codes.InvalidArgument, "bad argument").WithDetails(&spb.ModifyRPCErrorDetails{
				Reason: spb.ModifyRPCErrorDetails_MODIFY_NOT_ALLOWED,
			})
			if err != nil {
				panic("invalid generated error")
			}
			return &client.ClientErr{
				Recv: []error{s.Err()},
			}
		}(),
		inWant: status.New(codes.InvalidArgument, "bad argument"),
		inOpts: []ErrorOpt{IgnoreDetails()},
	}, {
		desc: "allow unimplemented specified with details in expected error",
		inError: func() error {
			s, err := status.New(codes.Unimplemented, "didn't write this yet").WithDetails(&spb.ModifyRPCErrorDetails{
				Reason: spb.ModifyRPCErrorDetails_UNKNOWN,
			})
			if err != nil {
				panic("invalid generated error")
			}
			return &client.ClientErr{
				Recv: []error{s.Err()},
			}
		}(),
		inWant: func() *status.Status {
			s, err := status.New(codes.InvalidArgument, "bad argument").WithDetails(&spb.ModifyRPCErrorDetails{
				Reason: spb.ModifyRPCErrorDetails_ELECTION_ID_IN_ALL_PRIMARY,
			})
			if err != nil {
				panic("invalid generated error")
			}
			return s
		}(),
		inOpts: []ErrorOpt{AllowUnimplemented()},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			if tt.expectFatalMsg != "" {
				got := testt.ExpectFatal(t, func(t testing.TB) {
					HasRecvClientErrorWithStatus(t, tt.inError, tt.inWant, tt.inOpts...)
				})
				if !strings.Contains(got, tt.expectFatalMsg) {
					t.Fatalf("did not get expected fatal message, but test called Fatal, got: %s, want: %s", got, tt.expectFatalMsg)
				}
				return
			}
			HasRecvClientErrorWithStatus(t, tt.inError, tt.inWant, tt.inOpts...)
		})
	}
}

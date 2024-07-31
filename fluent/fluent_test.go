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

package fluent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/openconfig/gribigo/server"
	"github.com/openconfig/gribigo/testcommon"
	"github.com/openconfig/lemming"
	"github.com/openconfig/testt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	aftpb "github.com/openconfig/gribi/v1/proto/gribi_aft"
	enums "github.com/openconfig/gribi/v1/proto/gribi_aft/enums"
	spb "github.com/openconfig/gribi/v1/proto/service"
	wpb "github.com/openconfig/ygot/proto/ywrapper"
)

func TestGRIBIClient(t *testing.T) {
	tests := []struct {
		desc string
		// inFn defines a test case which takes the argument of a gRIBI server's
		// address and returns an error if the function fails.
		//
		// The function could be externally defined (i.e., this could call a library
		// of functional tests for gRIBI to test the fake server, and ensure that the
		// Fluent API works as expected).
		inFn         func(string, testing.TB)
		wantFatalMsg string
		wantErrorMsg string
	}{{
		desc: "simple connection between client and server",
		inFn: func(addr string, t testing.TB) {
			c := NewClient()
			c.Connection().WithTarget(addr)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			c.Start(ctx, t)
			c.Stop(t)
		},
	}, {
		desc: "simple connection to invalid server",
		inFn: func(_ string, t testing.TB) {
			c := NewClient()
			c.Connection().WithTarget("http\r://foo.com/")
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			c.Start(ctx, t)
			c.Stop(t)
		},
		wantFatalMsg: "cannot dial target",
	}, {
		desc: "unsuccessful connection - check converges",
		inFn: func(addr string, t testing.TB) {
			c := NewClient()
			c.Connection().WithTarget(addr).WithRedundancyMode(AllPrimaryClients)
			c.Start(context.Background(), t)
			c.StartSending(context.Background(), t)
			// NB: we discard the error here, this test case is just to check we are
			// marked converged.
			c.Await(context.Background(), t)
			c.Stop(t)
		},
	}, {
		desc: "write basic IPv4 entry",
		inFn: func(addr string, t testing.TB) {
			c := NewClient()
			c.Connection().WithTarget(addr).WithRedundancyMode(ElectedPrimaryClient).WithInitialElectionID(0, 1).WithPersistence()
			c.Start(context.Background(), t)
			c.Modify().AddEntry(t, NextHopEntry().WithNetworkInstance(server.DefaultNetworkInstanceName).WithIndex(1).WithIPAddress("2.2.2.2"))
			c.Modify().AddEntry(t, NextHopGroupEntry().WithNetworkInstance(server.DefaultNetworkInstanceName).WithID(1).AddNextHop(1, 1))
			c.Modify().AddEntry(t, IPv4Entry().WithPrefix("1.1.1.1/32").WithNetworkInstance(server.DefaultNetworkInstanceName).WithNextHopGroup(1))
			c.StartSending(context.Background(), t)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			c.Await(ctx, t)

			for _, op := range c.Status(t).Results {
				if op.ProgrammingResult == spb.AFTResult_FAILED {
					t.Errorf("got unexpected failed programming, %v", op)
				}
			}

			c.Stop(t)
		},
	}, {
		desc: "remove basic next-hop",
		inFn: func(addr string, t testing.TB) {
			c := NewClient()
			c.Connection().WithTarget(addr).WithRedundancyMode(ElectedPrimaryClient).WithInitialElectionID(0, 1).WithPersistence()
			c.Start(context.Background(), t)
			nh := NextHopEntry().WithNetworkInstance(server.DefaultNetworkInstanceName).WithIndex(1).WithIPAddress("1.1.1.1")
			c.Modify().AddEntry(t, nh)
			c.Modify().DeleteEntry(t, nh)
			c.StartSending(context.Background(), t)

			// NB: we don't actually check any of the return values here, we
			// just check that we are marked converged.
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			c.Await(ctx, t)

			s := c.Status(t)
			if len(s.SendErrs) != 0 || len(s.ReadErrs) != 0 {
				t.Fatalf("got unexpected errors, %+v", s)
			}

			for _, op := range s.Results {
				if op.ProgrammingResult == spb.AFTResult_FAILED {
					t.Errorf("got unexpected failed programming, %v", op)
				}
			}

			c.Stop(t)
		},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			creds, err := lemming.WithTLSCredsFromFile(testcommon.TLSCreds())
			if err != nil {
				t.Fatalf("cannot load credentials, got err: %v", err)
			}

			d, err := lemming.New("DUT", "", creds, lemming.WithGNMIAddr(":0"), lemming.WithGRIBIAddr(":0"))
			if err != nil {
				t.Fatalf("cannot start server, %v", err)
			}

			if tt.wantFatalMsg != "" {
				if got := testt.ExpectFatal(t, func(t testing.TB) {
					tt.inFn(d.GRIBIAddr(), t)
				}); !strings.Contains(got, tt.wantFatalMsg) {
					t.Fatalf("did not get expected fatal error, got: %s, want: %s", got, tt.wantFatalMsg)
				}
				return
			}

			if tt.wantErrorMsg != "" {
				if got := testt.ExpectError(t, func(t testing.TB) {
					tt.inFn(d.GRIBIAddr(), t)
				}); !strings.Contains(strings.Join(got, " "), tt.wantErrorMsg) {
					t.Fatalf("did not get expected error, got: %s, want: %s", got, tt.wantErrorMsg)
				}
			}

			// Any unexpected error will be caught by being called directly on t from the fluent library.
			tt.inFn(d.GRIBIAddr(), t)

			// TODO(robjs): check error when https://github.com/openconfig/lemming/pull/226 is submitted.
			d.Stop()
		})
	}
}

func TestEntry(t *testing.T) {
	tests := []struct {
		desc           string
		in             GRIBIEntry
		wantOpProto    *spb.AFTOperation
		wantEntryProto *spb.AFTEntry
		wantOpErr      bool
		wantEntryErr   bool
	}{{
		desc: "prefix populated only",
		in:   IPv4Entry().WithPrefix("1.1.1.1/32"),
		wantOpProto: &spb.AFTOperation{
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix:    "1.1.1.1/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
				},
			},
		},
		wantEntryProto: &spb.AFTEntry{
			Entry: &spb.AFTEntry_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix:    "1.1.1.1/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
				},
			},
		},
	}, {
		desc: "prefix, nhg, ni",
		in:   IPv4Entry().WithPrefix("1.1.1.1/32").WithNetworkInstance("DEFAULT").WithNextHopGroup(42),
		wantOpProto: &spb.AFTOperation{
			NetworkInstance: "DEFAULT",
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix: "1.1.1.1/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{
						NextHopGroup: &wpb.UintValue{
							Value: 42,
						},
					},
				},
			},
		},
		wantEntryProto: &spb.AFTEntry{
			NetworkInstance: "DEFAULT",
			Entry: &spb.AFTEntry_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix: "1.1.1.1/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{
						NextHopGroup: &wpb.UintValue{
							Value: 42,
						},
					},
				},
			},
		},
	}, {
		desc: "next-hop-group",
		in:   NextHopGroupEntry().WithID(42).WithNetworkInstance("DEFAULT").AddNextHop(1, 32).AddNextHop(2, 16),
		wantOpProto: &spb.AFTOperation{
			NetworkInstance: "DEFAULT",
			Entry: &spb.AFTOperation_NextHopGroup{
				NextHopGroup: &aftpb.Afts_NextHopGroupKey{
					Id: 42,
					NextHopGroup: &aftpb.Afts_NextHopGroup{
						NextHop: []*aftpb.Afts_NextHopGroup_NextHopKey{{
							Index: 1,
							NextHop: &aftpb.Afts_NextHopGroup_NextHop{
								Weight: &wpb.UintValue{Value: 32},
							},
						}, {
							Index: 2,
							NextHop: &aftpb.Afts_NextHopGroup_NextHop{
								Weight: &wpb.UintValue{Value: 16},
							},
						}},
					},
				},
			},
		},
		wantEntryProto: &spb.AFTEntry{
			NetworkInstance: "DEFAULT",
			Entry: &spb.AFTEntry_NextHopGroup{
				NextHopGroup: &aftpb.Afts_NextHopGroupKey{
					Id: 42,
					NextHopGroup: &aftpb.Afts_NextHopGroup{
						NextHop: []*aftpb.Afts_NextHopGroup_NextHopKey{{
							Index: 1,
							NextHop: &aftpb.Afts_NextHopGroup_NextHop{
								Weight: &wpb.UintValue{Value: 32},
							},
						}, {
							Index: 2,
							NextHop: &aftpb.Afts_NextHopGroup_NextHop{
								Weight: &wpb.UintValue{Value: 16},
							},
						}},
					},
				},
			},
		},
	}, {
		desc: "next-hop",
		in: NextHopEntry().
			WithNetworkInstance("DEFAULT").WithIndex(1).
			WithIPAddress("198.51.100.1").
			WithSubinterfaceRef("Ethernet5/2", 1982).
			WithMacAddress("12:34:56:78:9a:bc").
			WithIPinIP("192.0.2.111", "192.0.2.222"),
		wantOpProto: &spb.AFTOperation{
			NetworkInstance: "DEFAULT",
			Entry: &spb.AFTOperation_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index: 1,
					NextHop: &aftpb.Afts_NextHop{
						IpAddress: &wpb.StringValue{Value: "198.51.100.1"},
						InterfaceRef: &aftpb.Afts_NextHop_InterfaceRef{
							Interface:    &wpb.StringValue{Value: "Ethernet5/2"},
							Subinterface: &wpb.UintValue{Value: 1982},
						},
						MacAddress: &wpb.StringValue{Value: "12:34:56:78:9a:bc"},
						IpInIp: &aftpb.Afts_NextHop_IpInIp{
							SrcIp: &wpb.StringValue{Value: "192.0.2.111"},
							DstIp: &wpb.StringValue{Value: "192.0.2.222"},
						},
					},
				},
			},
		},
		wantEntryProto: &spb.AFTEntry{
			NetworkInstance: "DEFAULT",
			Entry: &spb.AFTEntry_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index: 1,
					NextHop: &aftpb.Afts_NextHop{
						IpAddress: &wpb.StringValue{Value: "198.51.100.1"},
						InterfaceRef: &aftpb.Afts_NextHop_InterfaceRef{
							Interface:    &wpb.StringValue{Value: "Ethernet5/2"},
							Subinterface: &wpb.UintValue{Value: 1982},
						},
						MacAddress: &wpb.StringValue{Value: "12:34:56:78:9a:bc"},
						IpInIp: &aftpb.Afts_NextHop_IpInIp{
							SrcIp: &wpb.StringValue{Value: "192.0.2.111"},
							DstIp: &wpb.StringValue{Value: "192.0.2.222"},
						},
					},
				},
			},
		},
	}, {
		desc: "next-hop interface only",
		in: NextHopEntry().
			WithNetworkInstance("DEFAULT").WithIndex(1).
			WithInterfaceRef("Ethernet5/2"),
		wantOpProto: &spb.AFTOperation{
			NetworkInstance: "DEFAULT",
			Entry: &spb.AFTOperation_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index: 1,
					NextHop: &aftpb.Afts_NextHop{
						InterfaceRef: &aftpb.Afts_NextHop_InterfaceRef{
							Interface: &wpb.StringValue{Value: "Ethernet5/2"},
						},
					},
				},
			},
		},
		wantEntryProto: &spb.AFTEntry{
			NetworkInstance: "DEFAULT",
			Entry: &spb.AFTEntry_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index: 1,
					NextHop: &aftpb.Afts_NextHop{
						InterfaceRef: &aftpb.Afts_NextHop_InterfaceRef{
							Interface: &wpb.StringValue{Value: "Ethernet5/2"},
						},
					},
				},
			},
		},
	}, {
		desc: "nhg with a backup",
		in:   NextHopGroupEntry().WithID(42).WithBackupNHG(84),
		wantOpProto: &spb.AFTOperation{
			Entry: &spb.AFTOperation_NextHopGroup{
				NextHopGroup: &aftpb.Afts_NextHopGroupKey{
					Id: 42,
					NextHopGroup: &aftpb.Afts_NextHopGroup{
						BackupNextHopGroup: &wpb.UintValue{Value: 84},
					},
				},
			},
		},
		wantEntryProto: &spb.AFTEntry{
			Entry: &spb.AFTEntry_NextHopGroup{
				NextHopGroup: &aftpb.Afts_NextHopGroupKey{
					Id: 42,
					NextHopGroup: &aftpb.Afts_NextHopGroup{
						BackupNextHopGroup: &wpb.UintValue{Value: 84},
					},
				},
			},
		},
	}, {
		desc: "next-hop with decap",
		in:   NextHopEntry().WithNetworkInstance("DEFAULT").WithIndex(1).WithDecapsulateHeader(IPinIP).WithEncapsulateHeader(IPinIP),
		wantOpProto: &spb.AFTOperation{
			NetworkInstance: "DEFAULT",
			Entry: &spb.AFTOperation_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index: 1,
					NextHop: &aftpb.Afts_NextHop{
						DecapsulateHeader: enums.OpenconfigAftTypesEncapsulationHeaderType_OPENCONFIGAFTTYPESENCAPSULATIONHEADERTYPE_IPV4,
						EncapsulateHeader: enums.OpenconfigAftTypesEncapsulationHeaderType_OPENCONFIGAFTTYPESENCAPSULATIONHEADERTYPE_IPV4,
					},
				},
			},
		},
		wantEntryProto: &spb.AFTEntry{
			NetworkInstance: "DEFAULT",
			Entry: &spb.AFTEntry_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index: 1,
					NextHop: &aftpb.Afts_NextHop{
						DecapsulateHeader: enums.OpenconfigAftTypesEncapsulationHeaderType_OPENCONFIGAFTTYPESENCAPSULATIONHEADERTYPE_IPV4,
						EncapsulateHeader: enums.OpenconfigAftTypesEncapsulationHeaderType_OPENCONFIGAFTTYPESENCAPSULATIONHEADERTYPE_IPV4,
					},
				},
			},
		},
	}, {
		desc: "mpls entry",
		in:   LabelEntry().WithNetworkInstance("DEFAULT").WithLabel(42).WithNextHopGroup(1).WithNextHopGroupNetworkInstance("DEFAULT").WithPoppedLabelStack(10, 20),
		wantOpProto: &spb.AFTOperation{
			NetworkInstance: "DEFAULT",
			Entry: &spb.AFTOperation_Mpls{
				Mpls: &aftpb.Afts_LabelEntryKey{
					Label: &aftpb.Afts_LabelEntryKey_LabelUint64{LabelUint64: 42},
					LabelEntry: &aftpb.Afts_LabelEntry{
						NextHopGroup:                &wpb.UintValue{Value: 1},
						NextHopGroupNetworkInstance: &wpb.StringValue{Value: "DEFAULT"},
						PoppedMplsLabelStack: []*aftpb.Afts_LabelEntry_PoppedMplsLabelStackUnion{
							{PoppedMplsLabelStackUint64: 10},
							{PoppedMplsLabelStackUint64: 20},
						},
					},
				},
			},
		},
		wantEntryProto: &spb.AFTEntry{
			NetworkInstance: "DEFAULT",
			Entry: &spb.AFTEntry_Mpls{
				Mpls: &aftpb.Afts_LabelEntryKey{
					Label: &aftpb.Afts_LabelEntryKey_LabelUint64{LabelUint64: 42},
					LabelEntry: &aftpb.Afts_LabelEntry{
						NextHopGroup:                &wpb.UintValue{Value: 1},
						NextHopGroupNetworkInstance: &wpb.StringValue{Value: "DEFAULT"},
						PoppedMplsLabelStack: []*aftpb.Afts_LabelEntry_PoppedMplsLabelStackUnion{
							{PoppedMplsLabelStackUint64: 10},
							{PoppedMplsLabelStackUint64: 20},
						},
					},
				},
			},
		},
	}, {
		desc: "ipv6 entry",
		in:   IPv6Entry().WithNetworkInstance("DEFAULT").WithPrefix("2001:db8::/42").WithNextHopGroup(1).WithNextHopGroupNetworkInstance("DEFAULT").WithMetadata([]byte{1, 2, 3, 4}),
		wantOpProto: &spb.AFTOperation{
			NetworkInstance: "DEFAULT",
			Entry: &spb.AFTOperation_Ipv6{
				Ipv6: &aftpb.Afts_Ipv6EntryKey{
					Prefix: "2001:db8::/42",
					Ipv6Entry: &aftpb.Afts_Ipv6Entry{
						NextHopGroup:                &wpb.UintValue{Value: 1},
						NextHopGroupNetworkInstance: &wpb.StringValue{Value: "DEFAULT"},
						EntryMetadata:               &wpb.BytesValue{Value: []byte{1, 2, 3, 4}},
					},
				},
			},
		},
		wantEntryProto: &spb.AFTEntry{
			NetworkInstance: "DEFAULT",
			Entry: &spb.AFTEntry_Ipv6{
				Ipv6: &aftpb.Afts_Ipv6EntryKey{
					Prefix: "2001:db8::/42",
					Ipv6Entry: &aftpb.Afts_Ipv6Entry{
						NextHopGroup:                &wpb.UintValue{Value: 1},
						NextHopGroupNetworkInstance: &wpb.StringValue{Value: "DEFAULT"},
						EntryMetadata:               &wpb.BytesValue{Value: []byte{1, 2, 3, 4}},
					},
				},
			},
		},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			gotop, err := tt.in.OpProto()
			if (err != nil) != tt.wantOpErr {
				t.Fatalf("did not get expected error for op, got: %v, wantErr? %v", err, tt.wantOpErr)
			}
			if diff := cmp.Diff(gotop, tt.wantOpProto, protocmp.Transform()); diff != "" {
				t.Fatalf("did not get expected Operation proto, diff(-got,+want):\n%s", diff)
			}

			gotent, err := tt.in.EntryProto()
			if (err != nil) != tt.wantEntryErr {
				t.Fatalf("did not get expected error for entry, got: %v, wantErr? %v", err, tt.wantEntryErr)
			}
			if diff := cmp.Diff(gotent, tt.wantEntryProto, protocmp.Transform()); diff != "" {
				t.Fatalf("did not get expected Entry proto, diff(-got,+want)\n%s", diff)
			}
		})
	}
}

func TestEntriesToModifyRequest(t *testing.T) {
	tests := []struct {
		desc              string
		inClient          *GRIBIClient
		inOp              spb.AFTOperation_Operation
		inEntries         []GRIBIEntry
		wantModifyRequest *spb.ModifyRequest
		wantErr           bool
	}{{
		desc: "one ipv4 entry",
		inOp: spb.AFTOperation_ADD,
		inEntries: []GRIBIEntry{
			IPv4Entry().WithPrefix("1.1.1.1/32").WithNextHopGroup(42),
		},
		wantModifyRequest: &spb.ModifyRequest{
			Operation: []*spb.AFTOperation{{
				Id: 1,
				Op: spb.AFTOperation_ADD,
				Entry: &spb.AFTOperation_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix: "1.1.1.1/32",
						Ipv4Entry: &aftpb.Afts_Ipv4Entry{
							NextHopGroup: &wpb.UintValue{
								Value: 42,
							},
						},
					},
				},
			}},
		},
	}, {
		desc: "multiple entries",
		inOp: spb.AFTOperation_ADD,
		inEntries: []GRIBIEntry{
			IPv4Entry().WithPrefix("1.1.1.1/32").WithNextHopGroup(42),
			IPv4Entry().WithPrefix("2.2.2.2/32").WithNetworkInstance("TE-VRF").WithNextHopGroup(42),
		},
		wantModifyRequest: &spb.ModifyRequest{
			Operation: []*spb.AFTOperation{{
				Id: 1,
				Op: spb.AFTOperation_ADD,
				Entry: &spb.AFTOperation_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix: "1.1.1.1/32",
						Ipv4Entry: &aftpb.Afts_Ipv4Entry{
							NextHopGroup: &wpb.UintValue{
								Value: 42,
							},
						},
					},
				},
			}, {
				Id:              2,
				Op:              spb.AFTOperation_ADD,
				NetworkInstance: "TE-VRF",
				Entry: &spb.AFTOperation_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix: "2.2.2.2/32",
						Ipv4Entry: &aftpb.Afts_Ipv4Entry{
							NextHopGroup: &wpb.UintValue{
								Value: 42,
							},
						},
					},
				},
			}},
		},
	}, {
		desc: "set election ID",
		inClient: &GRIBIClient{
			connection: &gRIBIConnection{
				redundMode: ElectedPrimaryClient,
			},
			currentElectionID: &spb.Uint128{
				High: 42,
				Low:  42,
			},
		},
		inOp: spb.AFTOperation_ADD,
		inEntries: []GRIBIEntry{
			IPv4Entry().WithPrefix("8.8.4.4/32").WithNextHopGroup(15169),
		},
		wantModifyRequest: &spb.ModifyRequest{
			Operation: []*spb.AFTOperation{{
				Id: 1,
				Op: spb.AFTOperation_ADD,
				ElectionId: &spb.Uint128{
					High: 42,
					Low:  42,
				},
				Entry: &spb.AFTOperation_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix: "8.8.4.4/32",
						Ipv4Entry: &aftpb.Afts_Ipv4Entry{
							NextHopGroup: &wpb.UintValue{
								Value: 15169,
							},
						},
					},
				},
			}},
		},
	}, {
		desc: "one NH entry delete with only index",
		inOp: spb.AFTOperation_DELETE,
		inEntries: []GRIBIEntry{
			NextHopEntry().WithNetworkInstance("DEFAULT").WithIndex(1),
		},
		wantModifyRequest: &spb.ModifyRequest{
			Operation: []*spb.AFTOperation{{
				Id:              1,
				NetworkInstance: "DEFAULT",
				Op:              spb.AFTOperation_DELETE,
				Entry: &spb.AFTOperation_NextHop{
					NextHop: &aftpb.Afts_NextHopKey{
						Index: 1,
					},
				},
			}},
		},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			if tt.inClient == nil {
				tt.inClient = NewClient()
			}
			g := tt.inClient.Modify()
			got, err := g.entriesToModifyRequest(tt.inOp, tt.inEntries)
			if (err != nil) != tt.wantErr {
				t.Fatalf("did not get expected error, got: %v, wantErr? %v", got, tt.wantErr)
			}
			if diff := cmp.Diff(got, tt.wantModifyRequest, protocmp.Transform()); diff != "" {
				t.Fatalf("did not get expected ModifyRequest, diff(-got,+want):\n%s", diff)
			}
		})
	}
}

func TestModifyError(t *testing.T) {
	tests := []struct {
		desc       string
		in         *modifyError
		wantError  *modifyError
		wantStatus *status.Status
	}{{
		desc: "error with code",
		in:   ModifyError().WithCode(codes.InvalidArgument).WithReason(UnsupportedParameters),
		wantError: &modifyError{
			Reason: spb.ModifyRPCErrorDetails_UNSUPPORTED_PARAMS,
			Code:   codes.InvalidArgument,
		},
		wantStatus: func() *status.Status {
			s, _ := status.New(codes.InvalidArgument, "").WithDetails(&spb.ModifyRPCErrorDetails{
				Reason: spb.ModifyRPCErrorDetails_UNSUPPORTED_PARAMS,
			})
			return s
		}(),
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			if diff := cmp.Diff(tt.in, tt.wantError, cmp.AllowUnexported()); diff != "" {
				t.Fatalf("did not get expected internal error, diff(-got,+want):\n%s", diff)
			}
			if diff := cmp.Diff(tt.in.AsStatus(t), tt.wantStatus, cmpopts.IgnoreUnexported(status.Status{})); diff != "" {
				t.Fatalf("did not get expected status.proto, diff(-got,+want):\n%s", diff)
			}
		})
	}
}

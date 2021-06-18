package fluent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/openconfig/gribigo/device"
	"github.com/openconfig/gribigo/negtest"
	"github.com/openconfig/gribigo/server"
	"github.com/openconfig/gribigo/testcommon"
	wpb "github.com/openconfig/ygot/proto/ywrapper"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	aftpb "github.com/openconfig/gribi/v1/proto/gribi_aft"
	spb "github.com/openconfig/gribi/v1/proto/service"
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
		},
	}, {
		desc: "simple connection to invalid server",
		inFn: func(_ string, t testing.TB) {
			c := NewClient()
			c.Connection().WithTarget("some.failing.dns.name:noport")
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			c.Start(ctx, t)
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
		},
	}, {
		desc: "write basic IPv4 entry",
		inFn: func(addr string, t testing.TB) {
			c := NewClient()
			c.Connection().WithTarget(addr).WithRedundancyMode(ElectedPrimaryClient).WithInitialElectionID(0, 1).WithPersistence()
			c.Start(context.Background(), t)
			c.Modify().AddEntry(t, IPv4Entry().WithPrefix("1.1.1.1/32").WithNetworkInstance(server.DefaultNetworkInstanceName).WithNextHopGroup(42))
			c.StartSending(context.Background(), t)
			c.Await(context.Background(), t)
		},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			creds, err := device.TLSCredsFromFile(testcommon.TLSCreds())
			if err != nil {
				t.Fatalf("cannot load credentials, got err: %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			d, err := device.New(ctx, creds)

			if err != nil {
				t.Fatalf("cannot start server, %v", err)
			}

			if tt.wantFatalMsg != "" {
				if got := negtest.ExpectFatal(t, func(t testing.TB) {
					tt.inFn(d.GRIBIAddr(), t)
				}); !strings.Contains(got, tt.wantFatalMsg) {
					t.Fatalf("did not get expected fatal error, got: %s, want: %s", got, tt.wantFatalMsg)
				}
				return
			}

			if tt.wantErrorMsg != "" {
				if got := negtest.ExpectError(t, func(t testing.TB) {
					tt.inFn(d.GRIBIAddr(), t)
				}); !strings.Contains(got, tt.wantErrorMsg) {
					t.Fatalf("did not get expected error, got: %s, want: %s", got, tt.wantErrorMsg)
				}
			}

			// Any unexpected error will be caught by being called directly on t from the fluent library.
			tt.inFn(d.GRIBIAddr(), t)
		})
	}
}

func TestEntry(t *testing.T) {
	tests := []struct {
		desc      string
		in        gRIBIEntry
		wantProto *spb.AFTOperation
		wantErr   bool
	}{{
		desc: "prefix populated only",
		in:   IPv4Entry().WithPrefix("1.1.1.1/32"),
		wantProto: &spb.AFTOperation{
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix:    "1.1.1.1/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
				},
			},
		},
	}, {
		desc: "prefix, nhg, ni",
		in:   IPv4Entry().WithPrefix("1.1.1.1/32").WithNetworkInstance("DEFAULT").WithNextHopGroup(42),
		wantProto: &spb.AFTOperation{
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
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got, err := tt.in.proto()
			if (err != nil) != tt.wantErr {
				t.Fatalf("did not get expected error, got: %v, wantErr? %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(got, tt.wantProto, protocmp.Transform()); diff != "" {
				t.Fatalf("did not get expected proto, diff(-got,+want):\n%s", diff)
			}
		})
	}
}

func TestEntriesToModifyRequest(t *testing.T) {
	tests := []struct {
		desc              string
		inClient          *gRIBIClient
		inOp              spb.AFTOperation_Operation
		inEntries         []gRIBIEntry
		wantModifyRequest *spb.ModifyRequest
		wantErr           bool
	}{{
		desc: "one ipv4 entry",
		inOp: spb.AFTOperation_ADD,
		inEntries: []gRIBIEntry{
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
		inEntries: []gRIBIEntry{
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
		inClient: &gRIBIClient{
			connection: &gRIBIConnection{
				redundMode: ElectedPrimaryClient,
			},
			currentElectionID: &spb.Uint128{
				High: 42,
				Low:  42,
			},
		},
		inOp: spb.AFTOperation_ADD,
		inEntries: []gRIBIEntry{
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

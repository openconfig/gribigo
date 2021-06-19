package compliance

import (
	"context"
	"strings"
	"testing"

	"github.com/openconfig/gribigo/device"
	"github.com/openconfig/gribigo/negtest"
	"github.com/openconfig/gribigo/testcommon"
)

func TestCompliance(t *testing.T) {
	tests := []struct {
		in           Test
		wantFatalMsg string
		wantErrorMsg string
	}{{
		in: Test{
			Fn:        ModifyConnection,
			ShortName: "Modify RPC connection",
		},
	}, {
		in: Test{
			Fn:        ModifyConnectionWithElectionID,
			ShortName: "Modify RPC Connection with Election ID",
		},
	}, {
		in: Test{
			Fn:        ModifyConnectionSinglePrimaryPreserve,
			ShortName: "Modify RPC Connection with invalid persist/redundancy parameters",
		},
	}, {
		in: Test{
			Fn:        AddIPv4EntryRIBACK,
			ShortName: "Add IPv4 entry that can be programmed on the server - with RIB ACK",
		},
	}, {
		in: Test{
			Fn:        AddIPv4EntryFIBACK,
			ShortName: "Add IPv4 entry that can be programmed on the server - with FIB ACK",
		},
	}, {
		in: Test{
			Fn:        AddUnreferencedNextHopGroupFIBACK,
			ShortName: "Add next-hop-group entry that can be resolved on the server, no referencing IPv4 entries - with RIB ACK",
		},
	}, {
		in: Test{
			Fn:        AddUnreferencedNextHopGroupRIBACK,
			ShortName: "Add next-hop-group entry that can be resolved on the server, no referencing IPv4 entries - with FIB ACK",
		},
	}, {
		in: Test{
			Fn:        AddIPv4EntryRandom,
			ShortName: "Add IPv4 entries that are resolved by NHG and NH, in random order",
		},
	}}

	for _, tt := range tests {
		t.Run(tt.in.ShortName, func(t *testing.T) {
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
					tt.in.Fn(d.GRIBIAddr(), t)
				}); !strings.Contains(got, tt.wantFatalMsg) {
					t.Fatalf("did not get expected fatal error, got: %s, want: %s", got, tt.wantFatalMsg)
				}
				return
			}

			if tt.wantErrorMsg != "" {
				if got := negtest.ExpectError(t, func(t testing.TB) {
					tt.in.Fn(d.GRIBIAddr(), t)
				}); !strings.Contains(got, tt.wantErrorMsg) {
					t.Fatalf("did not get expected error, got: %s, want: %s", got, tt.wantErrorMsg)
				}
			}

			// Any unexpected error will be caught by being called directly on t from the fluent library.
			tt.in.Fn(d.GRIBIAddr(), t)
		})
	}
}

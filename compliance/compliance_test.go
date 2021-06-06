package compliance

import (
	"strings"
	"testing"

	"github.com/openconfig/gribigo/negtest"
	"github.com/openconfig/gribigo/testcommon"
)

func TestModifyConnectionParameters(t *testing.T) {
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
	}}

	for _, tt := range tests {
		t.Run(tt.in.ShortName, func(t *testing.T) {
			startServer, addr := testcommon.Server()
			go startServer()

			if tt.wantFatalMsg != "" {
				if got := negtest.ExpectFatal(t, func(t testing.TB) {
					tt.in.Fn(addr, t)
				}); !strings.Contains(got, tt.wantFatalMsg) {
					t.Fatalf("did not get expected fatal error, got: %s, want: %s", got, tt.wantFatalMsg)
				}
				return
			}

			if tt.wantErrorMsg != "" {
				if got := negtest.ExpectError(t, func(t testing.TB) {
					tt.in.Fn(addr, t)
				}); !strings.Contains(got, tt.wantErrorMsg) {
					t.Fatalf("did not get expected error, got: %s, want: %s", got, tt.wantErrorMsg)
				}
			}

			// Any unexpected error will be caught by being called directly on t from the fluent library.
			tt.in.Fn(addr, t)
		})
	}
}

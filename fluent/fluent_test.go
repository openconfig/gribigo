package fluent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/openconfig/gribigo/negtest"
	"github.com/openconfig/gribigo/testcommon"
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
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			startServer, addr := testcommon.Server()
			go startServer()

			if tt.wantFatalMsg != "" {
				if got := negtest.ExpectFatal(t, func(t testing.TB) {
					tt.inFn(addr, t)
				}); !strings.Contains(got, tt.wantFatalMsg) {
					t.Fatalf("did not get expected fatal error, got: %s, want: %s", got, tt.wantFatalMsg)
				}
				return
			}

			if tt.wantErrorMsg != "" {
				if got := negtest.ExpectError(t, func(t testing.TB) {
					tt.inFn(addr, t)
				}); !strings.Contains(got, tt.wantErrorMsg) {
					t.Fatalf("did not get expected error, got: %s, want: %s", got, tt.wantErrorMsg)
				}
			}

			// Any unexpected error will be caught by being called directly on t from the fluent library.
			tt.inFn(addr, t)
		})
	}
}

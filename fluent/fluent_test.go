package fluent

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	log "github.com/golang/glog"
	"github.com/openconfig/gribigo/negtest"
	"github.com/openconfig/gribigo/server"
	"google.golang.org/grpc"

	spb "github.com/openconfig/gribi/v1/proto/service"
)

// testServer starts a new gRIBI server using the fake from this repository and returns
// a function to start the server listening, a string indicating its listen address.
func testServer() (func(), string) {
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		// We panic here since this allows the test code to be much cleaner :-)
		panic(fmt.Sprintf("cannot create server, %v", err))
	}

	s := grpc.NewServer()
	spb.RegisterGRIBIServer(s, server.New())
	log.Infof("new server listening at %s", l.Addr().String())
	return func() {
		if err := s.Serve(l); err != nil {
			panic(fmt.Sprintf("server listen failed, %v", err))
		}
	}, l.Addr().String()
}

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
			c := NewClient().WithTarget(addr)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			c.Start(ctx, t)
		},
	}, {
		desc: "simple connection to invalid server",
		inFn: func(_ string, t testing.TB) {
			c := NewClient().WithTarget("some.failing.dns.name:noport")
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			c.Start(ctx, t)
		},
		wantFatalMsg: "cannot dial target",
	}, {
		desc: "simple connection and modify RPC",
		inFn: func(addr string, t testing.TB) {
			c := NewClient().WithTarget(addr)
			c.Start(context.Background(), t)
			c.StartSending(context.Background(), t)
			// TODO(robjs): add a check against the actual return value
			// for this test, rather than just there being no errors returned.
			time.Sleep(2 * time.Second)
		},
	}, {
		desc: "connection with an election ID",
		inFn: func(addr string, t testing.TB) {
			c := NewClient().WithTarget(addr).WithInitialElectionID(0, 1).WithRedundancyMode(ElectedPrimaryClient)
			c.Start(context.Background(), t)
			c.StartSending(context.Background(), t)
			// TODO(robjs): also check that we get the right return message.
			time.Sleep(2 * time.Second)
		},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			startServer, addr := testServer()
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

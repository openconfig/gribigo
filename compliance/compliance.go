// Package compliance encapsulates a set of compliance tests for gRIBI. It is a test only
// library. All tests are of the form func(address string, t testing.TB) where the address
// is the address that should be dialed for the test. The testing.TB object is used to report
// errors.
package compliance

import (
	"context"
	"fmt"
	"testing"

	"github.com/openconfig/gribigo/chk"
	"github.com/openconfig/gribigo/client"
	"github.com/openconfig/gribigo/fluent"
	"google.golang.org/grpc/codes"
)

// Test describes a test within the compliance library.
type Test struct {
	Fn          func(string, testing.TB)
	Description string
	ShortName   string
}

// ModifyConnection is a test that connects to a gRIBI server at addr and opens a Modify RPC. It determines
// that there is no response from the server.
func ModifyConnection(addr string, t testing.TB) {
	c := fluent.NewClient()
	c.Connection().WithTarget(addr)
	c.Start(context.Background(), t)
	c.StartSending(context.Background(), t)
	c.Await(context.Background(), t)
	// We get results, and just expected that there are none, because we did not
	// send anything to the server.
	if r := c.Results(t); len(r) != 0 {
		t.Fatalf("did not get expected number of return messages, got: %d (%v), want: 0", len(r), r)
	}
}

// ModifyConnectionWithElectionID is a test that connects to a gRIBI server at addr and opens a Modify RPC, with
// initial SessionParameters. It determines that there is a successful response from the server for the
// election ID.
func ModifyConnectionWithElectionID(addr string, t testing.TB) {
	c := fluent.NewClient()
	c.Connection().WithTarget(addr).WithInitialElectionID(1, 0).WithRedundancyMode(fluent.ElectedPrimaryClient).WithPersistence()
	c.Start(context.Background(), t)
	c.StartSending(context.Background(), t)
	if err := c.Await(context.Background(), t); err != nil {
		t.Fatalf("got unexpected error on client, %v", err)
	}
	res := c.Results(t)

	fmt.Printf("got %v\n", res)

	if !chk.HasElectionID(res, 1, 0) {
		t.Errorf("did not get expected election ID, got: %v, want: ElectionID=1", res)
	}

	if !chk.HasSuccessfulSessionParams(res) {
		t.Errorf("did not get expected successful session params, got: %v, want: SessionParams=OK", res)
	}
}

// ModifyConnectionSinglePrimaryPreserve is a test that connects to a gRIBI server at addr and requests
// ALL_PRIMARY mode with persistence enabled. This is expected to be an erroneous combination and hence it
// checks that the server returns an error that specifies unsupported parameters and the failed precondition
// code.
func ModifyConnectionSinglePrimaryPreserve(addr string, t testing.TB) {
	c := fluent.NewClient()
	c.Connection().WithTarget(addr).WithRedundancyMode(fluent.AllPrimaryClients).WithPersistence()
	c.Start(context.Background(), t)
	c.StartSending(context.Background(), t)
	err := c.Await(context.Background(), t)
	if err == nil {
		t.Fatalf("did not get expected error from server, got: nil")
	}
	ce, ok := err.(*client.ClientErr)
	if !ok {
		t.Fatalf("error returned from client was not expected type, got: %T, want: *client.ClientError", err)
	}
	if len(ce.Send) != 0 {
		t.Fatalf("got unexpected send errors, got: %v, want: none", ce.Send)
	}
	if num := len(ce.Recv); num != 1 {
		t.Fatalf("got wrong number of recv errors, got: %d (%v), want: 1", num, ce.Recv)
	}

	if want := fluent.ModifyError().WithCode(codes.FailedPrecondition).WithReason(fluent.UnsupportedParameters).AsStatus(t); !chk.HasRecvClientErrorWithStatus(ce, want) {
		t.Fatalf("did not get expected error type, got: %v, want: %v", ce, want)
	}
}

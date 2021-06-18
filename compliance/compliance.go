// Package compliance encapsulates a set of compliance tests for gRIBI. It is a test only
// library. All tests are of the form func(address string, t testing.TB) where the address
// is the address that should be dialed for the test. The testing.TB object is used to report
// errors.
package compliance

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/openconfig/gribigo/chk"
	"github.com/openconfig/gribigo/client"
	"github.com/openconfig/gribigo/constants"
	"github.com/openconfig/gribigo/fluent"
	"github.com/openconfig/gribigo/server"
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
	err := c.Await(context.Background(), t)
	if err != nil {
		t.Fatalf("got unexpected error on client, %v", err)
	}

	chk.HasResult(t, c.Results(t),
		fluent.OperationResult().
			WithCurrentServerElectionID(1, 0).
			AsResult(),
	)

	chk.HasResult(t, c.Results(t),
		fluent.OperationResult().
			WithSuccessfulSessionParams().
			AsResult(),
	)
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

	chk.HasNSendErrors(t, err, 0)
	chk.HasNRecvErrors(t, err, 1)

	want := fluent.
		ModifyError().
		WithCode(codes.FailedPrecondition).
		WithReason(fluent.UnsupportedParameters).
		AsStatus(t)

	chk.HasRecvClientErrorWithStatus(t, err, want)
}

// AddIPv4EntryRIBACK adds a simple IPv4 Entry which references a next-hop-group
// to the gRIBI server, requesting a RIB-level ACK.
func AddIPv4EntryRIBACK(addr string, t testing.TB) {
	addIPv4Internal(addr, t, fluent.InstalledInRIB)
}

// AddIPv4EntryFIBACK adds a simple IPv4 Entry which references a next-hop-group
// to the gRIBI server, requesting a FIB-level ACK.
func AddIPv4EntryFIBACK(addr string, t testing.TB) {
	addIPv4Internal(addr, t, fluent.InstalledInFIB)
}

// AddUnreferencedNextHopGroupRIBACK adds an unreferenced next-hop-group that contains
// nexthops to the gRIBI server, requesting a FIB-level ACK.
func AddUnreferencedNextHopGroupRIBACK(addr string, t testing.TB) {
	addNextHopGroupInternal(addr, t, fluent.InstalledInRIB)
}

// AddUnreferencedNextHopGroupFIBACK adds an unreferenced next-hop-group that contains
// nexthops to the gRIBI server, requesting a FIB-level ACK.
func AddUnreferencedNextHopGroupFIBACK(addr string, t testing.TB) {
	addNextHopGroupInternal(addr, t, fluent.InstalledInFIB)
}

// addIPv4Internal is an internal test that adds IPv4 entries, and checks
// whether the specified FIB ack is received.
func addIPv4Internal(addr string, t testing.TB, wantACK fluent.ProgrammingResult) {
	c := fluent.NewClient()
	ops := []func(){
		func() {
			c.Modify().AddEntry(t, fluent.NextHopEntry().WithNetworkInstance(server.DefaultNetworkInstanceName).WithIndex(1))
		},
		func() {
			c.Modify().AddEntry(t, fluent.NextHopGroupEntry().WithNetworkInstance(server.DefaultNetworkInstanceName).WithID(42).AddNextHop(1, 1))
		},
		func() {
			c.Modify().AddEntry(t, fluent.IPv4Entry().WithPrefix("1.1.1.1/32").WithNetworkInstance(server.DefaultNetworkInstanceName).WithNextHopGroup(42))
		},
	}

	res := doOps(c, addr, t, ops, wantACK, false)

	// Check the three entries in order.
	chk.HasResult(t, res,
		fluent.OperationResult().
			WithOperationID(1).
			WithProgrammingResult(wantACK).
			AsResult(),
	)

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithOperationID(2).
			WithProgrammingResult(wantACK).
			AsResult(),
	)

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithOperationID(3).
			WithProgrammingResult(wantACK).
			AsResult(),
	)

	// TODO(robjs): add gNMI subscription using generated telemetry library.
}

// addIPv4Random adds an IPv4 Entry, shuffling the order of the entries, and
// validating those entries are ACKed.
func AddIPv4EntryRandom(addr string, t testing.TB) {
	c := fluent.NewClient()
	ops := []func(){
		func() {
			c.Modify().AddEntry(t, fluent.NextHopEntry().WithNetworkInstance(server.DefaultNetworkInstanceName).WithIndex(1))
		},
		func() {
			c.Modify().AddEntry(t, fluent.NextHopGroupEntry().WithNetworkInstance(server.DefaultNetworkInstanceName).WithID(42).AddNextHop(1, 1))
		},
		func() {
			c.Modify().AddEntry(t, fluent.IPv4Entry().WithPrefix("1.1.1.1/32").WithNetworkInstance(server.DefaultNetworkInstanceName).WithNextHopGroup(42))
		},
	}

	res := doOps(c, addr, t, ops, fluent.InstalledInRIB, true)

	// Check the three entries in order.
	chk.HasResult(t, res,
		fluent.OperationResult().
			WithIPv4Operation("1.1.1.1/32").
			WithProgrammingResult(fluent.InstalledInRIB).
			WithOperationType(constants.ADD).
			AsResult(),
		chk.IgnoreOperationID(),
	)

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithNextHopGroupOperation(42).
			WithProgrammingResult(fluent.InstalledInRIB).
			WithOperationType(constants.ADD).
			AsResult(),
		chk.IgnoreOperationID(),
	)

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithNextHopOperation(1).
			WithProgrammingResult(fluent.InstalledInRIB).
			WithOperationType(constants.ADD).
			AsResult(),
		chk.IgnoreOperationID(),
	)

	// TODO(robjs): add gNMI subscription using generated telemetry library.
}

// doOps performs the series of operations in ops using the context client c. The
// address specified by addr is dialed. wantACK specifies the ACK type to request
// from the server, and randomise specifies whether the operations should be
// sent in order, or randomised.
//
// If the caller sets randomise to true, the client MUST NOT, rely on the operation
// ID to validate the entries, since this is allocated internally to the client.
func doOps(c *fluent.GRIBIClient, addr string, t testing.TB, ops []func(), wantACK fluent.ProgrammingResult, randomise bool) []*client.OpResult {
	conn := c.Connection().WithTarget(addr).WithRedundancyMode(fluent.ElectedPrimaryClient).WithInitialElectionID(0, 1).WithPersistence()

	if wantACK == fluent.InstalledInFIB {
		conn.WithFIBACK()
	}

	ctx := context.Background()
	c.Start(ctx, t)
	defer c.Stop(t)

	// If randomise is specified, we go and do the operations in a random order.
	// In this case, the caller MUST
	if randomise {
		rand.Seed(time.Now().UnixNano())
		rand.Shuffle(len(ops), func(i, j int) { ops[i], ops[j] = ops[j], ops[i] })
	}

	for _, fn := range ops {
		fn()
	}

	c.StartSending(ctx, t)
	err := c.Await(ctx, t)
	if err != nil {
		t.Fatalf("got unexpected error from server, got: %v, want: nil", err)
	}
	return c.Results(t)
}

// addNextHopGroupInternal is an internal implementation that checks that a
// next-hop-group can be added to the gRIBI server with the specified ACK mode.
// The tests does not install an IPv4Entry, so these NHGs are unreferenced.
// We still expect an ACK in this case.
func addNextHopGroupInternal(addr string, t testing.TB, wantACK fluent.ProgrammingResult) {
	c := fluent.NewClient()
	ops := []func(){
		func() {
			c.Modify().AddEntry(t, fluent.NextHopEntry().WithNetworkInstance(server.DefaultNetworkInstanceName).WithIndex(1))
		},
		func() {
			c.Modify().AddEntry(t, fluent.NextHopGroupEntry().WithNetworkInstance(server.DefaultNetworkInstanceName).WithID(42).AddNextHop(1, 1))
		},
	}

	res := doOps(c, addr, t, ops, wantACK, false)

	// Check the three entries in order.
	chk.HasResult(t, res,
		fluent.OperationResult().
			WithOperationID(1).
			WithProgrammingResult(wantACK).
			AsResult(),
	)

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithOperationID(2).
			WithProgrammingResult(wantACK).
			AsResult(),
	)
}

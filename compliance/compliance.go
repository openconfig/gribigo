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

// Package compliance encapsulates a set of compliance tests for gRIBI. It is a test only
// library. All tests are of the form func(c *fluent.GRIBIClient, t testing.TB) where the
// client is the one that should be used for the test. The testing.TB object is used to report
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
	"go.uber.org/atomic"
	"google.golang.org/grpc/codes"
)

// electionID is a atomically updated uint64 that we use for the election ID in the tests
// this ensures that we do not have tests that specify an election ID that is older than
// the last tests', and thus fail due to the state of the server.
var electionID = &atomic.Uint64{}

// Test describes a test within the compliance library.
type Test struct {
	// Fn is the function to be run for a test.
	Fn func(*fluent.GRIBIClient, testing.TB)
	// Description is a longer description of what the test does such that it can
	// be handed to a documentation generating function within the test.
	Description string
	// ShortName is a short description of the test for use in test output.
	ShortName string
	// Reference is a unique reference to external data (e.g., test plans) used for the test.
	Reference string
}

// TestSpec is a description of a test.
type TestSpec struct {
	// In is the specification of the test to be run.
	In Test
	// FatalMsg is an expected t.Fatalf message that the test finds.
	FatalMsg string
	// ErrorMsg is an expected t.Errorf message that the test finds.
	ErrorMsg string
}

var (
	// TestSuite is the library of tests that can be run for compliance.
	TestSuite = []*TestSpec{{
		In: Test{
			Fn:        ModifyConnection,
			ShortName: "Modify RPC connection",
		},
	}, {
		In: Test{
			Fn:        ModifyConnectionWithElectionID,
			ShortName: "Modify RPC Connection with Election ID",
		},
	}, {
		In: Test{
			Fn:        ModifyConnectionSinglePrimaryPreserve,
			ShortName: "Modify RPC Connection with invalid persist/redundancy parameters",
		},
	}, {
		In: Test{
			Fn:        AddIPv4EntryRIBACK,
			Reference: "TE-2.1.1.1",
			ShortName: "Add IPv4 entry that can be programmed on the server - with RIB ACK",
		},
	}, {
		In: Test{
			Fn:        AddIPv4EntryFIBACK,
			Reference: "TE-2.1.1.2",
			ShortName: "Add IPv4 entry that can be programmed on the server - with FIB ACK",
		},
	}, {
		In: Test{
			Fn:        AddUnreferencedNextHopGroupFIBACK,
			ShortName: "Add next-hop-group entry that can be resolved on the server, no referencing IPv4 entries - with RIB ACK",
		},
	}, {
		In: Test{
			Fn:        AddUnreferencedNextHopGroupRIBACK,
			ShortName: "Add next-hop-group entry that can be resolved on the server, no referencing IPv4 entries - with FIB ACK",
		},
	}, {
		In: Test{
			Fn:        AddIPv4EntryRandom,
			ShortName: "Add IPv4 entries that are resolved by NHG and NH, in random order",
		},
	}, {
		In: Test{
			Fn:        AddIPv4ToMultipleNHsSingleRequest,
			ShortName: "Add IPv4 entries that are resolved to a next-hop-group containing multiple next-hops (single ModifyRequest)",
			Reference: "TE-2.1.2.1",
		},
	}, {
		In: Test{
			Fn:        AddIPv4ToMultipleNHsMultipleRequests,
			ShortName: "Add IPv4 entries that are resolved to a next-hop-group containing multiple next-hops (multiple ModifyRequests)",
			Reference: "TE-2.1.2.2",
		},
	}, {
		In: Test{
			Fn:        DeleteIPv4Entry,
			ShortName: "Delete IPv4 entry within default network instance",
		},
	}, {
		In: Test{
			Fn:        DeleteReferencedNHGFailure,
			ShortName: "Delete NHG entry that is referenced - failure",
		},
	}, {
		In: Test{
			Fn:        DeleteReferencedNHFailure,
			ShortName: "Delete NH entry that is referenced - failure",
		},
	}, {
		In: Test{
			Fn:        DeleteNextHopGroup,
			ShortName: "Delete NHG entry successfully",
		},
	}, {
		In: Test{
			Fn:        DeleteNextHop,
			ShortName: "Delete NH entry successfully",
		},
	}}
)

// ModifyConnection is a test that opens a Modify RPC. It determines
// that there is no response from the server.
func ModifyConnection(c *fluent.GRIBIClient, t testing.TB) {
	c.Start(context.Background(), t)
	defer c.Stop(t)
	c.StartSending(context.Background(), t)
	c.Await(context.Background(), t)
	// We get results, and just expected that there are none, because we did not
	// send anything to the server.
	if r := c.Results(t); len(r) != 0 {
		t.Fatalf("did not get expected number of return messages, got: %d (%v), want: 0", len(r), r)
	}
}

// ModifyConnectionWithElectionID is a test that opens a Modify RPC,
// with initial SessionParameters. It determines that there is a
// successful response from the server for the election ID.
func ModifyConnectionWithElectionID(c *fluent.GRIBIClient, t testing.TB) {
	defer electionID.Inc()
	c.Connection().WithInitialElectionID(electionID.Load(), 0).WithRedundancyMode(fluent.ElectedPrimaryClient).WithPersistence()
	c.Start(context.Background(), t)
	defer c.Stop(t)
	c.StartSending(context.Background(), t)
	err := c.Await(context.Background(), t)
	if err != nil {
		t.Fatalf("got unexpected error on client, %v", err)
	}

	chk.HasResult(t, c.Results(t),
		fluent.OperationResult().
			WithCurrentServerElectionID(electionID.Load(), 0).
			AsResult(),
	)

	chk.HasResult(t, c.Results(t),
		fluent.OperationResult().
			WithSuccessfulSessionParams().
			AsResult(),
	)
}

// ModifyConnectionSinglePrimaryPreserve is a test that requests
// ALL_PRIMARY mode with persistence enabled. This is expected to be
// an erroneous combination and hence it checks that the server
// returns an error that specifies unsupported parameters and the
// failed precondition code.
func ModifyConnectionSinglePrimaryPreserve(c *fluent.GRIBIClient, t testing.TB) {
	c.Connection().WithRedundancyMode(fluent.AllPrimaryClients).WithPersistence()
	c.Start(context.Background(), t)
	defer c.Stop(t)
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
func AddIPv4EntryRIBACK(c *fluent.GRIBIClient, t testing.TB) {
	addIPv4Internal(c, t, fluent.InstalledInRIB)
}

// AddIPv4EntryFIBACK adds a simple IPv4 Entry which references a next-hop-group
// to the gRIBI server, requesting a FIB-level ACK.
func AddIPv4EntryFIBACK(c *fluent.GRIBIClient, t testing.TB) {
	addIPv4Internal(c, t, fluent.InstalledInFIB)
}

// AddUnreferencedNextHopGroupRIBACK adds an unreferenced next-hop-group that contains
// nexthops to the gRIBI server, requesting a FIB-level ACK.
func AddUnreferencedNextHopGroupRIBACK(c *fluent.GRIBIClient, t testing.TB) {
	addNextHopGroupInternal(c, t, fluent.InstalledInRIB)
}

// AddUnreferencedNextHopGroupFIBACK adds an unreferenced next-hop-group that contains
// nexthops to the gRIBI server, requesting a FIB-level ACK.
func AddUnreferencedNextHopGroupFIBACK(c *fluent.GRIBIClient, t testing.TB) {
	addNextHopGroupInternal(c, t, fluent.InstalledInFIB)
}

// addIPv4Internal is an internal test that adds IPv4 entries, and checks
// whether the specified FIB ack is received.
func addIPv4Internal(c *fluent.GRIBIClient, t testing.TB, wantACK fluent.ProgrammingResult) {
	ops := []func(){
		func() {
			c.Modify().AddEntry(t, fluent.NextHopEntry().WithNetworkInstance(server.DefaultNetworkInstanceName).WithIndex(1).WithIPAddress("192.0.2.1"))
		},
		func() {
			c.Modify().AddEntry(t, fluent.NextHopGroupEntry().WithNetworkInstance(server.DefaultNetworkInstanceName).WithID(42).AddNextHop(1, 1))
		},
		func() {
			c.Modify().AddEntry(t, fluent.IPv4Entry().WithPrefix("1.1.1.1/32").WithNetworkInstance(server.DefaultNetworkInstanceName).WithNextHopGroup(42))
		},
	}

	res := doOps(c, t, ops, wantACK, false)

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
}

// addIPv4Random adds an IPv4 Entry, shuffling the order of the entries, and
// validating those entries are ACKed.
func AddIPv4EntryRandom(c *fluent.GRIBIClient, t testing.TB) {
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

	res := doOps(c, t, ops, fluent.InstalledInRIB, true)

	// Check the three entries in order.
	chk.HasResult(t, res,
		fluent.OperationResult().
			WithIPv4Operation("1.1.1.1/32").
			WithProgrammingResult(fluent.InstalledInRIB).
			WithOperationType(constants.Add).
			AsResult(),
		chk.IgnoreOperationID(),
	)

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithNextHopGroupOperation(42).
			WithProgrammingResult(fluent.InstalledInRIB).
			WithOperationType(constants.Add).
			AsResult(),
		chk.IgnoreOperationID(),
	)

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithNextHopOperation(1).
			WithProgrammingResult(fluent.InstalledInRIB).
			WithOperationType(constants.Add).
			AsResult(),
		chk.IgnoreOperationID(),
	)

	// TODO(robjs): add gNMI subscription using generated telemetry library.
}

// doOps performs the series of operations in ops using the context
// client c. wantACK specifies the ACK type to request from the
// server, and randomise specifies whether the operations should be
// sent in order, or randomised.
//
// If the caller sets randomise to true, the client MUST NOT, rely on the operation
// ID to validate the entries, since this is allocated internally to the client.
func doOps(c *fluent.GRIBIClient, t testing.TB, ops []func(), wantACK fluent.ProgrammingResult, randomise bool) []*client.OpResult {
	defer electionID.Inc()
	conn := c.Connection().WithRedundancyMode(fluent.ElectedPrimaryClient).WithInitialElectionID(electionID.Load(), 0).WithPersistence()

	if wantACK == fluent.InstalledInFIB {
		conn.WithFIBACK()
	}

	ctx := context.Background()
	c.Start(ctx, t)
	defer c.Stop(t)
	c.StartSending(ctx, t)
	if err := c.Await(ctx, t); err != nil {
		t.Fatalf("got unexpected error from server - session negotiation, got: %v, want: nil", err)
	}

	// If randomise is specified, we go and do the operations in a random order.
	// In this case, the caller MUST
	if randomise {
		rand.Seed(time.Now().UnixNano())
		rand.Shuffle(len(ops), func(i, j int) { ops[i], ops[j] = ops[j], ops[i] })
	}

	for _, fn := range ops {
		fn()
	}

	if err := c.Await(ctx, t); err != nil {
		t.Fatalf("got unexpected error from server - entries, got: %v, want: nil", err)
	}
	return c.Results(t)
}

// addNextHopGroupInternal is an internal implementation that checks that a
// next-hop-group can be added to the gRIBI server with the specified ACK mode.
// The tests does not install an IPv4Entry, so these NHGs are unreferenced.
// We still expect an ACK in this case.
func addNextHopGroupInternal(c *fluent.GRIBIClient, t testing.TB, wantACK fluent.ProgrammingResult) {
	ops := []func(){
		func() {
			c.Modify().AddEntry(t, fluent.NextHopEntry().WithNetworkInstance(server.DefaultNetworkInstanceName).WithIndex(1))
		},
		func() {
			c.Modify().AddEntry(t, fluent.NextHopGroupEntry().WithNetworkInstance(server.DefaultNetworkInstanceName).WithID(42).AddNextHop(1, 1))
		},
	}

	res := doOps(c, t, ops, wantACK, false)

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

// For the following tests, the base topology shown below is assumed.
//
//   Topology:             ________
//                        |        |
//        -----port1----- |  DUT   |-----port2----
//          192.0.2.0/31  |        |  192.0.2.2/31
//                        |        |
//                        |        |----port3-----
//                        |        |  192.0.2.4/31
//                        |________|
//
//       -------------------1.0.0.0/8-------------->
//
// As the dataplane implementation is added, the input configuration
// within the test will cover the configuration of these ports, however,
// at this time the diagram above is illustrative for tracking the tests.

// baseTopologyEntries creates the entries shown in the diagram above using
// separate ModifyRequests for each entry.
func baseTopologyEntries(c *fluent.GRIBIClient, t testing.TB) {
	c.Modify().AddEntry(t, fluent.NextHopEntry().WithNetworkInstance(server.DefaultNetworkInstanceName).WithIndex(1).WithIPAddress("192.0.2.3"))
	c.Modify().AddEntry(t, fluent.NextHopEntry().WithNetworkInstance(server.DefaultNetworkInstanceName).WithIndex(2).WithIPAddress("192.0.2.5"))
	c.Modify().AddEntry(t, fluent.NextHopGroupEntry().WithNetworkInstance(server.DefaultNetworkInstanceName).WithID(1).AddNextHop(1, 1).AddNextHop(2, 1))
	c.Modify().AddEntry(t, fluent.IPv4Entry().WithPrefix("1.0.0.0/8").WithNetworkInstance(server.DefaultNetworkInstanceName).WithNextHopGroup(1))
}

// validateBaseEntries checks that the entries in the base topology are correctly
// installed.
func validateBaseTopologyEntries(res []*client.OpResult, t testing.TB) {
	// Check for next-hops 1 and 2.
	for _, nhopID := range []uint64{1, 2} {
		chk.HasResult(t, res,
			fluent.OperationResult().
				WithNextHopOperation(nhopID).
				WithProgrammingResult(fluent.InstalledInFIB).
				WithOperationType(constants.Add).
				AsResult(),
			chk.IgnoreOperationID(),
		)
	}

	// Check for next-hop-group 1.
	chk.HasResult(t, res,
		fluent.OperationResult().
			WithNextHopGroupOperation(1).
			WithProgrammingResult(fluent.InstalledInFIB).
			WithOperationType(constants.Add).
			AsResult(),
		chk.IgnoreOperationID(),
	)

	// Check for 1/8.
	chk.HasResult(t, res,
		fluent.OperationResult().
			WithIPv4Operation("1.0.0.0/8").
			WithProgrammingResult(fluent.InstalledInFIB).
			WithOperationType(constants.Add).
			AsResult(),
		chk.IgnoreOperationID(),
	)
}

// AddIPv4ToMultipleNHsSingleRequest creates an IPv4 entry which references a NHG containing
// 2 NHs within a single ModifyRequest, validating that they are installed in the FIB.
func AddIPv4ToMultipleNHsSingleRequest(c *fluent.GRIBIClient, t testing.TB) {

	ops := []func(){
		func() {
			c.Modify().AddEntry(t,
				fluent.NextHopEntry().WithNetworkInstance(server.DefaultNetworkInstanceName).WithIndex(1).WithIPAddress("192.0.2.3"),
				fluent.NextHopEntry().WithNetworkInstance(server.DefaultNetworkInstanceName).WithIndex(2).WithIPAddress("192.0.2.5"),
				fluent.NextHopGroupEntry().WithNetworkInstance(server.DefaultNetworkInstanceName).WithID(1).AddNextHop(1, 1).AddNextHop(2, 1),
				fluent.IPv4Entry().WithPrefix("1.0.0.0/8").WithNetworkInstance(server.DefaultNetworkInstanceName).WithNextHopGroup(1))
		},
	}

	validateBaseTopologyEntries(doOps(c, t, ops, fluent.InstalledInFIB, false), t)
}

// AddIPv4ToMultipleNHsMultipleRequests creates an IPv4 entry which references a NHG containing
// 2 NHs within multiple ModifyReqests, validating that they are installed in the FIB.
func AddIPv4ToMultipleNHsMultipleRequests(c *fluent.GRIBIClient, t testing.TB) {

	ops := []func(){
		func() { baseTopologyEntries(c, t) },
	}
	validateBaseTopologyEntries(doOps(c, t, ops, fluent.InstalledInFIB, false), t)
}

// DeleteIPv4Entry deletes an IPv4 entry from the server's RIB.
func DeleteIPv4Entry(addr string, t testing.TB) {
	c := fluent.NewClient()
	ops := []func(){
		func() { baseTopologyEntries(c, t) },
		func() {
			c.Modify().DeleteEntry(t, fluent.IPv4Entry().WithPrefix("1.0.0.0/8").WithNetworkInstance(server.DefaultNetworkInstanceName))
		},
	}
	res := doOps(c, addr, t, ops, fluent.InstalledInFIB, false)
	validateBaseTopologyEntries(res, t)

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithIPv4Operation("1.0.0.0/8").
			WithOperationType(constants.Delete).
			WithProgrammingResult(fluent.InstalledInFIB).
			AsResult(),
		chk.IgnoreOperationID(),
	)
}

// DeleteReferencedNHGFailure attempts to delete a NextHopGroup entry that is referenced
// from the RIB, and expects a failure.
func DeleteReferencedNHGFailure(addr string, t testing.TB) {
	c := fluent.NewClient()
	ops := []func(){
		func() { baseTopologyEntries(c, t) },
		func() {
			c.Modify().DeleteEntry(t, fluent.NextHopGroupEntry().WithID(1).WithNetworkInstance(server.DefaultNetworkInstanceName))
		},
	}
	res := doOps(c, addr, t, ops, fluent.InstalledInFIB, false)
	validateBaseTopologyEntries(res, t)

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithNextHopGroupOperation(1).
			WithOperationType(constants.Delete).
			WithProgrammingResult(fluent.ProgrammingFailed).
			AsResult(),
		chk.IgnoreOperationID())
}

// DeleteReferencedNHFailure attempts to delete a NH entry that is referened from the RIB
// and expects a failure.
func DeleteReferencedNHFailure(addr string, t testing.TB) {
	c := fluent.NewClient()
	ops := []func(){
		func() { baseTopologyEntries(c, t) },
		func() {
			c.Modify().DeleteEntry(t, fluent.NextHopEntry().WithIndex(1).WithNetworkInstance(server.DefaultNetworkInstanceName))
		},
		func() {
			c.Modify().DeleteEntry(t, fluent.NextHopEntry().WithIndex(2).WithNetworkInstance(server.DefaultNetworkInstanceName))
		},
	}
	res := doOps(c, addr, t, ops, fluent.InstalledInFIB, false)
	validateBaseTopologyEntries(res, t)

	for _, i := range []uint64{1, 2} {
		chk.HasResult(t, res,
			fluent.OperationResult().
				WithNextHopOperation(i).
				WithOperationType(constants.Delete).
				WithProgrammingResult(fluent.ProgrammingFailed).
				AsResult(),
			chk.IgnoreOperationID())
	}
}

// DeleteNextHopGroup attempts to delete a NHG entry that is not referenced and expects
// success.
func DeleteNextHopGroup(addr string, t testing.TB) {
	c := fluent.NewClient()
	ops := []func(){
		func() { baseTopologyEntries(c, t) },
		func() {
			c.Modify().DeleteEntry(t,
				fluent.IPv4Entry().WithPrefix("1.0.0.0/8").WithNetworkInstance(server.DefaultNetworkInstanceName),
				fluent.NextHopGroupEntry().WithID(1).WithNetworkInstance(server.DefaultNetworkInstanceName),
			)
		},
	}

	res := doOps(c, addr, t, ops, fluent.InstalledInFIB, false)
	validateBaseTopologyEntries(res, t)

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithNextHopGroupOperation(1).
			WithOperationType(constants.Delete).
			WithProgrammingResult(fluent.InstalledInFIB).
			AsResult(),
		chk.IgnoreOperationID())

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithIPv4Operation("1.0.0.0/8").
			WithOperationType(constants.Delete).
			WithProgrammingResult(fluent.InstalledInFIB).
			AsResult(),
		chk.IgnoreOperationID())
}

// DeleteNextHop attempts to delete the NH entris within the base topology and expects
// success.
//
// TODO(robjs): When traffic and AFT validation is added, ensure that a partial delete
// scenario keeps traffic routed via the remaining NH.
func DeleteNextHop(addr string, t testing.TB) {
	c := fluent.NewClient()
	ops := []func(){
		func() { baseTopologyEntries(c, t) },
		func() {
			c.Modify().DeleteEntry(t,
				fluent.IPv4Entry().WithPrefix("1.0.0.0/8").WithNetworkInstance(server.DefaultNetworkInstanceName),
				fluent.NextHopGroupEntry().WithID(1).WithNetworkInstance(server.DefaultNetworkInstanceName),
				fluent.NextHopEntry().WithIndex(1).WithNetworkInstance(server.DefaultNetworkInstanceName),
				fluent.NextHopEntry().WithIndex(2).WithNetworkInstance(server.DefaultNetworkInstanceName),
			)
		},
	}

	res := doOps(c, addr, t, ops, fluent.InstalledInFIB, false)
	validateBaseTopologyEntries(res, t)

	for _, i := range []uint64{1, 2} {
		chk.HasResult(t, res,
			fluent.OperationResult().
				WithNextHopOperation(i).
				WithOperationType(constants.Delete).
				WithProgrammingResult(fluent.InstalledInFIB).
				AsResult(),
			chk.IgnoreOperationID())

	}

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithNextHopGroupOperation(1).
			WithOperationType(constants.Delete).
			WithProgrammingResult(fluent.InstalledInFIB).
			AsResult(),
		chk.IgnoreOperationID())

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithIPv4Operation("1.0.0.0/8").
			WithOperationType(constants.Delete).
			WithProgrammingResult(fluent.InstalledInFIB).
			AsResult(),
		chk.IgnoreOperationID())
}

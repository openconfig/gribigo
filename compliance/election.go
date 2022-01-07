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

package compliance

import (
	"context"
	"testing"
	"time"

	log "github.com/golang/glog"
	"github.com/openconfig/gribigo/chk"
	"github.com/openconfig/gribigo/constants"
	"github.com/openconfig/gribigo/fluent"
	"github.com/openconfig/gribigo/server"
	"google.golang.org/grpc/codes"
)

// secondClient is an option that provides for a second gRIBI client to
// be supplied to a test.
type secondClient struct {
	// c is a fluent gRIBI client.
	c *fluent.GRIBIClient
}

// IsTestOpt marks secondClient as implementing the TestOpt interface.
func (*secondClient) IsTestOpt() {}

func SecondClient(c *fluent.GRIBIClient) *secondClient {
	return &secondClient{c: c}
}

// TestUnsupportedElectionParams ensures that election parameters that are invalid -
// currently ALL_PRIMARY and an election ID are reported as an error.
func TestUnsupportedElectionParams(c *fluent.GRIBIClient, t testing.TB, _ ...TestOpt) {
	defer electionID.Inc()
	c.Connection().WithInitialElectionID(electionID.Load(), 0).WithRedundancyMode(fluent.AllPrimaryClients)
	c.Start(context.Background(), t)
	defer c.Stop(t)
	c.StartSending(context.Background(), t)

	err := awaitTimeout(context.Background(), c, t, time.Minute)
	if err == nil {
		t.Fatalf("did not get expected error from server, got: nil")
	}

	chk.HasNSendErrors(t, err, 0)
	chk.HasNRecvErrors(t, err, 1)

	chk.HasRecvClientErrorWithStatus(
		t,
		err,
		fluent.ModifyError().
			WithCode(codes.FailedPrecondition).
			WithReason(fluent.UnsupportedParameters).
			AsStatus(t),
		chk.AllowUnimplemented(),
	)
}

func clientAB(c *fluent.GRIBIClient, t testing.TB, opts ...TestOpt) (*fluent.GRIBIClient, *fluent.GRIBIClient) {
	t.Helper()
	var clientA, clientB *fluent.GRIBIClient
	clientA = c
	for _, o := range opts {
		if opt, ok := o.(*secondClient); ok {
			clientB = opt.c
		}
	}
	if clientA == nil || clientB == nil {
		t.Fatalf("cannot run test with nil clientA or clientB, clients, a: %v, b: %v", clientA, clientB)
	}
	return clientA, clientB
}

// TestDifferingElectionParameters checks that when a client A is connected with parameters
// that differ to those that are used by a new client B an error is returned to client B.
//
// opts must contain a SecondClient option such that there is a second stub to be used to
// the device.
func TestDifferingElectionParameters(c *fluent.GRIBIClient, t testing.TB, opts ...TestOpt) {
	defer electionID.Inc()

	clientA, clientB := clientAB(c, t, opts...)

	clientA.Connection().WithInitialElectionID(electionID.Load(), 0).
		WithRedundancyMode(fluent.ElectedPrimaryClient).
		WithPersistence()
	clientA.Start(context.Background(), t)
	defer clientA.Stop(t)
	clientA.StartSending(context.Background(), t)

	clientB.Connection().WithInitialElectionID(electionID.Load(), 1).
		WithRedundancyMode(fluent.ElectedPrimaryClient).
		WithPersistence().
		WithFIBACK()
	clientB.Start(context.Background(), t)
	defer clientB.Stop(t)
	clientB.StartSending(context.Background(), t)

	clientAErr := awaitTimeout(context.Background(), clientA, t, time.Minute)
	if err := clientAErr; err != nil {
		t.Fatalf("did not expect error from server in client A, got: %v", err)
	}

	clientBErr := awaitTimeout(context.Background(), clientB, t, time.Minute)
	if err := clientBErr; err == nil {
		t.Fatalf("did not get expected error from server, got: %v", err)
	}

	chk.HasNSendErrors(t, clientBErr, 0)
	chk.HasNRecvErrors(t, clientBErr, 1)

	chk.HasRecvClientErrorWithStatus(
		t,
		clientBErr,
		fluent.ModifyError().
			WithCode(codes.FailedPrecondition).
			WithReason(fluent.ParamsDifferFromOtherClients).
			AsStatus(t),
	)
}

// TestMatchingElectionParameters tests whether two clients can connect with the same
// parameters and the connection is succesful.
func TestMatchingElectionParameters(c *fluent.GRIBIClient, t testing.TB, opts ...TestOpt) {
	defer electionID.Inc()

	clientA, clientB := clientAB(c, t, opts...)

	connect := func(cc *fluent.GRIBIClient) func(testing.TB) {
		cc.Connection().WithInitialElectionID(electionID.Load(), 0).
			WithRedundancyMode(fluent.ElectedPrimaryClient).
			WithPersistence().
			WithFIBACK()
		cc.Start(context.Background(), t)
		cc.StartSending(context.Background(), t)
		// Ensure that the next client uses a higher election ID.
		electionID.Inc()
		return cc.Stop
	}

	defer connect(clientA)(t)
	defer connect(clientB)(t)

	clientAErr := awaitTimeout(context.Background(), clientA, t, time.Minute)
	if err := clientAErr; err != nil {
		t.Fatalf("did not expect error from server in client A, got: %v", err)
	}

	clientBErr := awaitTimeout(context.Background(), clientB, t, time.Minute)
	if err := clientBErr; err != nil {
		t.Fatalf("did not get expected error from server, got: %v", err)
	}

	chk.HasNSendErrors(t, clientAErr, 0)
	chk.HasNRecvErrors(t, clientAErr, 0)
	chk.HasNSendErrors(t, clientBErr, 0)
	chk.HasNRecvErrors(t, clientBErr, 0)

	chk.HasResult(t, clientA.Results(t),
		fluent.OperationResult().
			WithCurrentServerElectionID(electionID.Load()-2, 0).
			AsResult(),
	)

	chk.HasResult(t, clientB.Results(t),
		fluent.OperationResult().
			WithCurrentServerElectionID(electionID.Load()-1, 0).
			AsResult(),
	)
}

// TestLowerElectionID tests whether a client connecting with a lower election
// ID is accepted, and the election ID reported to it is the higher election ID.
func TestLowerElectionID(c *fluent.GRIBIClient, t testing.TB, opts ...TestOpt) {
	defer electionID.Add(2)

	clientA, clientB := clientAB(c, t, opts...)

	clientA.Connection().WithInitialElectionID(electionID.Load()+1, 0).
		WithRedundancyMode(fluent.ElectedPrimaryClient).
		WithPersistence().
		WithFIBACK()
	clientA.Start(context.Background(), t)
	clientA.StartSending(context.Background(), t)
	defer clientA.Stop(t)

	clientB.Connection().WithInitialElectionID(electionID.Load(), 0).
		WithRedundancyMode(fluent.ElectedPrimaryClient).
		WithPersistence().
		WithFIBACK()
	clientB.Start(context.Background(), t)
	clientB.StartSending(context.Background(), t)
	defer clientB.Stop(t)

	clientAErr := awaitTimeout(context.Background(), clientA, t, time.Minute)
	if err := clientAErr; err != nil {
		t.Fatalf("did not expect error from server in client A, got: %v", err)
	}

	clientBErr := awaitTimeout(context.Background(), clientB, t, time.Minute)
	if err := clientBErr; err != nil {
		t.Fatalf("did not get expected error from server, got: %v", err)
	}

	chk.HasNSendErrors(t, clientAErr, 0)
	chk.HasNRecvErrors(t, clientAErr, 0)
	chk.HasNSendErrors(t, clientBErr, 0)
	chk.HasNRecvErrors(t, clientBErr, 0)

	chk.HasResult(t, clientA.Results(t),
		fluent.OperationResult().
			WithCurrentServerElectionID(electionID.Load()+1, 0).
			AsResult(),
	)

	chk.HasResult(t, clientB.Results(t),
		fluent.OperationResult().
			WithCurrentServerElectionID(electionID.Load()+1, 0).
			AsResult(),
	)
}

// TestActiveAfterMasterChange tests whether entries installed by client A remain active
// when clientB connects and provides a higher election ID. The presence of an entry is
// verified through the Get RPC.
func TestActiveAfterMasterChange(c *fluent.GRIBIClient, t testing.TB, opts ...TestOpt) {
	defer electionID.Add(2)

	clientA, clientB := clientAB(c, t, opts...)

	clientA.Connection().WithInitialElectionID(electionID.Load(), 0).
		WithRedundancyMode(fluent.ElectedPrimaryClient).
		WithPersistence()
	clientA.Start(context.Background(), t)
	clientA.StartSending(context.Background(), t)
	defer clientA.Stop(t)

	clientA.Modify().AddEntry(t,
		fluent.NextHopEntry().
			WithNetworkInstance(server.DefaultNetworkInstanceName).
			WithIndex(1).
			WithIPAddress("192.0.2.1"))

	clientA.Modify().AddEntry(t,
		fluent.NextHopGroupEntry().
			WithNetworkInstance(server.DefaultNetworkInstanceName).
			WithID(42).
			AddNextHop(1, 1))

	clientA.Modify().AddEntry(t,
		fluent.IPv4Entry().
			WithPrefix("1.1.1.1/32").
			WithNetworkInstance(server.DefaultNetworkInstanceName).
			WithNextHopGroup(42))

	if err := awaitTimeout(context.Background(), clientA, t, time.Minute); err != nil {
		t.Fatalf("could not program entries via clientA, got err: %v", err)
	}

	chk.HasResult(t, clientA.Results(t),
		fluent.OperationResult().
			WithNextHopOperation(1).
			WithOperationType(constants.Add).
			WithProgrammingResult(fluent.InstalledInRIB).
			AsResult(),
		chk.IgnoreOperationID(),
	)

	chk.HasResult(t, clientA.Results(t),
		fluent.OperationResult().
			WithNextHopGroupOperation(42).
			WithOperationType(constants.Add).
			WithProgrammingResult(fluent.InstalledInRIB).
			AsResult(),
		chk.IgnoreOperationID(),
	)

	chk.HasResult(t, clientA.Results(t),
		fluent.OperationResult().
			WithIPv4Operation("1.1.1.1/32").
			WithOperationType(constants.Add).
			WithProgrammingResult(fluent.InstalledInRIB).
			AsResult(),
		chk.IgnoreOperationID(),
	)

	chkIPv4 := func(c *fluent.GRIBIClient, t testing.TB, errDetails string) {
		gr, err := c.Get().
			WithNetworkInstance(server.DefaultNetworkInstanceName).
			WithAFT(fluent.IPv4).
			Send()

		log.Infof("got Get result, %s", gr)

		if err != nil {
			t.Fatalf("could not execute Get via client (%s), %v", errDetails, err)
		}

		chk.GetResponseHasEntries(t, gr,
			fluent.IPv4Entry().
				WithNetworkInstance(server.DefaultNetworkInstanceName).
				WithNextHopGroup(42).
				WithPrefix("1.1.1.1/32"),
		)
	}

	chkIPv4(clientA, t, "clientA, prior to clientB connecting")

	// connect clientB with higher election ID.
	clientB.Connection().WithInitialElectionID(electionID.Load()+1, 0).
		WithRedundancyMode(fluent.ElectedPrimaryClient).
		WithPersistence()
	clientB.Start(context.Background(), t)
	clientB.StartSending(context.Background(), t)
	defer clientB.Stop(t)

	chkIPv4(clientA, t, "clientA, after clientB connected")
	chkIPv4(clientB, t, "clientB directly")
}

// TestNewElectionIDNoUpdateRejected checks that a client that specifies a higher election
// ID without explicitly updating the ID is rejected.
func TestNewElectionIDNoUpdateRejected(c *fluent.GRIBIClient, t testing.TB, _ ...TestOpt) {
	defer electionID.Add(2)

	c.Connection().WithInitialElectionID(electionID.Load(), 0).
		WithRedundancyMode(fluent.ElectedPrimaryClient).
		WithPersistence()
	c.Start(context.Background(), t)
	c.StartSending(context.Background(), t)
	defer c.Stop(t)

	entries := []fluent.GRIBIEntry{
		fluent.NextHopEntry().
			WithIndex(1).
			WithNetworkInstance(server.DefaultNetworkInstanceName).
			WithElectionID(electionID.Load()+1, 0),
		fluent.NextHopGroupEntry().
			WithID(1).
			WithNetworkInstance(server.DefaultNetworkInstanceName).
			AddNextHop(1, 1).
			WithElectionID(electionID.Load()+1, 0),
		fluent.IPv4Entry().
			WithPrefix("1.1.1.1/32").
			WithNetworkInstance(server.DefaultNetworkInstanceName).
			WithElectionID(electionID.Load()+1, 0),
	}

	c.Modify().AddEntry(t, entries...)

	if err := awaitTimeout(context.Background(), c, t, time.Minute); err != nil {
		t.Fatalf("could not program entries via client, got err: %v", err)
	}

	chk.HasResult(t, c.Results(t),
		fluent.OperationResult().
			WithNextHopOperation(1).
			WithOperationType(constants.Add).
			WithProgrammingResult(fluent.ProgrammingFailed).
			AsResult(),
		chk.IgnoreOperationID(),
	)

	chk.HasResult(t, c.Results(t),
		fluent.OperationResult().
			WithNextHopGroupOperation(1).
			WithOperationType(constants.Add).
			WithProgrammingResult(fluent.ProgrammingFailed).
			AsResult(),
		chk.IgnoreOperationID(),
	)

	chk.HasResult(t, c.Results(t),
		fluent.OperationResult().
			WithIPv4Operation("1.1.1.1/32").
			WithOperationType(constants.Add).
			WithProgrammingResult(fluent.ProgrammingFailed).
			AsResult(),
		chk.IgnoreOperationID(),
	)
}

// TestIncElectionID ensures that when the election ID is updated explicitly by the
// client to a higher value that the new value is accepted, and the lower values are rejected.
func TestIncElectionID(c *fluent.GRIBIClient, t testing.TB, _ ...TestOpt) {
	defer electionID.Inc()

	c.Connection().WithInitialElectionID(electionID.Load(), 0).
		WithRedundancyMode(fluent.ElectedPrimaryClient).
		WithPersistence()
	c.Start(context.Background(), t)
	c.StartSending(context.Background(), t)
	defer c.Stop(t)

	c.Modify().AddEntry(t, fluent.
		NextHopEntry().
		WithNetworkInstance(server.DefaultNetworkInstanceName).
		WithIndex(1).
		WithIPAddress("1.1.1.1"))

	if err := awaitTimeout(context.Background(), c, t, time.Minute); err != nil {
		t.Fatalf("could not program entries via client, got err: %v", err)
	}

	chk.HasResult(t, c.Results(t),
		fluent.OperationResult().
			WithNextHopOperation(1).
			WithOperationType(constants.Add).
			WithProgrammingResult(fluent.InstalledInRIB).
			AsResult(),
		chk.IgnoreOperationID(),
	)

	electionID.Inc()

	c.Modify().UpdateElectionID(t, electionID.Load(), 0)

	if err := awaitTimeout(context.Background(), c, t, time.Minute); err != nil {
		t.Fatalf("could not update election ID via client, got err: %v", err)
	}

	chk.HasResult(t, c.Results(t),
		fluent.
			OperationResult().
			WithCurrentServerElectionID(electionID.Load(), 0).
			AsResult(),
	)

	// check old ID is not honoured.
	c.Modify().AddEntry(t, fluent.
		NextHopEntry().
		WithNetworkInstance(server.DefaultNetworkInstanceName).
		WithIndex(1).
		WithIPAddress("2.2.2.2").
		WithElectionID(electionID.Load()-1, 0))

	if err := awaitTimeout(context.Background(), c, t, time.Minute); err != nil {
		t.Fatalf("could not send update with stale ID via client, got err: %v", err)
	}

	chk.HasResult(t, c.Results(t),
		fluent.OperationResult().
			WithNextHopOperation(1).
			WithOperationType(constants.Add).
			WithProgrammingResult(fluent.ProgrammingFailed).
			AsResult(),
		chk.IgnoreOperationID(),
	)

	// check new ID is honoured.
	c.Modify().AddEntry(t, fluent.
		NextHopEntry().
		WithNetworkInstance(server.DefaultNetworkInstanceName).
		WithIndex(1).
		WithIPAddress("3.3.3.3").
		WithElectionID(electionID.Load(), 0))

	if err := awaitTimeout(context.Background(), c, t, time.Minute); err != nil {
		t.Fatalf("could not send update with current ID via client, got err: %v", err)
	}

	chk.HasResult(t, c.Results(t),
		fluent.OperationResult().
			WithNextHopOperation(1).
			WithOperationType(constants.Add).
			WithProgrammingResult(fluent.InstalledInRIB).
			AsResult(),
		chk.IgnoreOperationID(),
	)
}

// TestDecElectionID validates that when a client decreases the election ID
// it is not honoured by the server, and the server reports back the highest
// ID it has seen.
func TestDecElectionID(c *fluent.GRIBIClient, t testing.TB, _ ...TestOpt) {
	defer electionID.Inc()

	// ensure that we can safely use election ID - 1
	electionID.Inc()

	c.Connection().WithInitialElectionID(electionID.Load(), 0).
		WithRedundancyMode(fluent.ElectedPrimaryClient).
		WithPersistence()
	c.Start(context.Background(), t)
	c.StartSending(context.Background(), t)
	defer c.Stop(t)

	if err := awaitTimeout(context.Background(), c, t, time.Minute); err != nil {
		t.Fatalf("could not send update with current ID via client, got err: %v", err)
	}

	chk.HasResult(t, c.Results(t),
		fluent.
			OperationResult().
			WithCurrentServerElectionID(electionID.Load(), 0).
			AsResult(),
	)

	c.Modify().UpdateElectionID(t, electionID.Load()-1, 0)

	if err := awaitTimeout(context.Background(), c, t, time.Minute); err != nil {
		t.Fatalf("could not send update with current ID via client, got err: %v", err)
	}

	chk.HasResult(t, c.Results(t),
		fluent.
			OperationResult().
			WithCurrentServerElectionID(electionID.Load(), 0).
			AsResult(),
	)
}

// TestSameElectionIDFromTwoClients is the test to start 2 clients with same election ID.
// The client A should be master and client B should be non-master. The AFT operation
// from the client B should be rejected.
func TestSameElectionIDFromTwoClients(c *fluent.GRIBIClient, t testing.TB, opts ...TestOpt) {
	defer electionID.Inc()

	clientA, clientB := clientAB(c, t, opts...)

	clientA.Connection().WithInitialElectionID(electionID.Load(), 0).
		WithRedundancyMode(fluent.ElectedPrimaryClient).WithPersistence()

	clientA.Start(context.Background(), t)
	clientA.StartSending(context.Background(), t)
	defer clientA.Stop(t)

	clientB.Connection().WithInitialElectionID(electionID.Load(), 0).
		WithRedundancyMode(fluent.ElectedPrimaryClient).WithPersistence()

	clientB.Start(context.Background(), t)
	clientB.StartSending(context.Background(), t)
	defer clientB.Stop(t)

	clientA.Modify().AddEntry(t, fluent.NextHopEntry().WithNetworkInstance(server.DefaultNetworkInstanceName).WithIndex(10).WithIPAddress("192.0.2.1"))
	clientB.Modify().AddEntry(t, fluent.NextHopEntry().WithNetworkInstance(server.DefaultNetworkInstanceName).WithIndex(10).WithIPAddress("192.0.2.1"))

	clientAErr := awaitTimeout(context.Background(), clientA, t, time.Minute)
	if err := clientAErr; err != nil {
		t.Fatalf("did not expect error from server in client A, got: %v", err)
	}

	clientBErr := awaitTimeout(context.Background(), clientB, t, time.Minute)
	if err := clientBErr; err != nil {
		t.Fatalf("did not expect error from server in client A, got: %v", err)
	}

	chk.HasNSendErrors(t, clientAErr, 0)
	chk.HasNRecvErrors(t, clientAErr, 0)
	chk.HasNSendErrors(t, clientBErr, 0)
	chk.HasNRecvErrors(t, clientBErr, 0)

	chk.HasResult(t, clientA.Results(t),
		fluent.OperationResult().
			WithCurrentServerElectionID(electionID.Load(), 0).
			AsResult(),
	)

	chk.HasResult(t, clientB.Results(t),
		fluent.OperationResult().
			WithCurrentServerElectionID(electionID.Load(), 0).
			AsResult(),
	)

	chk.HasResult(t, clientA.Results(t),
		fluent.OperationResult().
			WithOperationID(1).
			WithProgrammingResult(fluent.InstalledInRIB).
			AsResult(),
	)

	chk.HasResult(t, clientB.Results(t),
		fluent.OperationResult().
			WithOperationID(1).
			WithProgrammingResult(fluent.ProgrammingFailed).
			AsResult(),
	)
}

// TestElectionIDAsZero is the test to send (0, 0) as the election ID
// The server should respond with RPC error Invalid Argument
func TestElectionIDAsZero(c *fluent.GRIBIClient, t testing.TB, _ ...TestOpt) {
	c.Connection().WithInitialElectionID(0, 0).WithRedundancyMode(fluent.ElectedPrimaryClient).WithPersistence()
	c.Start(context.Background(), t)
	defer c.Stop(t)
	c.StartSending(context.Background(), t)

	err := awaitTimeout(context.Background(), c, t, time.Minute)
	if err == nil {
		t.Fatalf("did not get expected error from server, got: nil")
	}

	chk.HasNSendErrors(t, err, 0)
	chk.HasNRecvErrors(t, err, 1)

	chk.HasRecvClientErrorWithStatus(
		t,
		err,
		fluent.ModifyError().
			WithCode(codes.InvalidArgument).
			AsStatus(t),
		chk.IgnoreDetails(),
	)
}

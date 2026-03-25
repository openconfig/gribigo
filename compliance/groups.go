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

	"github.com/openconfig/gribigo/chk"
	"github.com/openconfig/gribigo/constants"
	"github.com/openconfig/gribigo/fluent"

	spb "github.com/openconfig/gribi/v1/proto/service"
)

// AddToGroup adds a set of entries to a specific redundancy group and validates
// that they are successfully installed.
func AddToGroup(c *fluent.GRIBIClient, wantACK fluent.ProgrammingResult, t testing.TB, _ ...TestOpt) {
	defer flushServer(c, t)
	group := "group-a"

	defer electionID.Inc()
	c.Connection().
		WithRedundancyMode(fluent.ElectedPrimaryWithOwnership).
		WithInitialElectionIDForGroup(group, electionID.Load(), 0).
		WithPersistence()

	ctx := context.Background()
	c.Start(ctx, t)
	defer c.Stop(t)
	c.StartSending(ctx, t)
	if err := awaitTimeout(ctx, c, t, time.Minute); err != nil {
		t.Fatalf("got unexpected error from server - session negotiation, got: %v, want: nil", err)
	}

	c.Modify().AddEntry(t,
		fluent.NextHopEntry().WithNetworkInstance(defaultNetworkInstanceName).WithIndex(1).WithIPAddress("192.0.2.1"),
		fluent.NextHopGroupEntry().WithNetworkInstance(defaultNetworkInstanceName).WithID(1).AddNextHop(1, 1),
		fluent.IPv4Entry().WithPrefix("1.1.1.1/32").WithNetworkInstance(defaultNetworkInstanceName).WithNextHopGroup(1),
	)

	if err := awaitTimeout(ctx, c, t, time.Minute); err != nil {
		t.Fatalf("got unexpected error from server - entries, got: %v, want: nil", err)
	}

	res := c.Results(t)
	chk.HasResult(t, res,
		fluent.OperationResult().
			WithNextHopOperation(1).
			WithProgrammingResult(wantACK).
			WithOperationType(constants.Add).
			AsResult(),
		chk.IgnoreOperationID(),
	)
	chk.HasResult(t, res,
		fluent.OperationResult().
			WithNextHopGroupOperation(1).
			WithProgrammingResult(wantACK).
			WithOperationType(constants.Add).
			AsResult(),
		chk.IgnoreOperationID(),
	)
	chk.HasResult(t, res,
		fluent.OperationResult().
			WithIPv4Operation("1.1.1.1/32").
			WithProgrammingResult(wantACK).
			WithOperationType(constants.Add).
			AsResult(),
		chk.IgnoreOperationID(),
	)
}

// DeleteFromGroup adds and then deletes entries from a redundancy group, validating
// that they are successfully removed.
func DeleteFromGroup(c *fluent.GRIBIClient, wantACK fluent.ProgrammingResult, t testing.TB, _ ...TestOpt) {
	defer flushServer(c, t)
	group := "group-a"

	defer electionID.Inc()
	c.Connection().
		WithRedundancyMode(fluent.ElectedPrimaryWithOwnership).
		WithInitialElectionIDForGroup(group, electionID.Load(), 0).
		WithPersistence()

	ctx := context.Background()
	c.Start(ctx, t)
	defer c.Stop(t)
	c.StartSending(ctx, t)
	if err := awaitTimeout(ctx, c, t, time.Minute); err != nil {
		t.Fatalf("got unexpected error from server - session negotiation, got: %v, want: nil", err)
	}

	c.Modify().AddEntry(t,
		fluent.NextHopEntry().WithNetworkInstance(defaultNetworkInstanceName).WithIndex(1).WithIPAddress("192.0.2.1"),
		fluent.NextHopGroupEntry().WithNetworkInstance(defaultNetworkInstanceName).WithID(1).AddNextHop(1, 1),
		fluent.IPv4Entry().WithPrefix("1.1.1.1/32").WithNetworkInstance(defaultNetworkInstanceName).WithNextHopGroup(1),
	)

	if err := awaitTimeout(ctx, c, t, time.Minute); err != nil {
		t.Fatalf("got unexpected error from server - add, got: %v, want: nil", err)
	}

	c.Modify().DeleteEntry(t,
		fluent.IPv4Entry().WithPrefix("1.1.1.1/32").WithNetworkInstance(defaultNetworkInstanceName),
		fluent.NextHopGroupEntry().WithNetworkInstance(defaultNetworkInstanceName).WithID(1),
		fluent.NextHopEntry().WithNetworkInstance(defaultNetworkInstanceName).WithIndex(1),
	)

	if err := awaitTimeout(ctx, c, t, time.Minute); err != nil {
		t.Fatalf("got unexpected error from server - delete, got: %v, want: nil", err)
	}

	res := c.Results(t)
	chk.HasResult(t, res,
		fluent.OperationResult().
			WithNextHopOperation(1).
			WithProgrammingResult(wantACK).
			WithOperationType(constants.Delete).
			AsResult(),
		chk.IgnoreOperationID(),
	)
	chk.HasResult(t, res,
		fluent.OperationResult().
			WithNextHopGroupOperation(1).
			WithProgrammingResult(wantACK).
			WithOperationType(constants.Delete).
			AsResult(),
		chk.IgnoreOperationID(),
	)
	chk.HasResult(t, res,
		fluent.OperationResult().
			WithIPv4Operation("1.1.1.1/32").
			WithProgrammingResult(wantACK).
			WithOperationType(constants.Delete).
			AsResult(),
		chk.IgnoreOperationID(),
	)
}

// GroupIsolation validates that entries from one group are not visible or
// modifiable by another group.
func GroupIsolation(c *fluent.GRIBIClient, t testing.TB, opts ...TestOpt) {
	defer flushServer(c, t)
	clientA, clientB := clientAB(c, t, opts...)

	defer electionID.Add(2)
	clientA.Connection().
		WithRedundancyMode(fluent.ElectedPrimaryWithOwnership).
		WithInitialElectionIDForGroup("group-a", electionID.Load(), 0).
		WithPersistence()
	clientA.Start(context.Background(), t)
	defer clientA.Stop(t)
	clientA.StartSending(context.Background(), t)
	if err := awaitTimeout(context.Background(), clientA, t, time.Minute); err != nil {
		t.Fatalf("could not connect clientA: %v", err)
	}

	clientB.Connection().
		WithRedundancyMode(fluent.ElectedPrimaryWithOwnership).
		WithInitialElectionIDForGroup("group-b", electionID.Load()+1, 0).
		WithPersistence()
	clientB.Start(context.Background(), t)
	defer clientB.Stop(t)
	clientB.StartSending(context.Background(), t)
	if err := awaitTimeout(context.Background(), clientB, t, time.Minute); err != nil {
		t.Fatalf("could not connect clientB: %v", err)
	}

	// clientA adds an entry.
	clientA.Modify().AddEntry(t,
		fluent.NextHopEntry().WithNetworkInstance(defaultNetworkInstanceName).WithIndex(1).WithIPAddress("192.0.2.1"))
	if err := awaitTimeout(context.Background(), clientA, t, time.Minute); err != nil {
		t.Fatalf("clientA add failed: %v", err)
	}

	// clientB tries to delete it, but it should fail because it's owned by clientA.
	clientB.Modify().DeleteEntry(t,
		fluent.NextHopEntry().WithNetworkInstance(defaultNetworkInstanceName).WithIndex(1))
	if err := awaitTimeout(context.Background(), clientB, t, time.Minute); err != nil {
		t.Fatalf("clientB delete failed: %v", err)
	}

	// For clientB, it should be a failure because it doesn't own the entry.
	chk.HasResult(t, clientB.Results(t),
		fluent.OperationResult().
			WithNextHopOperation(1).
			WithProgrammingResult(fluent.ProgrammingFailed).
			WithOperationType(constants.Delete).
			AsResult(),
		chk.IgnoreOperationID(),
	)

	// Verify clientA's entry still exists (via Get)
	gr, err := clientA.Get().
		WithNetworkInstance(defaultNetworkInstanceName).
		WithRedundancyGroup("group-a").
		WithAFT(fluent.NextHop).
		Send()
	if err != nil {
		t.Fatalf("clientA get failed: %v", err)
	}
	chk.GetResponseHasEntries(t, gr,
		fluent.NextHopEntry().WithNetworkInstance(defaultNetworkInstanceName).WithIndex(1).WithIPAddress("192.0.2.1"))
}

// FlushGroup validates that the Flush RPC can be used to remove entries
// belonging to a specific redundancy group.
func FlushGroup(c *fluent.GRIBIClient, wantACK fluent.ProgrammingResult, t testing.TB, _ ...TestOpt) {
	defer flushServer(c, t)
	group := "group-a"

	defer electionID.Inc()
	c.Connection().
		WithRedundancyMode(fluent.ElectedPrimaryWithOwnership).
		WithInitialElectionIDForGroup(group, electionID.Load(), 0).
		WithPersistence()

	ctx := context.Background()
	c.Start(ctx, t)
	defer c.Stop(t)
	c.StartSending(ctx, t)
	if err := awaitTimeout(ctx, c, t, time.Minute); err != nil {
		t.Fatalf("got unexpected error from server - session negotiation, got: %v, want: nil", err)
	}

	c.Modify().AddEntry(t,
		fluent.NextHopEntry().WithNetworkInstance(defaultNetworkInstanceName).WithIndex(1).WithIPAddress("192.0.2.1"))
	if err := awaitTimeout(ctx, c, t, time.Minute); err != nil {
		t.Fatalf("got unexpected error from server - entries, got: %v, want: nil", err)
	}

	// Flush the group.
	curID := electionID.Load()
	fr, err := c.Flush().
		WithRedundancyGroup(group, curID, 0).
		WithNetworkInstance(defaultNetworkInstanceName).
		Send()

	if err != nil {
		t.Fatalf("Flush failed: %v", err)
	}
	if fr.GetResult() != spb.FlushResponse_OK {
		t.Fatalf("Flush result not OK: %v", fr.GetResult())
	}

	// Verify entries are gone.
	gr, err := c.Get().
		WithNetworkInstance(defaultNetworkInstanceName).
		WithRedundancyGroup(group).
		WithAFT(fluent.NextHop).
		Send()
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if len(gr.GetEntry()) != 0 {
		t.Fatalf("Entries still exist in group after flush: %v", gr.GetEntry())
	}

	// Verify entries are also gone from the merged RIB.
	mgr, err := c.Get().
		WithNetworkInstance(defaultNetworkInstanceName).
		WithAFT(fluent.NextHop).
		Send()
	if err != nil {
		t.Fatalf("Merged Get failed: %v", err)
	}
	if len(mgr.GetEntry()) != 0 {
		t.Fatalf("Entries still exist in merged RIB after group flush: %v", mgr.GetEntry())
	}
}

// MultipleClientsInGroup validates that only the master client (highest election ID)
// in a group can program entries.
func MultipleClientsInGroup(c *fluent.GRIBIClient, t testing.TB, opts ...TestOpt) {
	defer flushServer(c, t)
	clientA, clientB := clientAB(c, t, opts...)

	defer electionID.Add(2)
	group := "group-a"

	// clientA has lower election ID.
	clientA.Connection().
		WithRedundancyMode(fluent.ElectedPrimaryWithOwnership).
		WithInitialElectionIDForGroup(group, electionID.Load(), 0).
		WithPersistence()
	clientA.Start(context.Background(), t)
	defer clientA.Stop(t)
	clientA.StartSending(context.Background(), t)
	if err := awaitTimeout(context.Background(), clientA, t, time.Minute); err != nil {
		t.Fatalf("could not connect clientA: %v", err)
	}

	// clientB has higher election ID.
	clientB.Connection().
		WithRedundancyMode(fluent.ElectedPrimaryWithOwnership).
		WithInitialElectionIDForGroup(group, electionID.Load()+1, 0).
		WithPersistence()
	clientB.Start(context.Background(), t)
	defer clientB.Stop(t)
	clientB.StartSending(context.Background(), t)
	if err := awaitTimeout(context.Background(), clientB, t, time.Minute); err != nil {
		t.Fatalf("could not connect clientB: %v", err)
	}

	// clientB is master. clientA attempts to program and should fail.
	clientA.Modify().AddEntry(t,
		fluent.NextHopEntry().WithNetworkInstance(defaultNetworkInstanceName).WithIndex(1).WithIPAddress("192.0.2.1"))
	if err := awaitTimeout(context.Background(), clientA, t, time.Minute); err != nil {
		t.Fatalf("clientA modify failed: %v", err)
	}

	chk.HasResult(t, clientA.Results(t),
		fluent.OperationResult().
			WithNextHopOperation(1).
			WithProgrammingResult(fluent.ProgrammingFailed).
			WithOperationType(constants.Add).
			AsResult(),
		chk.IgnoreOperationID(),
	)

	// clientB attempts to program and should succeed.
	clientB.Modify().AddEntry(t,
		fluent.NextHopEntry().WithNetworkInstance(defaultNetworkInstanceName).WithIndex(1).WithIPAddress("192.0.2.1"))
	if err := awaitTimeout(context.Background(), clientB, t, time.Minute); err != nil {
		t.Fatalf("clientB modify failed: %v", err)
	}

	chk.HasResult(t, clientB.Results(t),
		fluent.OperationResult().
			WithNextHopOperation(1).
			WithProgrammingResult(fluent.InstalledInRIB).
			WithOperationType(constants.Add).
			AsResult(),
		chk.IgnoreOperationID(),
	)
}

// GroupOwnershipConflict validates that two groups cannot own the same entry.
func GroupOwnershipConflict(c *fluent.GRIBIClient, t testing.TB, opts ...TestOpt) {
	defer flushServer(c, t)
	clientA, clientB := clientAB(c, t, opts...)

	defer electionID.Add(2)

	// clientA adds an entry.
	clientA.Connection().
		WithRedundancyMode(fluent.ElectedPrimaryWithOwnership).
		WithInitialElectionIDForGroup("group-a", electionID.Load(), 0).
		WithPersistence()
	clientA.Start(context.Background(), t)
	defer clientA.Stop(t)
	clientA.StartSending(context.Background(), t)
	if err := awaitTimeout(context.Background(), clientA, t, time.Minute); err != nil {
		t.Fatalf("could not connect clientA: %v", err)
	}

	clientA.Modify().AddEntry(t,
		fluent.NextHopEntry().WithNetworkInstance(defaultNetworkInstanceName).WithIndex(1).WithIPAddress("192.0.2.1"))
	if err := awaitTimeout(context.Background(), clientA, t, time.Minute); err != nil {
		t.Fatalf("clientA add failed: %v", err)
	}

	// clientB tries to add the same entry.
	clientB.Connection().
		WithRedundancyMode(fluent.ElectedPrimaryWithOwnership).
		WithInitialElectionIDForGroup("group-b", electionID.Load()+1, 0).
		WithPersistence()
	clientB.Start(context.Background(), t)
	defer clientB.Stop(t)
	clientB.StartSending(context.Background(), t)
	if err := awaitTimeout(context.Background(), clientB, t, time.Minute); err != nil {
		t.Fatalf("could not connect clientB: %v", err)
	}

	clientB.Modify().AddEntry(t,
		fluent.NextHopEntry().WithNetworkInstance(defaultNetworkInstanceName).WithIndex(1).WithIPAddress("192.0.2.2"))
	if err := awaitTimeout(context.Background(), clientB, t, time.Minute); err != nil {
		t.Fatalf("clientB add failed: %v", err)
	}

	// clientB should fail because clientA owns it.
	chk.HasResult(t, clientB.Results(t),
		fluent.OperationResult().
			WithNextHopOperation(1).
			WithProgrammingResult(fluent.ProgrammingFailed).
			WithOperationType(constants.Add).
			AsResult(),
		chk.IgnoreOperationID(),
	)
}

// MergedRIBGet validates that Get without a redundancy group returns entries from all groups.
func MergedRIBGet(c *fluent.GRIBIClient, t testing.TB, opts ...TestOpt) {
	defer flushServer(c, t)
	clientA, clientB := clientAB(c, t, opts...)

	defer electionID.Add(2)

	// Group A adds entry 1.
	clientA.Connection().
		WithRedundancyMode(fluent.ElectedPrimaryWithOwnership).
		WithInitialElectionIDForGroup("group-a", electionID.Load(), 0).
		WithPersistence()
	clientA.Start(context.Background(), t)
	defer clientA.Stop(t)
	clientA.StartSending(context.Background(), t)
	if err := awaitTimeout(context.Background(), clientA, t, time.Minute); err != nil {
		t.Fatalf("could not connect clientA: %v", err)
	}
	clientA.Modify().AddEntry(t,
		fluent.NextHopEntry().WithNetworkInstance(defaultNetworkInstanceName).WithIndex(1).WithIPAddress("192.0.2.1"))
	if err := awaitTimeout(context.Background(), clientA, t, time.Minute); err != nil {
		t.Fatalf("clientA add failed: %v", err)
	}

	// Group B adds entry 2.
	clientB.Connection().
		WithRedundancyMode(fluent.ElectedPrimaryWithOwnership).
		WithInitialElectionIDForGroup("group-b", electionID.Load()+1, 0).
		WithPersistence()
	clientB.Start(context.Background(), t)
	defer clientB.Stop(t)
	clientB.StartSending(context.Background(), t)
	if err := awaitTimeout(context.Background(), clientB, t, time.Minute); err != nil {
		t.Fatalf("could not connect clientB: %v", err)
	}
	clientB.Modify().AddEntry(t,
		fluent.NextHopEntry().WithNetworkInstance(defaultNetworkInstanceName).WithIndex(2).WithIPAddress("192.0.2.2"))
	if err := awaitTimeout(context.Background(), clientB, t, time.Minute); err != nil {
		t.Fatalf("clientB add failed: %v", err)
	}

	// Get from clientA without group should return BOTH.
	gr, err := clientA.Get().
		WithNetworkInstance(defaultNetworkInstanceName).
		WithAFT(fluent.NextHop).
		Send()
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	chk.GetResponseHasEntries(t, gr,
		fluent.NextHopEntry().WithNetworkInstance(defaultNetworkInstanceName).WithIndex(1).WithIPAddress("192.0.2.1"),
		fluent.NextHopEntry().WithNetworkInstance(defaultNetworkInstanceName).WithIndex(2).WithIPAddress("192.0.2.2"),
	)
}


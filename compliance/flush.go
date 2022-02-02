// Copyright 2022 Google LLC
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

	"github.com/openconfig/gribigo/chk"
	"github.com/openconfig/gribigo/constants"
	"github.com/openconfig/gribigo/fluent"
	"github.com/openconfig/gribigo/server"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// FlushFromMasterDefaultNI programs a chain of entries into the default NI with the
// specified wantACK mode and then issues a Flush towards the server using the current
// master's election ID. It validates that no entries remain in the default network
// instance using the Get RPC.
func FlushFromMasterDefaultNI(c *fluent.GRIBIClient, wantACK fluent.ProgrammingResult, t testing.TB, _ ...TestOpt) {
	addFlushEntriesToNI(c, server.DefaultNetworkInstanceName, wantACK, t)

	// addFlushEntriesToNI increments the election ID so to check with the current value,
	// we need to subtract one from the current election ID.
	curID := electionID.Load() - 1

	ctx := context.Background()
	c.Start(ctx, t)
	defer c.Stop(t)

	fr, err := c.Flush().
		WithElectionID(curID, 0).
		WithAllNetworkInstances().
		Send()
	switch {
	case err != nil:
		t.Fatalf("got unexpected error from flush, got: %v", err)
	case fr == nil:
		t.Fatalf("got nil response from flush, got: %v", fr)
	}

	checkNIHasNEntries(ctx, c, server.DefaultNetworkInstanceName, 0, t)
}

// FlushFromOverrideDefaultNI programs a chain of entries into the default NI with the
// specified wantACK mode, and then issues a Flush towards the server specifying that
// the election ID should be overridden and ensures that entries are removed using the
// Get RPC.
func FlushFromOverrideDefaultNI(c *fluent.GRIBIClient, wantACK fluent.ProgrammingResult, t testing.TB, _ ...TestOpt) {
	addFlushEntriesToNI(c, server.DefaultNetworkInstanceName, wantACK, t)

	ctx := context.Background()
	c.Start(ctx, t)
	defer c.Stop(t)

	fr, err := c.Flush().
		WithElectionOverride().
		WithAllNetworkInstances().
		Send()
	switch {
	case err != nil:
		t.Fatalf("got unexpected error from flush, got: %v", err)
	case fr == nil:
		t.Fatalf("got nil response from flush, got: %v", fr)
	}

	checkNIHasNEntries(ctx, c, server.DefaultNetworkInstanceName, 0, t)
}

// FlushFromNonMasterDefaultNI sends a Flush to the server using an old election ID
// and  validates that the programmed chain of entries are not removed form the
// default NI using the Get RPC.
func FlushFromNonMasterDefaultNI(c *fluent.GRIBIClient, wantACK fluent.ProgrammingResult, t testing.TB, _ ...TestOpt) {
	addFlushEntriesToNI(c, server.DefaultNetworkInstanceName, wantACK, t)

	// addFlushEntriesToNI increments the election ID so to check with the current value,
	// we need to subtract two to ensure that we are not the current master.
	curID := electionID.Load() - 2

	ctx := context.Background()
	c.Start(ctx, t)
	defer c.Stop(t)

	_, flushErr := c.Flush().
		WithElectionID(curID, 0).
		WithAllNetworkInstances().
		Send()
	if flushErr == nil {
		t.Fatalf("did not get expected error from flush, got: %v", flushErr)
	}

	s, ok := status.FromError(flushErr)
	if !ok {
		t.Fatalf("received invalid error from server, got: %v", flushErr)
	}
	if got, want := s.Code(), codes.FailedPrecondition; got != want {
		t.Fatalf("did not get expected error from server, got code: %s (error: %v), want: %s", got, flushErr, want)
	}

	checkNIHasNEntries(ctx, c, server.DefaultNetworkInstanceName, 3, t)
}

// FlushOfSpecificNI programs entries into two network-instances, the default and a named
// L3VRF. It subsequently issues a Flush RPC and ensures that entries that are within the
// flushed network instance (the default) are removed, but the others are preserved.
func FlushOfSpecificNI(c *fluent.GRIBIClient, wantACK fluent.ProgrammingResult, t testing.TB, _ ...TestOpt) {
	// TODO(robjs): we need to initialise the server with >1 network instance.
	t.Skip()

	vrfName := "TEST-VRF"

	addFlushEntriesToNI(c, server.DefaultNetworkInstanceName, wantACK, t)
	addFlushEntriesToNI(c, vrfName, wantACK, t)

	// addFlushEntriesToNI increments the election ID so to check with the current value,
	// we need to subtract one from the current election ID.
	curID := electionID.Load() - 1

	ctx := context.Background()
	c.Start(ctx, t)
	defer c.Stop(t)

	fr, err := c.Flush().
		WithElectionID(curID, 0).
		WithNetworkInstance(server.DefaultNetworkInstanceName).
		Send()
	switch {
	case err != nil:
		t.Fatalf("got unexpected error from flush, got: %v", err)
	case fr == nil:
		t.Fatalf("got nil response from flush, got: %v", fr)
	}

	checkNIHasNEntries(ctx, c, server.DefaultNetworkInstanceName, 0, t)
	checkNIHasNEntries(ctx, c, vrfName, 3, t)
}

// FlushOfAllNIs programs entries in two network instances - the default and a
// named VRF and ensures that entries are removed from both when the Flush specifies
// that all network instances are to be removed.
func FlushOfAllNIs(c *fluent.GRIBIClient, wantACK fluent.ProgrammingResult, t testing.TB, _ ...TestOpt) {
	// TODO(robjs): we need to initialise the server with >1 network instance.
	t.Skip()

	vrfName := "TEST-VRF"

	addFlushEntriesToNI(c, server.DefaultNetworkInstanceName, wantACK, t)
	addFlushEntriesToNI(c, vrfName, wantACK, t)

	// addFlushEntriesToNI increments the election ID so to check with the current value,
	// we need to subtract one from the current election ID.
	curID := electionID.Load() - 1

	ctx := context.Background()
	c.Start(ctx, t)
	defer c.Stop(t)

	fr, err := c.Flush().
		WithElectionID(curID, 0).
		WithAllNetworkInstances().
		Send()
	switch {
	case err != nil:
		t.Fatalf("got unexpected error from flush, got: %v", err)
	case fr == nil:
		t.Fatalf("got nil response from flush, got: %v", fr)
	}

	checkNIHasNEntries(ctx, c, server.DefaultNetworkInstanceName, 0, t)
	checkNIHasNEntries(ctx, c, vrfName, 0, t)
}

// addFlushEntriesToNI is a helper that programs a chain of IPv4->NHG->NH entries
// into the specified niName network-instance, with the specified wantACK behaviour.
// It validates that the entries were installed from the returned Modify results.
func addFlushEntriesToNI(c *fluent.GRIBIClient, niName string, wantACK fluent.ProgrammingResult, t testing.TB) {
	t.Helper()
	ops := []func(){
		func() {
			c.Modify().AddEntry(t,
				fluent.NextHopEntry().
					WithNetworkInstance(niName).
					WithIndex(1).
					WithIPAddress("1.1.1.1"))
		},
		func() {
			c.Modify().AddEntry(t,
				fluent.NextHopGroupEntry().
					WithNetworkInstance(niName).
					WithID(1).
					AddNextHop(1, 1))
		},
		func() {
			c.Modify().AddEntry(t,
				fluent.IPv4Entry().
					WithNetworkInstance(niName).
					WithNextHopGroup(1).
					WithPrefix("42.42.42.42/32"))
		},
	}

	res := doModifyOps(c, t, ops, wantACK, false)

	// validate that our entries were installed correctly.
	chk.HasResult(t, res,
		fluent.OperationResult().
			WithNextHopOperation(1).
			WithOperationType(constants.Add).
			WithProgrammingResult(wantACK).
			AsResult(),
		chk.IgnoreOperationID(),
	)

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithNextHopGroupOperation(1).
			WithOperationType(constants.Add).
			WithProgrammingResult(wantACK).
			AsResult(),
		chk.IgnoreOperationID(),
	)

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithIPv4Operation("42.42.42.42/32").
			WithOperationType(constants.Add).
			WithProgrammingResult(wantACK).
			AsResult(),
		chk.IgnoreOperationID(),
	)
}

// checkNIHasNEntries uses the Get RPC to validate that the network instance named ni
// contains want (an integer) entries.
func checkNIHasNEntries(ctx context.Context, c *fluent.GRIBIClient, ni string, want int, t testing.TB) {
	t.Helper()
	gr, err := c.Get().
		WithNetworkInstance(ni).
		WithAFT(fluent.AllAFTs).
		Send()

	if err != nil {
		t.Fatalf("got unexpected error from get, got: %v", err)
	}

	if got := len(gr.GetEntry()); got != want {
		t.Fatalf("network instance %s has entries, got: %d, wanted:%d\nentries:\n%v", ni, got, want, gr)
	}
}

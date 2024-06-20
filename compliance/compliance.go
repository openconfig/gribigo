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

	aftpb "github.com/openconfig/gribi/v1/proto/gribi_aft"
	spb "github.com/openconfig/gribi/v1/proto/service"
)

// init statically sets the first Election ID used by the compliance tests to 1, since 0
// is an invalid value.
func init() {
	electionID.Store(1)
}

// electionID is a atomically updated uint64 that we use for the election ID in the tests
// this ensures that we do not have tests that specify an election ID that is older than
// the last tests', and thus fail due to the state of the server.
var electionID = &atomic.Uint64{}

// SetElectionID allows an external caller to specify an election ID to be used for
// subsequent calls.
func SetElectionID(v uint64) {
	electionID.Store(v)
}

var (
	// defaultNetworkInstance name is the string name of the default network instance
	// on the server. It can be overridden by tests that have pushed a configuration
	// to a server where they have specified a value for the default network instance.
	defaultNetworkInstanceName = server.DefaultNetworkInstanceName

	// vrfName is a name of a non-default VRF that exists on the server. It can be
	// overriden by tests that have pushed a configuration to the server where they
	// have created a name that is not the specified string.
	vrfName = "NON-DEFAULT-VRF"

	// nonexistentVRFName is a name of a VRF that does not exist on the server.
	nonexistentVRFName = "NonExistentVrf"
)

// SetDefaultNetworkInstanceName allows an external caller to specify a network
// instance name to be used for the default network instance.
func SetDefaultNetworkInstanceName(n string) {
	defaultNetworkInstanceName = n
}

// SetNonDefaultVRFName allows an external caller to specify a network-instance
// name to be used as a non-default Layer 3 VRF.
func SetNonDefaultVRFName(n string) {
	vrfName = n
}

// Test describes a test within the compliance library.
type Test struct {
	// Fn is the function to be run for a test. Tests must not error if additional
	// TestOpt arguments are supplied to them, but silently ignore them so as to allow
	// a consistent set of options to be passed to the test suite.
	Fn func(*fluent.GRIBIClient, testing.TB, ...TestOpt)
	// Description is a longer description of what the test does such that it can
	// be handed to a documentation generating function within the test.
	Description string
	// ShortName is a short description of the test for use in test output.
	ShortName string
	// Reference is a unique reference to external data (e.g., test plans) used for the test.
	Reference string

	// RequiresFIBACK marks a test that requires the implementation of FIB ACK on the server.
	// This is expected behaviour of a gRIBI server, but some implementations add this in
	// later builds.
	RequiresFIBACK bool
	// RequiresServerReordering marks a test that requires the implementation of server-side
	// reordering of transactions rather than an immediate NACK. Currently, some implementations
	// immediately NACK forward references, which causes some tests to fail. The reference
	// implementation handles reodering.
	RequiresServerReordering bool
	// RequiresImplicitReplace marks a test that requires the implementation of AFTOperation
	// ADD for entries that already exist.
	RequiresImplicitReplace bool
	// RequiresIdempotentDelete marks a test that requires the implementation of AFTOperation
	// DELETE for entries that do not exist.
	RequiresIdempotentDelete bool
	// RequiresNonDefaultNINHG marks a test that configures NH and NHG entries (not
	// including IPv4) in non-default network-instance.
	RequiresNonDefaultNINHG bool
	// RequiresMPLS marks a test that requires MPLS support in the gRIBI server.
	RequiresMPLS bool
	// RequiresIPv6 marks a test that requires IPv6 support in the gRIBI server.
	RequiresIPv6 bool
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

// TestOpt is an option that is handed to tests that need additional parameters,
// each test is responsible for checking whether it is populated in the manner that
// it expects.
type TestOpt interface {
	// IsTestOpt marks the TestOpt as implementing this interface.
	IsTestOpt()
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
			Fn:        InvalidElectionIDAndAFTOperation,
			ShortName: "Invalid updated election ID and AFTOperation in same ModifyRequest",
		},
	}, {
		In: Test{
			Fn:        InvalidElectionIDAndParams,
			ShortName: "Invalid update election ID and SessionParams in same ModifyRequest",
		},
	}, {
		In: Test{
			Fn:        InvalidParamsAndAFTOperation,
			ShortName: "Invalid session params and AFT operation in same ModifyRequest",
		},
	}, {
		In: Test{
			Fn:        makeTestWithACK(AddIPv4Entry, fluent.InstalledInRIB),
			Reference: "TE-2.1.1.1",
			ShortName: "Add IPv4 entry that can be programmed on the server - with RIB ACK",
		},
	}, {
		In: Test{
			Fn:             makeTestWithACK(AddIPv4Entry, fluent.InstalledInFIB),
			Reference:      "TE-2.1.1.2",
			ShortName:      "Add IPv4 entry that can be programmed on the server - with FIB ACK",
			RequiresFIBACK: true,
		},
	}, {
		In: Test{
			Fn:        makeTestWithACK(AddUnreferencedNextHopGroup, fluent.InstalledInRIB),
			ShortName: "Add next-hop-group entry that can be resolved on the server, no referencing IPv4 entries - with RIB ACK",
		},
	}, {
		In: Test{
			Fn:             makeTestWithACK(AddUnreferencedNextHopGroup, fluent.InstalledInFIB),
			ShortName:      "Add next-hop-group entry that can be resolved on the server, no referencing IPv4 entries - with FIB ACK",
			RequiresFIBACK: true,
		},
	}, {
		In: Test{
			Fn:                       AddIPv4EntryRandom,
			ShortName:                "Add IPv4 entries that are resolved by NHG and NH, in random order",
			RequiresServerReordering: true,
		},
	}, {
		In: Test{
			Fn:             makeTestWithACK(AddIPv4ToMultipleNHsSingleRequest, fluent.InstalledInFIB),
			ShortName:      "Add IPv4 entries that are resolved to a next-hop-group containing multiple next-hops (single ModifyRequest) - with FIB ACK",
			Reference:      "TE-2.1.2.1",
			RequiresFIBACK: true,
		},
	}, {
		In: Test{
			Fn:        makeTestWithACK(AddIPv4ToMultipleNHsSingleRequest, fluent.InstalledInRIB),
			ShortName: "Add IPv4 entries that are resolved to a next-hop-group containing multiple next-hops (single ModifyRequest) - with RIB ACK",
			Reference: "TE-2.1.2.1",
		},
	}, {
		In: Test{
			Fn:             makeTestWithACK(AddIPv4ToMultipleNHsMultipleRequests, fluent.InstalledInFIB),
			ShortName:      "Add IPv4 entries that are resolved to a next-hop-group containing multiple next-hops (multiple ModifyRequests) - with FIB ACK",
			Reference:      "TE-2.1.2.2",
			RequiresFIBACK: true,
		},
	}, {
		In: Test{
			Fn:        makeTestWithACK(AddIPv4ToMultipleNHsMultipleRequests, fluent.InstalledInRIB),
			ShortName: "Add IPv4 entries that are resolved to a next-hop-group containing multiple next-hops (multiple ModifyRequests) - with RIB ACK",
			Reference: "TE-2.1.2.2",
		},
	}, {
		In: Test{
			Fn:        makeTestWithACK(DeleteIPv4Entry, fluent.InstalledInRIB),
			ShortName: "Delete IPv4 entry within default network instance - RIB ACK",
		},
	}, {
		In: Test{
			Fn:        makeTestWithACK(DeleteReferencedNHGFailure, fluent.InstalledInRIB),
			ShortName: "Delete NHG entry that is referenced - failure - RIB ACK",
		},
	}, {
		In: Test{
			Fn:        makeTestWithACK(DeleteReferencedNHFailure, fluent.InstalledInRIB),
			ShortName: "Delete NH entry that is referenced - failure - RIB ACK",
		},
	}, {
		In: Test{
			Fn:        makeTestWithACK(DeleteNextHopGroup, fluent.InstalledInRIB),
			ShortName: "Delete NHG entry successfully - RIB ACK",
		},
	}, {
		In: Test{
			Fn:             makeTestWithACK(DeleteNextHopGroup, fluent.InstalledInFIB),
			ShortName:      "Delete NHG entry successfully - FIB ACK",
			RequiresFIBACK: true,
		},
	}, {
		In: Test{
			Fn:        makeTestWithACK(DeleteNextHop, fluent.InstalledInRIB),
			ShortName: "Delete NH entry successfully - RIB ACK",
		},
	}, {
		In: Test{
			Fn:             makeTestWithACK(DeleteNextHop, fluent.InstalledInRIB),
			ShortName:      "Delete NH entry successfully - FIB ACK",
			RequiresFIBACK: true,
		},
	}, {
		In: Test{
			Fn:        AddIPv4Metadata,
			ShortName: "Add Metadata for IPv4 entry",
		},
	}, {
		In: Test{
			Fn:        makeTestWithACK(AddIPv4EntryDifferentNINHG, fluent.InstalledInRIB),
			ShortName: "Add IPv4 Entry that references a NHG in a different network instance",
		},
	}, {
		In: Test{
			Fn:        makeTestWithACK(AddDeleteAdd, fluent.InstalledInRIB),
			ShortName: "Add-Delete-Add for IPv4Entry - RIB ACK",
		},
	}, {
		In: Test{
			Fn:                      makeTestWithACK(ImplicitReplaceNH, fluent.InstalledInRIB),
			ShortName:               "Implicit replace NH entry - RIB ACK",
			RequiresImplicitReplace: true,
		},
	}, {
		In: Test{
			Fn:                      makeTestWithACK(ImplicitReplaceNHG, fluent.InstalledInRIB),
			ShortName:               "Implicit replace NHG entry - RIB ACK",
			RequiresImplicitReplace: true,
		},
	}, {
		In: Test{
			Fn:                      makeTestWithACK(ImplicitReplaceIPv4Entry, fluent.InstalledInRIB),
			ShortName:               "Implicit replace IPv4 entry - RIB ACK",
			RequiresImplicitReplace: true,
		},
	}, {
		In: Test{
			Fn:                      makeTestWithACK(ImplicitReplaceNH, fluent.InstalledInFIB),
			ShortName:               "Implicit replace NH entry - FIB ACK",
			RequiresImplicitReplace: true,
			RequiresFIBACK:          true,
		},
	}, {
		In: Test{
			Fn:                      makeTestWithACK(ImplicitReplaceNHG, fluent.InstalledInFIB),
			ShortName:               "Implicit replace NHG entry - FIB ACK",
			RequiresImplicitReplace: true,
			RequiresFIBACK:          true,
		},
	}, {
		In: Test{
			Fn:                      makeTestWithACK(ImplicitReplaceIPv4Entry, fluent.InstalledInFIB),
			ShortName:               "Implicit replace IPv4 entry - FIB ACK",
			RequiresImplicitReplace: true,
			RequiresFIBACK:          true,
		},
	}, {
		In: Test{
			Fn:                       makeTestWithACK(IdempotentDelete, fluent.InstalledInRIB),
			ShortName:                "Idempotent Delete entry - RIB ACK",
			RequiresIdempotentDelete: true,
		},
	}, {
		In: Test{
			Fn:                       makeTestWithACK(IdempotentDelete, fluent.InstalledInFIB),
			ShortName:                "Idempotent Delete entry - FIB ACK",
			RequiresIdempotentDelete: true,
			RequiresFIBACK:           true,
		},
	}, {
		In: Test{
			Fn:        ReplaceMissingNH,
			ShortName: "Ensure failure for a NH entry that does not exist",
		},
	}, {
		In: Test{
			Fn:        ReplaceMissingNHG,
			ShortName: "Ensure failure for a NHG entry that does not exist",
		},
	}, {
		In: Test{
			Fn:        ReplaceMissingIPv4Entry,
			ShortName: "Ensure failure for an IPv4 entry that does not exist",
		},
	}, {
		In: Test{
			Fn:        TestOperationIsolation,
			ShortName: "AFTOperation responses must not be sent to other clients",
		},
	}, {
		In: Test{
			Fn:        makeTestWithACK(GetNH, fluent.InstalledInRIB),
			ShortName: "Get for installed NH - RIB ACK",
		},
	}, {
		In: Test{
			Fn:        makeTestWithACK(GetNHG, fluent.InstalledInRIB),
			ShortName: "Get for installed NHG - RIB ACK",
		},
	}, {
		In: Test{
			Fn:        makeTestWithACK(GetIPv4, fluent.InstalledInRIB),
			ShortName: "Get for installed IPv4 Entry - RIB ACK",
		},
	}, {
		In: Test{
			Fn:        makeTestWithACK(GetIPv4Chain, fluent.InstalledInRIB),
			ShortName: "Get for installed chain of entries - RIB ACK",
		},
	}, {
		In: Test{
			Fn:        makeTestWithACK(GetBenchmarkNH, fluent.InstalledInRIB),
			ShortName: "Benchmark Get for next-hops",
		},
	}, {
		In: Test{
			Fn:             makeTestWithACK(GetNH, fluent.InstalledInFIB),
			ShortName:      "Get for installed NH - FIB ACK",
			RequiresFIBACK: true,
		},
	}, {
		In: Test{
			Fn:             makeTestWithACK(GetNHG, fluent.InstalledInFIB),
			ShortName:      "Get for installed NHG - FIB ACK",
			RequiresFIBACK: true,
		},
	}, {
		In: Test{
			Fn:             makeTestWithACK(GetIPv4, fluent.InstalledInFIB),
			ShortName:      "Get for installed IPv4 Entry - FIB ACK",
			RequiresFIBACK: true,
		},
	}, {
		In: Test{
			Fn:             makeTestWithACK(GetIPv4Chain, fluent.InstalledInFIB),
			ShortName:      "Get for installed chain of entries - FIB ACK",
			RequiresFIBACK: true,
		},
	}, {
		In: Test{
			Fn:        TestUnsupportedElectionParams,
			ShortName: "Election - Ensure that election ID is not accepted in ALL_PRIMARY mode",
		},
	}, {
		In: Test{
			Fn:        TestDifferingElectionParameters,
			ShortName: "Election - Ensure that a client with mismatched parameters is rejected",
		},
	}, {
		In: Test{
			Fn:        TestMatchingElectionParameters,
			ShortName: "Election - Matching parameters for two clients in election",
		},
	}, {
		In: Test{
			Fn:        TestParamsDifferFromOtherClients,
			ShortName: "Election - Ensure client with differing parameters is rejected",
		},
	}, {
		In: Test{
			Fn:        TestLowerElectionID,
			ShortName: "Election - Lower election ID from new client",
		},
	}, {
		In: Test{
			Fn:        TestActiveAfterMasterChange,
			ShortName: "Election - Active entries after new master connects",
		},
	}, {
		In: Test{
			Fn:        TestNewElectionIDNoUpdateRejected,
			ShortName: "Election - Unannounced master operations are rejected",
		},
	}, {
		In: Test{
			Fn:        TestIncElectionID,
			ShortName: "Election - Incrementing election ID is honoured, and older IDs are rejected",
		},
	}, {
		In: Test{
			Fn:        TestDecElectionID,
			ShortName: "Election - Decrementing election ID is ignored",
		},
	}, {
		In: Test{
			Fn:        TestSameElectionIDFromTwoClients,
			ShortName: "Election - Sending same election ID from two clients",
		},
	}, {
		In: Test{
			Fn:        TestElectionIDAsZero,
			ShortName: "Election - Sending election ID as zero",
		},
	}, {
		In: Test{
			Fn:        makeTestWithACK(FlushFromMasterDefaultNI, fluent.InstalledInRIB),
			ShortName: "Flush of all entries in default NI by elected master",
		},
	}, {
		In: Test{
			Fn:        makeTestWithACK(FlushFromNonMasterDefaultNI, fluent.InstalledInRIB),
			ShortName: "Flush from non-elected master returns error",
		},
	}, {
		In: Test{
			Fn:        makeTestWithACK(FlushNIUnspecified, fluent.InstalledInRIB),
			ShortName: "Flush without specifying network instance returns error",
		},
	}, {
		In: Test{
			Fn:        makeTestWithACK(FlushFromOverrideDefaultNI, fluent.InstalledInRIB),
			ShortName: "Flush from client overriding election is honoured",
		},
	}, {
		In: Test{
			Fn:                      makeTestWithACK(FlushOfSpecificNI, fluent.InstalledInRIB),
			ShortName:               "Flush to specific network instance is honoured",
			RequiresNonDefaultNINHG: true,
		},
	}, {
		In: Test{
			Fn:                      makeTestWithACK(FlushOfAllNIs, fluent.InstalledInRIB),
			ShortName:               "Flush all network instances",
			RequiresNonDefaultNINHG: true,
		},
	}, {
		In: Test{
			Fn:                      makeTestWithACK(FlushPreservesDefaultNI, fluent.InstalledInRIB),
			ShortName:               "Flush non-default network instances preserves the default",
			RequiresNonDefaultNINHG: false, // No entries in the non-default VRF.
		},
	}, {
		In: Test{
			Fn:           makeTestWithACK(AddMPLSEntry, fluent.InstalledInRIB),
			ShortName:    "MPLS simple programming entry",
			RequiresMPLS: true,
		},
	}, {
		In: Test{
			Fn:           makeTestWithACK(DeleteMPLSEntry, fluent.InstalledInRIB),
			ShortName:    "MPLS delete entry",
			RequiresMPLS: true,
		},
	}, {
		In: Test{
			Fn:           makeTestWithACK(AddMPLSEntryWithLabelStack, fluent.InstalledInRIB),
			ShortName:    "MPLS add entry with NH label stack",
			RequiresMPLS: true,
		},
	}, {
		In: Test{
			Fn:           makeTestWithACK(AddIPv6Entry, fluent.InstalledInRIB),
			ShortName:    "Add IPv6 entry that can be programmed on the server - with RIB ACK",
			RequiresIPv6: true,
		},
	}, {
		In: Test{
			Fn:             makeTestWithACK(AddIPv6Entry, fluent.InstalledInFIB),
			ShortName:      "Add IPv6 entry that can be programmed on the server - with FIB ACK",
			RequiresFIBACK: true,
			RequiresIPv6:   true,
		},
	}, {
		In: Test{
			Fn:           AddIPv6Metadata,
			ShortName:    "Add IPv6 entry with metadata",
			RequiresIPv6: true,
		},
	}, {
		In: Test{
			Fn:        AddToNonexistentNetworkInstance,
			ShortName: "Add to a nonexistent network instance",
		},
	}}
)

// awaitTimeout calls a fluent client Await, adding a timeout to the context.
func awaitTimeout(ctx context.Context, c *fluent.GRIBIClient, t testing.TB, timeout time.Duration) error {
	subctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return c.Await(subctx, t)
}

// ModifyConnection is a test that opens a Modify RPC. It determines
// that there is no response from the server.
func ModifyConnection(c *fluent.GRIBIClient, t testing.TB, _ ...TestOpt) {
	c.Start(context.Background(), t)
	defer c.Stop(t)
	c.StartSending(context.Background(), t)
	awaitTimeout(context.Background(), c, t, time.Minute)
	// We get results, and just expected that there are none, because we did not
	// send anything to the server.
	if r := c.Results(t); len(r) != 0 {
		t.Fatalf("did not get expected number of return messages, got: %d (%v), want: 0", len(r), r)
	}
}

// ModifyConnectionWithElectionID is a test that opens a Modify RPC,
// with initial SessionParameters. It determines that there is a
// successful response from the server for the election ID.
func ModifyConnectionWithElectionID(c *fluent.GRIBIClient, t testing.TB, _ ...TestOpt) {
	defer electionID.Inc()
	c.Connection().WithInitialElectionID(electionID.Load(), 0).WithRedundancyMode(fluent.ElectedPrimaryClient).WithPersistence()
	c.Start(context.Background(), t)
	defer c.Stop(t)
	c.StartSending(context.Background(), t)
	if err := awaitTimeout(context.Background(), c, t, time.Minute); err != nil {
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

// ModifyConnectionSinglePrimaryPreserve tests that the server returns an error
// when a client sends ALL_PRIMARY mode with persistence enabled. This is
// expected to be an erroneous combination and hence it checks that the server
// returns an error that specifies unsupported parameters and the failed
// precondition code.
func ModifyConnectionSinglePrimaryPreserve(c *fluent.GRIBIClient, t testing.TB, _ ...TestOpt) {
	c.Connection().WithRedundancyMode(fluent.AllPrimaryClients).WithPersistence()
	c.Start(context.Background(), t)
	defer c.Stop(t)
	c.StartSending(context.Background(), t)
	err := awaitTimeout(context.Background(), c, t, time.Minute)
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

	chk.HasRecvClientErrorWithStatus(t, err, want, chk.AllowUnimplemented())
}

// InvalidElectionIDAndAFTOperation ensures that the server returns an error when the client
// attempts to update the election ID whilst simultaenously specifying an operation.
func InvalidElectionIDAndAFTOperation(c *fluent.GRIBIClient, t testing.TB, _ ...TestOpt) {
	defer electionID.Inc()
	c.Connection().WithRedundancyMode(fluent.ElectedPrimaryClient).WithPersistence().WithInitialElectionID(electionID.Load(), 0)
	c.Start(context.Background(), t)
	defer c.Stop(t)

	c.StartSending(context.Background(), t)

	// Inject a specfic invalid entry that specifies election ID in two places along
	// with an entry.
	c.Modify().InjectRequest(t, &spb.ModifyRequest{
		ElectionId: &spb.Uint128{
			Low:  electionID.Load(),
			High: 0,
		},
		Operation: []*spb.AFTOperation{{
			ElectionId: &spb.Uint128{
				Low:  electionID.Load(),
				High: 0,
			},
			Id:              1,
			NetworkInstance: defaultNetworkInstanceName,
			Op:              spb.AFTOperation_ADD,
			Entry: &spb.AFTOperation_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index:   1,
					NextHop: &aftpb.Afts_NextHop{},
				},
			},
		}},
	})

	err := awaitTimeout(context.Background(), c, t, time.Minute)
	if err == nil {
		t.Fatal("did not get expected error from server, got: nil")
	}

	want := fluent.ModifyError().
		WithCode(codes.InvalidArgument).
		AsStatus(t)

	chk.HasRecvClientErrorWithStatus(t, err, want, chk.IgnoreDetails())
}

// InvalidElectionIDAndParams validates that the server returns an error when a client
// specifies an update election ID at the same time as specifying session parameters (which
// are illegal after the first message).
func InvalidElectionIDAndParams(c *fluent.GRIBIClient, t testing.TB, _ ...TestOpt) {
	defer electionID.Inc()
	c.Connection().WithRedundancyMode(fluent.ElectedPrimaryClient).WithPersistence().WithInitialElectionID(electionID.Load(), 0)
	c.Start(context.Background(), t)
	defer c.Stop(t)

	c.StartSending(context.Background(), t)

	// Inject a specfic invalid entry that specifies election ID in two places along
	// with the session parameters.
	c.Modify().InjectRequest(t, &spb.ModifyRequest{
		ElectionId: &spb.Uint128{
			Low:  electionID.Load(),
			High: 0,
		},
		Params: &spb.SessionParameters{
			Redundancy:  spb.SessionParameters_SINGLE_PRIMARY,
			Persistence: spb.SessionParameters_PRESERVE,
		},
	})

	err := awaitTimeout(context.Background(), c, t, time.Minute)
	if err == nil {
		t.Fatal("did not get expected error from server, got: nil")
	}

	want := fluent.ModifyError().
		WithCode(codes.InvalidArgument).
		AsStatus(t)

	chk.HasRecvClientErrorWithStatus(t, err, want, chk.IgnoreDetails())
}

// InvalidElectionIDAndAFTOperation validates that the server returns an error when a client
// specifies session parameters (which must be the first message in the stream) and an AFTOperation
// simulateously.
func InvalidParamsAndAFTOperation(c *fluent.GRIBIClient, t testing.TB, _ ...TestOpt) {
	defer electionID.Inc()
	c.Connection().WithRedundancyMode(fluent.ElectedPrimaryClient).WithPersistence().WithInitialElectionID(electionID.Load(), 0)
	c.Start(context.Background(), t)
	defer c.Stop(t)

	c.StartSending(context.Background(), t)

	// Inject a specifically invalid entry that specifies both the
	// election ID being updated and an entry to perform.
	c.Modify().InjectRequest(t, &spb.ModifyRequest{
		Params: &spb.SessionParameters{
			Redundancy:  spb.SessionParameters_SINGLE_PRIMARY,
			Persistence: spb.SessionParameters_PRESERVE,
		},
		Operation: []*spb.AFTOperation{{
			ElectionId: &spb.Uint128{
				Low:  electionID.Load(),
				High: 0,
			},
			Id:              1,
			NetworkInstance: defaultNetworkInstanceName,
			Op:              spb.AFTOperation_ADD,
			Entry: &spb.AFTOperation_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index:   1,
					NextHop: &aftpb.Afts_NextHop{},
				},
			},
		}},
	})

	err := awaitTimeout(context.Background(), c, t, time.Minute)
	if err == nil {
		t.Fatal("did not get expected error from server, got: nil")
	}

	want := fluent.ModifyError().
		WithCode(codes.InvalidArgument).
		AsStatus(t)

	chk.HasRecvClientErrorWithStatus(t, err, want, chk.IgnoreDetails())
}

// AddIPv4Entry adds a fully referenced IPv4Entry and checks whether the specified ACK
// type (wantACK) is returned.
func AddIPv4Entry(c *fluent.GRIBIClient, wantACK fluent.ProgrammingResult, t testing.TB, _ ...TestOpt) {
	defer flushServer(c, t)

	ops := []func(){
		func() {
			c.Modify().AddEntry(t, fluent.NextHopEntry().WithNetworkInstance(defaultNetworkInstanceName).WithIndex(1).WithIPAddress("192.0.2.1"))
		},
		func() {
			c.Modify().AddEntry(t, fluent.NextHopGroupEntry().WithNetworkInstance(defaultNetworkInstanceName).WithID(42).AddNextHop(1, 1))
		},
		func() {
			c.Modify().AddEntry(t, fluent.IPv4Entry().WithPrefix("1.1.1.1/32").WithNetworkInstance(defaultNetworkInstanceName).WithNextHopGroup(42))
		},
	}

	res := DoModifyOps(c, t, ops, wantACK, false)

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
func AddIPv4EntryRandom(c *fluent.GRIBIClient, t testing.TB, _ ...TestOpt) {
	defer flushServer(c, t)
	ops := []func(){
		func() {
			c.Modify().AddEntry(t, fluent.NextHopEntry().WithNetworkInstance(defaultNetworkInstanceName).WithIndex(1))
		},
		func() {
			c.Modify().AddEntry(t, fluent.NextHopGroupEntry().WithNetworkInstance(defaultNetworkInstanceName).WithID(42).AddNextHop(1, 1))
		},
		func() {
			c.Modify().AddEntry(t, fluent.IPv4Entry().WithPrefix("1.1.1.1/32").WithNetworkInstance(defaultNetworkInstanceName).WithNextHopGroup(42))
		},
	}

	res := DoModifyOps(c, t, ops, fluent.InstalledInRIB, true)

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
}

// AddIPv4Metadata adds an IPv4 Entry (and its dependencies) with metadata alongside the
// entry.
func AddIPv4Metadata(c *fluent.GRIBIClient, t testing.TB, _ ...TestOpt) {
	defer flushServer(c, t)
	ops := []func(){
		func() {
			c.Modify().AddEntry(t, fluent.NextHopEntry().WithIndex(1).WithNetworkInstance(defaultNetworkInstanceName).WithIPAddress("192.0.2.3"))
			c.Modify().AddEntry(t, fluent.NextHopGroupEntry().WithID(1).WithNetworkInstance(defaultNetworkInstanceName).AddNextHop(1, 1))
			c.Modify().AddEntry(t, fluent.IPv4Entry().
				WithPrefix("1.1.1.1/32").
				WithNetworkInstance(defaultNetworkInstanceName).
				WithNextHopGroup(1).
				WithMetadata([]byte{1, 2, 3, 4, 5, 6, 7, 8}),
			)
		},
	}

	res := DoModifyOps(c, t, ops, fluent.InstalledInRIB, false)

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithIPv4Operation("1.1.1.1/32").
			WithOperationType(constants.Add).
			WithProgrammingResult(fluent.InstalledInRIB).
			AsResult(),
		chk.IgnoreOperationID())

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithNextHopGroupOperation(1).
			WithOperationType(constants.Add).
			WithProgrammingResult(fluent.InstalledInRIB).
			AsResult(),
		chk.IgnoreOperationID())

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithNextHopOperation(1).
			WithOperationType(constants.Add).
			WithProgrammingResult(fluent.InstalledInRIB).
			AsResult(),
		chk.IgnoreOperationID())
}

// AddIPv4EntryDifferentNINHG adds an IPv4 entry that references a next-hop-group within a
// different network instance, and validates that the entry is successfully installed.
func AddIPv4EntryDifferentNINHG(c *fluent.GRIBIClient, wantACK fluent.ProgrammingResult, t testing.TB, _ ...TestOpt) {
	defer flushServer(c, t)
	ops := []func(){
		func() {
			c.Modify().AddEntry(t, fluent.NextHopEntry().
				WithNetworkInstance(defaultNetworkInstanceName).
				WithIndex(1).
				WithIPAddress("192.0.2.3"))
			c.Modify().AddEntry(t, fluent.NextHopGroupEntry().
				WithNetworkInstance(defaultNetworkInstanceName).
				WithID(1).
				AddNextHop(1, 1))
			c.Modify().AddEntry(t, fluent.IPv4Entry().
				WithPrefix("1.1.1.1/32").
				WithNetworkInstance(vrfName).
				WithNextHopGroup(1).
				WithNextHopGroupNetworkInstance(defaultNetworkInstanceName))
		},
	}

	res := DoModifyOps(c, t, ops, wantACK, false)

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithNextHopGroupOperation(1).
			WithProgrammingResult(wantACK).
			WithOperationType(constants.Add).
			AsResult(),
		chk.IgnoreOperationID())

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithNextHopOperation(1).
			WithProgrammingResult(wantACK).
			WithOperationType(constants.Add).
			AsResult(),
		chk.IgnoreOperationID())

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithIPv4Operation("1.1.1.1/32").
			WithProgrammingResult(wantACK).
			WithOperationType(constants.Add).
			AsResult(),
		chk.IgnoreOperationID())
}

// DoModifyOps performs the series of operations in ops using the context
// client c. wantACK specifies the ACK type to request from the
// server, and randomise specifies whether the operations should be
// sent in order, or randomised.
//
// If the caller sets randomise to true, the client MUST NOT, rely on the operation
// ID to validate the entries, since this is allocated internally to the client.
func DoModifyOps(c *fluent.GRIBIClient, t testing.TB, ops []func(), wantACK fluent.ProgrammingResult, randomise bool) []*client.OpResult {
	defer electionID.Inc()
	conn := c.Connection().WithRedundancyMode(fluent.ElectedPrimaryClient).WithInitialElectionID(electionID.Load(), 0).WithPersistence()

	if wantACK == fluent.InstalledInFIB {
		conn.WithFIBACK()
	}

	ctx := context.Background()
	c.Start(ctx, t)
	defer c.Stop(t)
	c.StartSending(ctx, t)
	if err := awaitTimeout(ctx, c, t, time.Minute); err != nil {
		t.Fatalf("got unexpected error from server - session negotiation, got: %v, want: nil", err)
	}

	// If randomise is specified, we go and do the operations in a random order.
	// In this case, the caller MUST
	if randomise {
		rand.New(rand.NewSource(time.Now().UnixNano()))
		rand.Shuffle(len(ops), func(i, j int) { ops[i], ops[j] = ops[j], ops[i] })
	}

	for _, fn := range ops {
		fn()
	}

	if err := awaitTimeout(ctx, c, t, time.Minute); err != nil {
		t.Fatalf("got unexpected error from server - entries, got: %v, want: nil", err)
	}
	return c.Results(t)
}

// AddUnreferencedNextHopGroup adds a NHG that is not referenced by any other entry. An ACK is expected,
// and is validated to be of the type specified by wantACK.
func AddUnreferencedNextHopGroup(c *fluent.GRIBIClient, wantACK fluent.ProgrammingResult, t testing.TB, _ ...TestOpt) {
	defer flushServer(c, t)

	ops := []func(){
		func() {
			c.Modify().AddEntry(t, fluent.NextHopEntry().WithNetworkInstance(defaultNetworkInstanceName).WithIndex(1).WithIPAddress("192.0.2.1"))
		},
		func() {
			c.Modify().AddEntry(t, fluent.NextHopGroupEntry().WithNetworkInstance(defaultNetworkInstanceName).WithID(42).AddNextHop(1, 1))
		},
	}

	res := DoModifyOps(c, t, ops, wantACK, false)

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

// ImplicitReplaceNH performs two add operations for the same NextHop entry, validating that the
// server handles this as an implicit replace of the entry.
func ImplicitReplaceNH(c *fluent.GRIBIClient, wantACK fluent.ProgrammingResult, t testing.TB, _ ...TestOpt) {
	defer flushServer(c, t)
	ops := []func(){
		func() {
			c.Modify().AddEntry(t,
				fluent.NextHopEntry().
					WithNetworkInstance(defaultNetworkInstanceName).
					WithIndex(1).
					WithIPAddress("192.0.2.1"))
		},
		func() {
			c.Modify().AddEntry(t,
				fluent.NextHopEntry().
					WithNetworkInstance(defaultNetworkInstanceName).
					WithIndex(1).
					WithIPAddress("192.0.2.2"))
		},
	}

	res := DoModifyOps(c, t, ops, wantACK, false)

	// Check the two Add operations in order - we always start at 1 during a test, so we can
	// safely specify the explicit IDs here.
	chk.HasResult(t, res,
		fluent.OperationResult().
			WithOperationID(1).
			WithNextHopOperation(1).
			WithOperationType(constants.Add).
			WithProgrammingResult(wantACK).
			AsResult())

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithOperationID(2).
			WithNextHopOperation(1).
			WithOperationType(constants.Add).
			WithProgrammingResult(wantACK).
			AsResult())
}

// ImplicitReplaceNHG performs two add operations for the same NextHopGroup entry, validating that the
// server handles this as an implicit replace of the entry.
func ImplicitReplaceNHG(c *fluent.GRIBIClient, wantACK fluent.ProgrammingResult, t testing.TB, _ ...TestOpt) {
	defer flushServer(c, t)

	ops := []func(){
		func() {
			c.Modify().AddEntry(t,
				fluent.NextHopEntry().
					WithNetworkInstance(defaultNetworkInstanceName).
					WithIndex(1).
					WithIPAddress("192.0.2.1"))

			c.Modify().AddEntry(t,
				fluent.NextHopEntry().
					WithNetworkInstance(defaultNetworkInstanceName).
					WithIndex(2).
					WithIPAddress("192.0.2.3"))

			c.Modify().AddEntry(t,
				fluent.NextHopGroupEntry().
					WithID(1).
					WithNetworkInstance(defaultNetworkInstanceName).
					AddNextHop(1, 1))
		},
		func() {
			c.Modify().AddEntry(t,
				fluent.NextHopGroupEntry().
					WithID(1).
					WithNetworkInstance(defaultNetworkInstanceName).
					AddNextHop(2, 1))
		},
	}

	res := DoModifyOps(c, t, ops, wantACK, false)

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithOperationID(1).
			WithNextHopOperation(1).
			WithOperationType(constants.Add).
			WithProgrammingResult(wantACK).
			AsResult())

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithOperationID(2).
			WithNextHopOperation(2).
			WithOperationType(constants.Add).
			WithProgrammingResult(wantACK).
			AsResult())

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithOperationID(3).
			WithNextHopGroupOperation(1).
			WithOperationType(constants.Add).
			WithProgrammingResult(wantACK).
			AsResult())

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithOperationID(4).
			WithNextHopGroupOperation(1).
			WithOperationType(constants.Add).
			WithProgrammingResult(wantACK).
			AsResult())
}

// ImplicitReplaceIPv4Entry performs two add operations for the same NextHopGroup entry, validating that the
// server handles this as an implicit replace of the entry.
func ImplicitReplaceIPv4Entry(c *fluent.GRIBIClient, wantACK fluent.ProgrammingResult, t testing.TB, _ ...TestOpt) {
	defer flushServer(c, t)

	ops := []func(){
		func() {
			c.Modify().AddEntry(t,
				fluent.NextHopEntry().
					WithNetworkInstance(defaultNetworkInstanceName).
					WithIndex(1).
					WithIPAddress("192.0.2.1"))

			c.Modify().AddEntry(t,
				fluent.NextHopEntry().
					WithNetworkInstance(defaultNetworkInstanceName).
					WithIndex(2).
					WithIPAddress("192.0.2.1"))

			c.Modify().AddEntry(t,
				fluent.NextHopGroupEntry().
					WithID(1).
					WithNetworkInstance(defaultNetworkInstanceName).
					AddNextHop(1, 1))

			c.Modify().AddEntry(t,
				fluent.NextHopGroupEntry().
					WithID(2).
					WithNetworkInstance(defaultNetworkInstanceName).
					AddNextHop(2, 1))

			c.Modify().AddEntry(t,
				fluent.IPv4Entry().
					WithPrefix("1.0.0.0/8").
					WithNetworkInstance(defaultNetworkInstanceName).
					WithNextHopGroup(1))
		},
		func() {
			c.Modify().AddEntry(t,
				fluent.IPv4Entry().
					WithPrefix("1.0.0.0/8").
					WithNetworkInstance(defaultNetworkInstanceName).
					WithNextHopGroup(2))
		},
	}

	res := DoModifyOps(c, t, ops, wantACK, false)

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithOperationID(1).
			WithNextHopOperation(1).
			WithOperationType(constants.Add).
			WithProgrammingResult(wantACK).
			AsResult())

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithOperationID(2).
			WithNextHopOperation(2).
			WithOperationType(constants.Add).
			WithProgrammingResult(wantACK).
			AsResult())

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithOperationID(3).
			WithNextHopGroupOperation(1).
			WithOperationType(constants.Add).
			WithProgrammingResult(wantACK).
			AsResult())

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithOperationID(4).
			WithNextHopGroupOperation(2).
			WithOperationType(constants.Add).
			WithProgrammingResult(wantACK).
			AsResult())

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithOperationID(5).
			WithIPv4Operation("1.0.0.0/8").
			WithOperationType(constants.Add).
			WithProgrammingResult(wantACK).
			AsResult())

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithOperationID(6).
			WithIPv4Operation("1.0.0.0/8").
			WithOperationType(constants.Add).
			WithProgrammingResult(wantACK).
			AsResult())
}

// IdempotentDelete performs two delete operations for the same NextHop,
// NextHopGroup, and IPv4Entry, validating that the server handles duplicate
// operations successfully.
func IdempotentDelete(c *fluent.GRIBIClient, wantACK fluent.ProgrammingResult, t testing.TB, _ ...TestOpt) {
	defer flushServer(c, t)
	ops := []func(){
		func() { baseTopologyEntries(c, t) },
		func() {
			c.Modify().DeleteEntry(t,
				fluent.IPv4Entry().
					WithNetworkInstance(defaultNetworkInstanceName).
					WithPrefix("1.0.0.0/8"))
		},
		func() {
			c.Modify().DeleteEntry(t,
				fluent.IPv4Entry().
					WithNetworkInstance(defaultNetworkInstanceName).
					WithPrefix("1.0.0.0/8"))
		},
		func() {
			c.Modify().DeleteEntry(t,
				fluent.NextHopGroupEntry().
					WithNetworkInstance(defaultNetworkInstanceName).
					WithID(1))
		},
		func() {
			c.Modify().DeleteEntry(t,
				fluent.NextHopGroupEntry().
					WithNetworkInstance(defaultNetworkInstanceName).
					WithID(1))
		},
		func() {
			c.Modify().DeleteEntry(t,
				fluent.NextHopEntry().
					WithNetworkInstance(defaultNetworkInstanceName).
					WithIndex(1))
		},
		func() {
			c.Modify().DeleteEntry(t,
				fluent.NextHopEntry().
					WithNetworkInstance(defaultNetworkInstanceName).
					WithIndex(1))
		},
	}

	res := DoModifyOps(c, t, ops, wantACK, false)
	validateBaseTopologyEntries(res, wantACK, t)

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithOperationID(5).
			WithIPv4Operation("1.0.0.0/8").
			WithOperationType(constants.Delete).
			WithProgrammingResult(wantACK).
			AsResult())

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithOperationID(6).
			WithIPv4Operation("1.0.0.0/8").
			WithOperationType(constants.Delete).
			WithProgrammingResult(wantACK).
			AsResult())

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithOperationID(7).
			WithNextHopGroupOperation(1).
			WithOperationType(constants.Delete).
			WithProgrammingResult(wantACK).
			AsResult())

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithOperationID(8).
			WithNextHopGroupOperation(1).
			WithOperationType(constants.Delete).
			WithProgrammingResult(wantACK).
			AsResult())

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithOperationID(9).
			WithNextHopOperation(1).
			WithOperationType(constants.Delete).
			WithProgrammingResult(wantACK).
			AsResult())

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithOperationID(10).
			WithNextHopOperation(1).
			WithOperationType(constants.Delete).
			WithProgrammingResult(wantACK).
			AsResult())
}

// ReplaceMissingNH validates that an operation for a next-hop entry that does not exist
// on the server fails.
func ReplaceMissingNH(c *fluent.GRIBIClient, t testing.TB, _ ...TestOpt) {
	defer flushServer(c, t)

	ops := []func(){
		func() {
			c.Modify().ReplaceEntry(t,
				fluent.NextHopEntry().
					WithNetworkInstance(defaultNetworkInstanceName).
					WithIndex(42))
		},
	}

	res := DoModifyOps(c, t, ops, fluent.InstalledInRIB, false)

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithOperationID(1).
			WithNextHopOperation(42).
			WithOperationType(constants.Replace).
			WithProgrammingResult(fluent.ProgrammingFailed).
			AsResult())
}

// ReplaceMissingNHG validates that an operation for a next-hop-group entry that does not exist
// on the server fails.
func ReplaceMissingNHG(c *fluent.GRIBIClient, t testing.TB, _ ...TestOpt) {
	defer flushServer(c, t)

	ops := []func(){
		func() {
			// Make sure that our replace does not get rejected because of a missing reference.
			c.Modify().AddEntry(t,
				fluent.NextHopEntry().
					WithNetworkInstance(defaultNetworkInstanceName).
					WithIndex(42).
					WithIPAddress("192.0.2.3"))

			c.Modify().ReplaceEntry(t,
				fluent.NextHopGroupEntry().
					WithNetworkInstance(defaultNetworkInstanceName).
					WithID(42).
					AddNextHop(42, 1))
		},
		func() {
			// Remove the NH we added.
			c.Modify().DeleteEntry(t,
				fluent.NextHopEntry().
					WithNetworkInstance(defaultNetworkInstanceName).
					WithIndex(42))
		},
	}

	res := DoModifyOps(c, t, ops, fluent.InstalledInRIB, false)

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithOperationID(1).
			WithNextHopOperation(42).
			WithOperationType(constants.Add).
			WithProgrammingResult(fluent.InstalledInRIB).
			AsResult())

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithOperationID(2).
			WithNextHopGroupOperation(42).
			WithOperationType(constants.Replace).
			WithProgrammingResult(fluent.ProgrammingFailed).
			AsResult())

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithOperationID(3).
			WithNextHopOperation(42).
			WithOperationType(constants.Delete).
			WithProgrammingResult(fluent.InstalledInRIB).
			AsResult())
}

// ReplaceMissingIPv4Entry validates that an operation for an IPv4 entry that does not exist
// on the server fails.
func ReplaceMissingIPv4Entry(c *fluent.GRIBIClient, t testing.TB, _ ...TestOpt) {
	defer flushServer(c, t)

	ops := []func(){
		func() {
			c.Modify().AddEntry(t,
				fluent.NextHopEntry().
					WithNetworkInstance(defaultNetworkInstanceName).
					WithIndex(42).
					WithIPAddress("192.0.2.3"))

			c.Modify().AddEntry(t,
				fluent.NextHopGroupEntry().
					WithNetworkInstance(defaultNetworkInstanceName).
					WithID(42).
					AddNextHop(42, 1))

			c.Modify().ReplaceEntry(t,
				fluent.IPv4Entry().
					WithNetworkInstance(defaultNetworkInstanceName).
					WithPrefix("192.0.2.1/32").
					WithNextHopGroup(42))
		},
		func() {
			// Remove the entries we added.

			c.Modify().DeleteEntry(t,
				fluent.NextHopGroupEntry().
					WithNetworkInstance(defaultNetworkInstanceName).
					WithID(42))

			c.Modify().DeleteEntry(t,
				fluent.NextHopEntry().
					WithNetworkInstance(defaultNetworkInstanceName).
					WithIndex(42))
		},
	}

	res := DoModifyOps(c, t, ops, fluent.InstalledInRIB, false)

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithOperationID(1).
			WithNextHopOperation(42).
			WithOperationType(constants.Add).
			WithProgrammingResult(fluent.InstalledInRIB).
			AsResult())

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithOperationID(2).
			WithNextHopGroupOperation(42).
			WithOperationType(constants.Add).
			WithProgrammingResult(fluent.InstalledInRIB).
			AsResult())

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithOperationID(3).
			WithIPv4Operation("192.0.2.1/32").
			WithOperationType(constants.Replace).
			WithProgrammingResult(fluent.ProgrammingFailed).
			AsResult())

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithOperationID(4).
			WithNextHopGroupOperation(42).
			WithOperationType(constants.Delete).
			WithProgrammingResult(fluent.InstalledInRIB).
			AsResult())

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithOperationID(5).
			WithNextHopOperation(42).
			WithOperationType(constants.Delete).
			WithProgrammingResult(fluent.InstalledInRIB).
			AsResult())
}

// TestOperationIsolation verifies no AFTOperation responses are received on a
// second client after the primary client has disconnected.
func TestOperationIsolation(c *fluent.GRIBIClient, t testing.TB, opts ...TestOpt) {
	defer flushServer(c, t)
	defer electionID.Add(2)
	clientA, clientB := clientAB(c, t, opts...)

	clientA.Connection().WithInitialElectionID(electionID.Load()+1, 0).
		WithRedundancyMode(fluent.ElectedPrimaryClient).
		WithPersistence()
	clientA.Start(context.Background(), t)
	clientA.StartSending(context.Background(), t)
	clientAErr := awaitTimeout(context.Background(), clientA, t, time.Minute)
	chk.HasNRecvErrors(t, clientAErr, 0)

	clientB.Connection().WithInitialElectionID(electionID.Load(), 0).
		WithRedundancyMode(fluent.ElectedPrimaryClient).
		WithPersistence()
	clientB.Start(context.Background(), t)
	defer clientB.Stop(t)
	clientB.StartSending(context.Background(), t)

	entries := []fluent.GRIBIEntry{
		fluent.NextHopEntry().
			WithNetworkInstance(defaultNetworkInstanceName).
			WithIndex(1).
			WithIPAddress("192.0.2.1"),
		fluent.NextHopGroupEntry().
			WithNetworkInstance(defaultNetworkInstanceName).
			WithID(42).
			AddNextHop(1, 1),
		fluent.IPv4Entry().
			WithPrefix("1.1.1.1/32").
			WithNetworkInstance(defaultNetworkInstanceName).
			WithNextHopGroup(42),
	}

	clientA.Modify().AddEntry(t, entries...)
	clientA.Stop(t)

	clientBErr := awaitTimeout(context.Background(), clientB, t, time.Minute)
	chk.HasNRecvErrors(t, clientBErr, 0)
	chk.HasNSendErrors(t, clientBErr, 0)
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
func baseTopologyEntries(c *fluent.GRIBIClient, t testing.TB, _ ...TestOpt) {
	c.Modify().AddEntry(t, fluent.NextHopEntry().WithNetworkInstance(defaultNetworkInstanceName).WithIndex(1).WithIPAddress("192.0.2.3"))
	c.Modify().AddEntry(t, fluent.NextHopEntry().WithNetworkInstance(defaultNetworkInstanceName).WithIndex(2).WithIPAddress("192.0.2.5"))
	c.Modify().AddEntry(t, fluent.NextHopGroupEntry().WithNetworkInstance(defaultNetworkInstanceName).WithID(1).AddNextHop(1, 1).AddNextHop(2, 1))
	c.Modify().AddEntry(t, fluent.IPv4Entry().WithPrefix("1.0.0.0/8").WithNetworkInstance(defaultNetworkInstanceName).WithNextHopGroup(1))
}

// validateBaseEntries checks that the entries in the base topology are correctly
// installed.
func validateBaseTopologyEntries(res []*client.OpResult, wantACK fluent.ProgrammingResult, t testing.TB) {
	// Check for next-hops 1 and 2.
	for _, nhopID := range []uint64{1, 2} {
		chk.HasResult(t, res,
			fluent.OperationResult().
				WithNextHopOperation(nhopID).
				WithProgrammingResult(wantACK).
				WithOperationType(constants.Add).
				AsResult(),
			chk.IgnoreOperationID(),
		)
	}

	// Check for next-hop-group 1.
	chk.HasResult(t, res,
		fluent.OperationResult().
			WithNextHopGroupOperation(1).
			WithProgrammingResult(wantACK).
			WithOperationType(constants.Add).
			AsResult(),
		chk.IgnoreOperationID(),
	)

	// Check for 1/8.
	chk.HasResult(t, res,
		fluent.OperationResult().
			WithIPv4Operation("1.0.0.0/8").
			WithProgrammingResult(wantACK).
			WithOperationType(constants.Add).
			AsResult(),
		chk.IgnoreOperationID(),
	)
}

// AddIPv4ToMultipleNHsSingleRequest is the internal implementation of the single request installation
// of an IPv4Entry referencing a NHG that contains multiple NHs. It uses the wantACK parameter to determine the
// type of acknowledgement that is expected from the server.
func AddIPv4ToMultipleNHsSingleRequest(c *fluent.GRIBIClient, wantACK fluent.ProgrammingResult, t testing.TB, _ ...TestOpt) {
	defer flushServer(c, t)
	ops := []func(){
		func() {
			c.Modify().AddEntry(t,
				fluent.NextHopEntry().WithNetworkInstance(defaultNetworkInstanceName).WithIndex(1).WithIPAddress("192.0.2.3"),
				fluent.NextHopEntry().WithNetworkInstance(defaultNetworkInstanceName).WithIndex(2).WithIPAddress("192.0.2.5"),
				fluent.NextHopGroupEntry().WithNetworkInstance(defaultNetworkInstanceName).WithID(1).AddNextHop(1, 1).AddNextHop(2, 1),
				fluent.IPv4Entry().WithPrefix("1.0.0.0/8").WithNetworkInstance(defaultNetworkInstanceName).WithNextHopGroup(1))
		},
	}

	validateBaseTopologyEntries(DoModifyOps(c, t, ops, wantACK, false), wantACK, t)
}

// AddIPv4ToMultipleNHsMultipleRequests creates an IPv4 entry which references a NHG containing
// 2 NHs within multiple ModifyReqests, validating that they are installed in the specified RIB
// or FIB according to wantACK.
func AddIPv4ToMultipleNHsMultipleRequests(c *fluent.GRIBIClient, wantACK fluent.ProgrammingResult, t testing.TB, _ ...TestOpt) {
	defer flushServer(c, t)
	ops := []func(){
		func() { baseTopologyEntries(c, t) },
	}
	validateBaseTopologyEntries(DoModifyOps(c, t, ops, wantACK, false), wantACK, t)
}

// makeTestWithACK creates a version of a test function that can be directly executed by the runner
// which has a specific ACK type.
func makeTestWithACK(fn func(*fluent.GRIBIClient, fluent.ProgrammingResult, testing.TB, ...TestOpt), wantACK fluent.ProgrammingResult) func(*fluent.GRIBIClient, testing.TB, ...TestOpt) {
	return func(c *fluent.GRIBIClient, t testing.TB, opt ...TestOpt) { fn(c, wantACK, t, opt...) }
}

// DeleteIPv4Entry deletes an IPv4 entry from the server's RIB.
func DeleteIPv4Entry(c *fluent.GRIBIClient, wantACK fluent.ProgrammingResult, t testing.TB, _ ...TestOpt) {
	defer flushServer(c, t)

	ops := []func(){
		func() { baseTopologyEntries(c, t) },
		func() {
			c.Modify().DeleteEntry(t, fluent.IPv4Entry().WithPrefix("1.0.0.0/8").WithNetworkInstance(defaultNetworkInstanceName))
		},
	}
	res := DoModifyOps(c, t, ops, wantACK, false)
	validateBaseTopologyEntries(res, wantACK, t)

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithIPv4Operation("1.0.0.0/8").
			WithOperationType(constants.Delete).
			WithProgrammingResult(wantACK).
			AsResult(),
		chk.IgnoreOperationID(),
	)
}

// DeleteReferencedNHGFailure attempts to delete a NextHopGroup entry that is referenced
// from the RIB, and expects a failure.
func DeleteReferencedNHGFailure(c *fluent.GRIBIClient, wantACK fluent.ProgrammingResult, t testing.TB, _ ...TestOpt) {
	defer flushServer(c, t)
	ops := []func(){
		func() { baseTopologyEntries(c, t) },
		func() {
			c.Modify().DeleteEntry(t, fluent.NextHopGroupEntry().WithID(1).WithNetworkInstance(defaultNetworkInstanceName))
		},
	}
	res := DoModifyOps(c, t, ops, wantACK, false)
	validateBaseTopologyEntries(res, wantACK, t)

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
func DeleteReferencedNHFailure(c *fluent.GRIBIClient, wantACK fluent.ProgrammingResult, t testing.TB, _ ...TestOpt) {
	defer flushServer(c, t)

	ops := []func(){
		func() { baseTopologyEntries(c, t) },
		func() {
			c.Modify().DeleteEntry(t, fluent.NextHopEntry().WithIndex(1).WithNetworkInstance(defaultNetworkInstanceName))
		},
		func() {
			c.Modify().DeleteEntry(t, fluent.NextHopEntry().WithIndex(2).WithNetworkInstance(defaultNetworkInstanceName))
		},
	}
	res := DoModifyOps(c, t, ops, wantACK, false)
	validateBaseTopologyEntries(res, wantACK, t)

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
// success. The ACK type expected is validated against wantACK.
func DeleteNextHopGroup(c *fluent.GRIBIClient, wantACK fluent.ProgrammingResult, t testing.TB, _ ...TestOpt) {
	defer flushServer(c, t)

	ops := []func(){
		func() { baseTopologyEntries(c, t) },
		func() {
			c.Modify().DeleteEntry(t,
				fluent.IPv4Entry().WithPrefix("1.0.0.0/8").WithNetworkInstance(defaultNetworkInstanceName),
				fluent.NextHopGroupEntry().WithID(1).WithNetworkInstance(defaultNetworkInstanceName),
			)
		},
	}

	res := DoModifyOps(c, t, ops, wantACK, false)
	validateBaseTopologyEntries(res, wantACK, t)

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithNextHopGroupOperation(1).
			WithOperationType(constants.Delete).
			WithProgrammingResult(wantACK).
			AsResult(),
		chk.IgnoreOperationID())

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithIPv4Operation("1.0.0.0/8").
			WithOperationType(constants.Delete).
			WithProgrammingResult(wantACK).
			AsResult(),
		chk.IgnoreOperationID())
}

// DeleteNextHop attempts to delete the NH entries within the base topology and expects
// success. The ACK type returned is validated against wantACK.
func DeleteNextHop(c *fluent.GRIBIClient, wantACK fluent.ProgrammingResult, t testing.TB, _ ...TestOpt) {
	defer flushServer(c, t)
	ops := []func(){
		func() { baseTopologyEntries(c, t) },
		func() {
			c.Modify().DeleteEntry(t,
				fluent.IPv4Entry().WithPrefix("1.0.0.0/8").WithNetworkInstance(defaultNetworkInstanceName),
				fluent.NextHopGroupEntry().WithID(1).WithNetworkInstance(defaultNetworkInstanceName),
				fluent.NextHopEntry().WithIndex(1).WithNetworkInstance(defaultNetworkInstanceName),
				fluent.NextHopEntry().WithIndex(2).WithNetworkInstance(defaultNetworkInstanceName),
			)
		},
	}

	res := DoModifyOps(c, t, ops, wantACK, false)
	validateBaseTopologyEntries(res, wantACK, t)

	for _, i := range []uint64{1, 2} {
		chk.HasResult(t, res,
			fluent.OperationResult().
				WithNextHopOperation(i).
				WithOperationType(constants.Delete).
				WithProgrammingResult(wantACK).
				AsResult(),
			chk.IgnoreOperationID())
	}

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithNextHopGroupOperation(1).
			WithOperationType(constants.Delete).
			WithProgrammingResult(wantACK).
			AsResult(),
		chk.IgnoreOperationID())

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithIPv4Operation("1.0.0.0/8").
			WithOperationType(constants.Delete).
			WithProgrammingResult(wantACK).
			AsResult(),
		chk.IgnoreOperationID())
}

// AddDeleteAdd tests that when a single ModifyRequest contains a sequence of operations,
// each is acknowledged separately by the server. Note that this does not imply that the
// transactions cannot be coaesced when writing to hardware, but in order to prevent
// pending transactions at the client, all must be acknowledged.
func AddDeleteAdd(c *fluent.GRIBIClient, wantACK fluent.ProgrammingResult, t testing.TB, _ ...TestOpt) {
	defer flushServer(c, t)

	ops := []func(){
		// Build the initial forwarding entries.
		func() { baseTopologyEntries(c, t) },
		// Add another prefix with add/delete/add.
		func() {
			c.Modify().AddEntry(t,
				fluent.IPv4Entry().
					WithPrefix("2.0.0.0/8").
					WithNetworkInstance(defaultNetworkInstanceName).
					WithNextHopGroup(1))
			c.Modify().DeleteEntry(t,
				fluent.IPv4Entry().
					WithPrefix("2.0.0.0/8").
					WithNetworkInstance(defaultNetworkInstanceName))
			c.Modify().AddEntry(t,
				fluent.IPv4Entry().
					WithPrefix("2.0.0.0/8").
					WithNetworkInstance(defaultNetworkInstanceName).
					WithNextHopGroup(1))
		},
	}

	res := DoModifyOps(c, t, ops, wantACK, false)
	validateBaseTopologyEntries(res, wantACK, t)

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithIPv4Operation("2.0.0.0/8").
			WithOperationType(constants.Add).
			WithProgrammingResult(wantACK).
			AsResult(),
		chk.IgnoreOperationID())

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithIPv4Operation("2.0.0.0/8").
			WithOperationType(constants.Delete).
			WithProgrammingResult(wantACK).
			AsResult(),
		chk.IgnoreOperationID())

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithIPv4Operation("2.0.0.0/8").
			WithOperationType(constants.Add).
			WithProgrammingResult(wantACK).
			AsResult(),
		chk.IgnoreOperationID())
}

// AddIPv6Entry adds a fully referenced IPv4Entry and checks whether the specified ACK
// type (wantACK) is returned.
func AddIPv6Entry(c *fluent.GRIBIClient, wantACK fluent.ProgrammingResult, t testing.TB, _ ...TestOpt) {
	defer flushServer(c, t)

	ops := []func(){
		func() {
			c.Modify().AddEntry(t, fluent.NextHopEntry().WithNetworkInstance(defaultNetworkInstanceName).WithIndex(1).WithIPAddress("192.0.2.1"))
		},
		func() {
			c.Modify().AddEntry(t, fluent.NextHopGroupEntry().WithNetworkInstance(defaultNetworkInstanceName).WithID(42).AddNextHop(1, 1))
		},
		func() {
			c.Modify().AddEntry(t, fluent.IPv6Entry().WithPrefix("2001:db8::/32").WithNetworkInstance(defaultNetworkInstanceName).WithNextHopGroup(42))
		},
	}

	res := DoModifyOps(c, t, ops, wantACK, false)

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

// AddIPv6Metadata adds an IPv6 Entry (and its dependencies) with metadata alongside the
// entry.
func AddIPv6Metadata(c *fluent.GRIBIClient, t testing.TB, _ ...TestOpt) {
	defer flushServer(c, t)
	ops := []func(){
		func() {
			c.Modify().AddEntry(t, fluent.NextHopEntry().WithIndex(1).WithNetworkInstance(defaultNetworkInstanceName).WithIPAddress("192.0.2.3"))
			c.Modify().AddEntry(t, fluent.NextHopGroupEntry().WithID(1).WithNetworkInstance(defaultNetworkInstanceName).AddNextHop(1, 1))
			c.Modify().AddEntry(t, fluent.IPv6Entry().
				WithPrefix("2001:db8::1/128").
				WithNetworkInstance(defaultNetworkInstanceName).
				WithNextHopGroup(1).
				WithMetadata([]byte{1, 2, 3, 4, 5, 6, 7, 8}),
			)
		},
	}

	res := DoModifyOps(c, t, ops, fluent.InstalledInRIB, false)

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithIPv6Operation("2001:db8::1/128").
			WithOperationType(constants.Add).
			WithProgrammingResult(fluent.InstalledInRIB).
			AsResult(),
		chk.IgnoreOperationID())

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithNextHopGroupOperation(1).
			WithOperationType(constants.Add).
			WithProgrammingResult(fluent.InstalledInRIB).
			AsResult(),
		chk.IgnoreOperationID())

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithNextHopOperation(1).
			WithOperationType(constants.Add).
			WithProgrammingResult(fluent.InstalledInRIB).
			AsResult(),
		chk.IgnoreOperationID())
}


// AddToNonexistentNetworkInstance adds a IPV6Entry to a network
// instance that does not exist and verifies it fails.
func AddToNonexistentNetworkInstance(c *fluent.GRIBIClient, t testing.TB, _ ...TestOpt) {
	defer flushServer(c, t)
	ops := []func(){
		func() {
			c.Modify().AddEntry(t, fluent.NextHopEntry().WithIndex(1).WithNetworkInstance(defaultNetworkInstanceName).WithIPAddress("192.0.2.3"))
			c.Modify().AddEntry(t, fluent.NextHopGroupEntry().WithID(1).WithNetworkInstance(defaultNetworkInstanceName).AddNextHop(1, 1))
			c.Modify().AddEntry(t, fluent.IPv6Entry().
				WithPrefix("2001:db8::1/128").
				WithNetworkInstance(nonexistentVRFName).
				WithNextHopGroup(1))
		},
	}

	res := DoModifyOps(c, t, ops, fluent.InstalledInFIB, false)

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithIPv6Operation("2001:db8::1/128").
			WithOperationType(constants.Add).
			WithProgrammingResult(fluent.ProgrammingFailed).
			AsResult(),
		chk.IgnoreOperationID())
}

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
	"github.com/openconfig/gribigo/device"
	"github.com/openconfig/gribigo/fluent"
	"github.com/openconfig/gribigo/server"
	"github.com/openconfig/gribigo/testcommon"

	ppb "github.com/openconfig/gribigo/proto/policy"
)

func TestGroupPolicy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	creds, err := device.TLSCredsFromFile(testcommon.TLSCreds())
	if err != nil {
		t.Fatalf("cannot load credentials, got err: %v", err)
	}

	// Define policies for two groups.
	// Group A can only program 10.0.0.0/8
	// Group B can only program 20.0.0.0/8
	policies := map[string]*ppb.GroupPolicy{
		"group-a": {
			Ipv4Policy: &ppb.IPv4PrefixPolicy{
				AllowedPrefixes: []string{"10.0.0.0/8"},
			},
		},
		"group-b": {
			Ipv4Policy: &ppb.IPv4PrefixPolicy{
				AllowedPrefixes: []string{"20.0.0.0/8"},
			},
		},
	}

	d, err := device.New(ctx,
		creds,
		device.ServerOpts(server.WithGroupPolicies(policies)),
	)
	if err != nil {
		t.Fatalf("cannot start server, %v", err)
	}

	c := fluent.NewClient()
	c.Connection().WithTarget(d.GRIBIAddr())

	// Test Group A
	t.Run("Group A policy", func(t *testing.T) {
		c.Connection().
			WithRedundancyMode(fluent.ElectedPrimaryWithOwnership).
			WithInitialElectionIDForGroup("group-a", 1, 0).
			WithPersistence()
		
		c.Start(ctx, t)
		defer c.Stop(t)
		c.StartSending(ctx, t)
		if err := awaitTimeout(ctx, c, t, time.Minute); err != nil {
			t.Fatalf("session negotiation failed: %v", err)
		}

		// Allowed prefix
		c.Modify().AddEntry(t,
			fluent.NextHopEntry().WithNetworkInstance(server.DefaultNetworkInstanceName).WithIndex(1).WithIPAddress("192.0.2.1"),
			fluent.NextHopGroupEntry().WithNetworkInstance(server.DefaultNetworkInstanceName).WithID(1).AddNextHop(1, 1),
			fluent.IPv4Entry().WithPrefix("10.1.1.1/32").WithNetworkInstance(server.DefaultNetworkInstanceName).WithNextHopGroup(1),
		)
		if err := awaitTimeout(ctx, c, t, time.Minute); err != nil {
			t.Fatalf("allowed add failed: %v", err)
		}
		chk.HasResult(t, c.Results(t),
			fluent.OperationResult().
				WithIPv4Operation("10.1.1.1/32").
				WithOperationType(constants.Add).
				WithProgrammingResult(fluent.InstalledInRIB).
				AsResult(),
			chk.IgnoreOperationID(),
		)

		// Disallowed prefix
		c.Modify().AddEntry(t,
			fluent.IPv4Entry().WithPrefix("20.1.1.1/32").WithNetworkInstance(server.DefaultNetworkInstanceName).WithNextHopGroup(1),
		)
		if err := awaitTimeout(ctx, c, t, time.Minute); err != nil {
			t.Fatalf("disallowed add returned RPC error: %v", err)
		}
		// Should fail with PERMISSION_DENIED
		chk.HasResult(t, c.Results(t),
			fluent.OperationResult().
				WithIPv4Operation("20.1.1.1/32").
				WithOperationType(constants.Add).
				WithProgrammingResult(fluent.PermissionDenied).
				AsResult(),
			chk.IgnoreOperationID(),
		)
	})

	// Test Group B
	t.Run("Group B policy", func(t *testing.T) {
		c2 := fluent.NewClient()
		c2.Connection().WithTarget(d.GRIBIAddr())
		c2.Connection().
			WithRedundancyMode(fluent.ElectedPrimaryWithOwnership).
			WithInitialElectionIDForGroup("group-b", 1, 0).
			WithPersistence()
		
		c2.Start(ctx, t)
		defer c2.Stop(t)
		c2.StartSending(ctx, t)
		if err := awaitTimeout(ctx, c2, t, time.Minute); err != nil {
			t.Fatalf("session negotiation failed: %v", err)
		}

		// Group B allowed prefix (which was Group A's disallowed)
		c2.Modify().AddEntry(t,
			fluent.NextHopEntry().WithNetworkInstance(server.DefaultNetworkInstanceName).WithIndex(2).WithIPAddress("192.0.2.2"),
			fluent.NextHopGroupEntry().WithNetworkInstance(server.DefaultNetworkInstanceName).WithID(2).AddNextHop(2, 1),
			fluent.IPv4Entry().WithPrefix("20.1.1.1/32").WithNetworkInstance(server.DefaultNetworkInstanceName).WithNextHopGroup(2),
		)
		if err := awaitTimeout(ctx, c2, t, time.Minute); err != nil {
			t.Fatalf("allowed add failed: %v", err)
		}
		chk.HasResult(t, c2.Results(t),
			fluent.OperationResult().
				WithIPv4Operation("20.1.1.1/32").
				WithOperationType(constants.Add).
				WithProgrammingResult(fluent.InstalledInRIB).
				AsResult(),
			chk.IgnoreOperationID(),
		)

		// Group B disallowed prefix (which was Group A's allowed)
		c2.Modify().AddEntry(t,
			fluent.IPv4Entry().WithPrefix("10.1.1.1/32").WithNetworkInstance(server.DefaultNetworkInstanceName).WithNextHopGroup(2),
		)
		if err := awaitTimeout(ctx, c2, t, time.Minute); err != nil {
			t.Fatalf("disallowed add returned RPC error: %v", err)
		}
		chk.HasResult(t, c2.Results(t),
			fluent.OperationResult().
				WithIPv4Operation("10.1.1.1/32").
				WithOperationType(constants.Add).
				WithProgrammingResult(fluent.PermissionDenied).
				AsResult(),
			chk.IgnoreOperationID(),
		)
	})
}

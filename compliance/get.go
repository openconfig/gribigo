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
	"encoding/binary"
	"fmt"
	"net"
	"testing"
	"time"

	log "github.com/golang/glog"
	"github.com/openconfig/gribigo/chk"
	"github.com/openconfig/gribigo/client"
	"github.com/openconfig/gribigo/constants"
	"github.com/openconfig/gribigo/fluent"
)

// GetNH validates that an installed next-hop is returned via the Get RPC.
func GetNH(c *fluent.GRIBIClient, wantACK fluent.ProgrammingResult, t testing.TB, _ ...TestOpt) {
	defer flushServer(c, t)
	ops := []func(){
		func() {
			c.Modify().AddEntry(t,
				fluent.NextHopEntry().
					WithNetworkInstance(defaultNetworkInstanceName).
					WithIndex(1).
					WithIPAddress("192.0.2.3"))
		},
	}

	res := DoModifyOps(c, t, ops, wantACK, false)

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithNextHopOperation(1).
			WithOperationType(constants.Add).
			WithProgrammingResult(wantACK).
			AsResult(),
		chk.IgnoreOperationID(),
	)

	ctx := context.Background()
	c.Start(ctx, t)
	defer c.Stop(t)
	gr, err := c.Get().
		WithNetworkInstance(defaultNetworkInstanceName).
		WithAFT(fluent.NextHop).
		Send()

	if err != nil {
		t.Fatalf("got unexpected error from get, got: %v", err)
	}

	chk.GetResponseHasEntries(t, gr,
		fluent.NextHopEntry().
			WithNetworkInstance(defaultNetworkInstanceName).
			WithIndex(1).
			WithIPAddress("192.0.2.3"))

}

// GetNHG validates that an installed next-hop-group is returned via the Get RPC.
func GetNHG(c *fluent.GRIBIClient, wantACK fluent.ProgrammingResult, t testing.TB, _ ...TestOpt) {
	defer flushServer(c, t)
	ops := []func(){
		func() {
			c.Modify().AddEntry(t,
				fluent.NextHopEntry().
					WithNetworkInstance(defaultNetworkInstanceName).
					WithIndex(1).
					WithIPAddress("192.0.2.3"))
		},
		func() {
			c.Modify().AddEntry(t,
				fluent.NextHopGroupEntry().
					WithNetworkInstance(defaultNetworkInstanceName).
					WithID(1).
					AddNextHop(1, 1))
		},
	}

	res := DoModifyOps(c, t, ops, wantACK, false)

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

	ctx := context.Background()
	c.Start(ctx, t)
	defer c.Stop(t)
	gr, err := c.Get().
		WithNetworkInstance(defaultNetworkInstanceName).
		WithAFT(fluent.NextHopGroup).
		Send()

	if err != nil {
		t.Fatalf("got unexpected error from get, got: %v", err)
	}

	chk.GetResponseHasEntries(t, gr,
		fluent.NextHopGroupEntry().
			WithNetworkInstance(defaultNetworkInstanceName).
			WithID(1).
			AddNextHop(1, 1),
	)
}

// GetIPv4 validates that an installed IPv4 entry is returned via the Get RPC.
func GetIPv4(c *fluent.GRIBIClient, wantACK fluent.ProgrammingResult, t testing.TB, _ ...TestOpt) {
	defer flushServer(c, t)

	ops := []func(){
		func() {
			c.Modify().AddEntry(t,
				fluent.NextHopEntry().
					WithNetworkInstance(defaultNetworkInstanceName).
					WithIndex(1).
					WithIPAddress("192.0.2.3"))
		},
		func() {
			c.Modify().AddEntry(t,
				fluent.NextHopGroupEntry().
					WithNetworkInstance(defaultNetworkInstanceName).
					WithID(1).
					AddNextHop(1, 1))
		},
		func() {
			c.Modify().AddEntry(t,
				fluent.IPv4Entry().
					WithNetworkInstance(defaultNetworkInstanceName).
					WithNextHopGroup(1).
					WithPrefix("42.42.42.42/32").
					WithMetadata([]byte{1, 2, 3, 4, 5, 6, 7, 8}))
		},
	}

	res := DoModifyOps(c, t, ops, wantACK, false)

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

	ctx := context.Background()
	c.Start(ctx, t)
	defer c.Stop(t)
	gr, err := c.Get().
		WithNetworkInstance(defaultNetworkInstanceName).
		WithAFT(fluent.IPv4).
		Send()

	if err != nil {
		t.Fatalf("got unexpected error from get, got: %v", err)
	}

	chk.GetResponseHasEntries(t, gr,
		fluent.IPv4Entry().
			WithNetworkInstance(defaultNetworkInstanceName).
			WithNextHopGroup(1).
			WithPrefix("42.42.42.42/32").
			WithMetadata([]byte{1, 2, 3, 4, 5, 6, 7, 8}),
	)
}

// GetIPv6 validates that an installed IPv6 entry is returned via the Get RPC.
func GetIPv6(c *fluent.GRIBIClient, wantACK fluent.ProgrammingResult, t testing.TB, _ ...TestOpt) {
	defer flushServer(c, t)

	ops := []func(){
		func() {
			c.Modify().AddEntry(t,
				fluent.NextHopEntry().
					WithNetworkInstance(defaultNetworkInstanceName).
					WithIndex(1).
					WithIPAddress("192.0.2.3"))
		},
		func() {
			c.Modify().AddEntry(t,
				fluent.NextHopGroupEntry().
					WithNetworkInstance(defaultNetworkInstanceName).
					WithID(1).
					AddNextHop(1, 1))
		},
		func() {
			c.Modify().AddEntry(t,
				fluent.IPv6Entry().
					WithNetworkInstance(defaultNetworkInstanceName).
					WithNextHopGroup(1).
					WithPrefix("2001:db8::/32").
					WithMetadata([]byte{1, 2, 3, 4, 5, 6, 7, 8}))
		},
	}

	res := DoModifyOps(c, t, ops, wantACK, false)

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
			WithIPv6Operation("2001:db8::/32").
			WithOperationType(constants.Add).
			WithProgrammingResult(wantACK).
			AsResult(),
		chk.IgnoreOperationID(),
	)

	ctx := context.Background()
	c.Start(ctx, t)
	defer c.Stop(t)
	gr, err := c.Get().
		WithNetworkInstance(defaultNetworkInstanceName).
		WithAFT(fluent.IPv6).
		Send()

	if err != nil {
		t.Fatalf("got unexpected error from get, got: %v", err)
	}

	chk.GetResponseHasEntries(t, gr,
		fluent.IPv6Entry().
			WithNetworkInstance(defaultNetworkInstanceName).
			WithNextHopGroup(1).
			WithPrefix("2001:db8::/32").
			WithMetadata([]byte{1, 2, 3, 4, 5, 6, 7, 8}),
	)
}

// GetIPv4Chain validates that Get for all AFTs returns the chain of IPv4Entry->NHG->NH
// required.
func GetIPv4Chain(c *fluent.GRIBIClient, wantACK fluent.ProgrammingResult, t testing.TB, _ ...TestOpt) {
	defer flushServer(c, t)

	ops := []func(){
		func() {
			c.Modify().AddEntry(t,
				fluent.NextHopEntry().
					WithNetworkInstance(defaultNetworkInstanceName).
					WithIndex(1).
					WithIPAddress("192.0.2.3"))
		},
		func() {
			c.Modify().AddEntry(t,
				fluent.NextHopGroupEntry().
					WithNetworkInstance(defaultNetworkInstanceName).
					WithID(1).
					AddNextHop(1, 1))
		},
		func() {
			c.Modify().AddEntry(t,
				fluent.IPv4Entry().
					WithNetworkInstance(defaultNetworkInstanceName).
					WithNextHopGroup(1).
					WithPrefix("42.42.42.42/32"))
		},
	}

	res := DoModifyOps(c, t, ops, wantACK, false)

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

	ctx := context.Background()
	c.Start(ctx, t)
	defer c.Stop(t)
	gr, err := c.Get().
		WithNetworkInstance(defaultNetworkInstanceName).
		WithAFT(fluent.AllAFTs).
		Send()

	if err != nil {
		t.Fatalf("got unexpected error from get, got: %v", err)
	}

	chk.GetResponseHasEntries(t, gr,
		fluent.IPv4Entry().
			WithNetworkInstance(defaultNetworkInstanceName).
			WithNextHopGroup(1).
			WithPrefix("42.42.42.42/32"),
		fluent.NextHopGroupEntry().
			WithNetworkInstance(defaultNetworkInstanceName).
			WithID(1).
			AddNextHop(1, 1),
		fluent.NextHopEntry().
			WithNetworkInstance(defaultNetworkInstanceName).
			WithIndex(1).
			WithIPAddress("192.0.2.3"),
	)
}

// indexAsIPv4 converts a uint32 index into an IP address, using the baseSlashEight argument as the
// starting point. For example, if an index = 1 is provided with baseSlashEight = 1 the address
// returned is 1.0.0.1.
func indexAsIPv4(i uint32, baseSlashEight int) string {
	ip := make(net.IP, 4)
	binary.BigEndian.PutUint32(ip, uint32(i)+uint32(baseSlashEight*16777216))
	return ip.String()
}

// populateNNHs creates N next-hops based via the client, c, expecting the wanACK back from
// the server. Errors are reported via the testing.TB provided.
func populateNNHs(c *fluent.GRIBIClient, n int, wantACK fluent.ProgrammingResult, t testing.TB) {
	ops := []func(){}
	for i := 0; i < n; i++ {
		j := i + 1
		ops = append(ops, func() {
			c.Modify().AddEntry(t,
				fluent.NextHopEntry().
					WithNetworkInstance(defaultNetworkInstanceName).
					WithIndex(uint64(j)).
					WithIPAddress(indexAsIPv4(uint32(i), 1)))
		})
	}

	log.V(2).Infof("doing programming")
	res := DoModifyOps(c, t, ops, wantACK, false)
	log.V(2).Infof("finished programming")

	log.V(2).Infof("doing check for %d nexthops", n)

	wants := []*client.OpResult{}
	for i := 0; i < n; i++ {
		wants = append(wants,
			fluent.OperationResult().
				WithNextHopOperation(uint64(i)+1).
				WithProgrammingResult(wantACK).
				WithOperationType(constants.Add).
				AsResult())
	}
	chk.HasResultsCache(t, res, wants, chk.IgnoreOperationID())
}

// GetBenchmarkNH benchmarks the performance of Get populating the server with N next-hop
// instances and measuring latency of the Get returned by the server. No validation of
// the returned contents is performed.
func GetBenchmarkNH(c *fluent.GRIBIClient, wantACK fluent.ProgrammingResult, t testing.TB, _ ...TestOpt) {
	for _, i := range []int{10, 100, 1000} {
		populateNNHs(c, i, wantACK, t)
		ctx := context.Background()
		c.Start(ctx, t)

		start := time.Now()
		_, err := c.Get().
			WithNetworkInstance(defaultNetworkInstanceName).
			WithAFT(fluent.NextHop).
			Send()
		end := time.Now()
		if err != nil {
			flushServer(c, t)
			t.Fatalf("got unexpected error, %v", err)
		}

		latency := end.Sub(start).Nanoseconds()
		fmt.Printf("latency for %d NHs: %d\n", i, latency)
		flushServer(c, t)
		c.Stop(t)
	}
}

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
	"github.com/openconfig/gribigo/server"
)

// GetNH validates that an installed next-hop is returned via the Get RPC.
func GetNH(c *fluent.GRIBIClient, wantACK fluent.ProgrammingResult, t testing.TB) {
	ops := []func(){
		func() {
			c.Modify().AddEntry(t,
				fluent.NextHopEntry().
					WithNetworkInstance(server.DefaultNetworkInstanceName).
					WithIndex(1).
					WithIPAddress("1.1.1.1"))
		},
	}

	res := doModifyOps(c, t, ops, wantACK, false)

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
		WithNetworkInstance(server.DefaultNetworkInstanceName).
		WithAFT(fluent.NextHop).
		Send()

	// TODO(robjs): add checking for get responses, requires new
	// fluent/check library for this.
	_, _ = gr, err
}

// TODO(robjs): get NHG
// TODO(robjs): get ipv4 entry

func indexAsIPv4(i uint32, baseSlashEight int) string {
	ip := make(net.IP, 4)
	binary.BigEndian.PutUint32(ip, uint32(i)+uint32(baseSlashEight*16777216))
	return ip.String()
}

func populateNNHs(c *fluent.GRIBIClient, n int, wantACK fluent.ProgrammingResult, t testing.TB) {
	ops := []func(){}
	for i := 0; i < n; i++ {
		j := i + 1
		ops = append(ops, func() {
			c.Modify().AddEntry(t,
				fluent.NextHopEntry().
					WithNetworkInstance(server.DefaultNetworkInstanceName).
					WithIndex(uint64(j)).
					WithIPAddress(indexAsIPv4(uint32(i), 1)))
		})
	}

	log.V(2).Infof("doing programming")
	res := doModifyOps(c, t, ops, wantACK, false)
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

func GetBenchmarkNH(c *fluent.GRIBIClient, wantACK fluent.ProgrammingResult, t testing.TB) {
	for _, i := range []int{10, 100, 1000} {
		populateNNHs(c, i, wantACK, t)
		ctx := context.Background()
		c.Start(ctx, t)

		start := time.Now()
		_, err := c.Get().
			WithNetworkInstance(server.DefaultNetworkInstanceName).
			WithAFT(fluent.NextHop).
			Send()
		end := time.Now()
		if err != nil {
			t.Fatalf("got unexpected error, %v", err)
		}

		latency := end.Sub(start).Nanoseconds()
		fmt.Printf("latency for %d NHs: %d\n", i, latency)
		c.Stop(t)
	}
}

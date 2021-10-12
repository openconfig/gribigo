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
	"fmt"
	"testing"

	"github.com/openconfig/gribigo/chk"
	"github.com/openconfig/gribigo/constants"
	"github.com/openconfig/gribigo/fluent"
	"github.com/openconfig/gribigo/server"
)

func doGet(c *fluent.GRIBIClient, t testing.TB, gets []func()) *spb.GetResponse {
	ctx := context.Background()
	c.Start(ctx, t)
	defer c.Stop(t)

}

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

	gr, err := c.Get().
		WithNetworkInstance(server.DefaultNetworkInstanceName).
		WithAFT(fluent.NextHop).
		Send()
	fmt.Printf("%v, %v", gr, err)

}

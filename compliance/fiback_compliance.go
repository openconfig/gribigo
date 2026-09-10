// Copyright 2025 Google LLC
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
	"testing"

	"github.com/openconfig/gribigo/chk"
	"github.com/openconfig/gribigo/constants"
	"github.com/openconfig/gribigo/fluent"
)

var (
	FibAckComplianceTestSuite = []*TestSpec{{
		In: Test{
			Fn:                                  IPv4EntryMissingNextHopGroup,
			Reference:                           "TE-3.1.2",
			ShortName:                           "Error: Missing NextHopGroup for the IPv4Entry",
			RequiresDisallowedForwardReferences: true,
		},
	}, {
		In: Test{
			Fn:                                  IPv4EntryEmptyNextHopGroup,
			Reference:                           "TE-3.1.3",
			ShortName:                           "Error: Empty NextHopGroup for the IPv4Entry",
			RequiresDisallowedForwardReferences: true,
		},
	}}
)

// IPv4EntryMissingNextHopGroup - Missing NextHopGroup for the IPv4Entry 203.0.113.1/32.
func IPv4EntryMissingNextHopGroup(c *fluent.GRIBIClient, t testing.TB, _ ...TestOpt) {
	defer flushServer(c, t)

	ops := []func(){
		func() {
			c.Modify().AddEntry(t, fluent.NextHopEntry().WithNetworkInstance(defaultNetworkInstanceName).WithIndex(1).WithIPAddress("192.0.2.3"))
			// NB: NHG 11 has not been defined
			c.Modify().AddEntry(t, fluent.IPv4Entry().WithPrefix("203.0.113.1/32").WithNetworkInstance(defaultNetworkInstanceName).WithNextHopGroup(11))
		},
	}

	res := DoModifyOps(c, t, ops, fluent.InstalledInFIB, false)

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithNextHopOperation(1).
			WithOperationType(constants.Add).
			WithProgrammingResult(fluent.InstalledInFIB).
			AsResult(),
		chk.IgnoreOperationID())

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithIPv4Operation("203.0.113.1/32").
			WithOperationType(constants.Add).
			WithProgrammingResult(fluent.ProgrammingFailed).
			AsResult(),
		chk.IgnoreOperationID())
}

// IPv4EntryEmptyNextHopGroup - Empty NextHopGroup for the IPv4Entry 203.0.113.1/32.
func IPv4EntryEmptyNextHopGroup(c *fluent.GRIBIClient, t testing.TB, _ ...TestOpt) {
	defer flushServer(c, t)

	ops := []func(){
		func() {
			c.Modify().AddEntry(t, fluent.NextHopEntry().WithNetworkInstance(defaultNetworkInstanceName).WithIndex(1).WithIPAddress("192.0.2.3"))
			// // NB: NHG is specified to not include any NHs
			c.Modify().AddEntry(t, fluent.NextHopGroupEntry().WithNetworkInstance(defaultNetworkInstanceName).WithID(11))
			c.Modify().AddEntry(t, fluent.IPv4Entry().WithPrefix("203.0.113.1/32").WithNetworkInstance(defaultNetworkInstanceName).WithNextHopGroup(11))
		},
	}

	res := DoModifyOps(c, t, ops, fluent.InstalledInFIB, false)

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithNextHopOperation(1).
			WithOperationType(constants.Add).
			WithProgrammingResult(fluent.InstalledInFIB).
			AsResult(),
		chk.IgnoreOperationID())

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithNextHopGroupOperation(11).
			WithOperationType(constants.Add).
			WithProgrammingResult(fluent.ProgrammingFailed).
			AsResult(),
		chk.IgnoreOperationID())

	chk.HasResult(t, res,
		fluent.OperationResult().
			WithIPv4Operation("203.0.113.1/32").
			WithOperationType(constants.Add).
			WithProgrammingResult(fluent.ProgrammingFailed).
			AsResult(),
		chk.IgnoreOperationID())
}

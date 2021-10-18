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
	"github.com/openconfig/gribigo/fluent"
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

// TestDifferingElectionParameters checks that when a client A is connected with parameters
// that differ to those that are used by a new client B an error is returned to client B.
//
// opts must contain a SecondClient option such that there is a second stub to be used to
// the device.
func TestDifferingElectionParameters(c *fluent.GRIBIClient, t testing.TB, opts ...TestOpt) {
	defer electionID.Inc()

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

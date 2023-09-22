// Copyright 2023 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package reconciler reconciles the contents of two gRIBI RIBs -- the intended RIB
// is assumed to contain the desired RIB entries, whereas the 'target' RIB is to be
// programmed. The reconciler:
//
//   - Uses the messages that are returned from the `Get` RPC to build the contents of
//     an external RIB.
//   - Calculates a diff between the two RIBs.
//   - Sends gRIBI operations to the target RIB to make it consistent with the
//     intended RIB.
package reconciler

import (
	"context"
	"fmt"

	"github.com/openconfig/gribigo/rib"

	spb "github.com/openconfig/gribi/v1/proto/service"
)

type R struct {
	intended, target RIBTarget

	// intended is a RIB containing the AFT entries that are intended to be
	// programmed by the reconciler.
	lastIntended *rib.RIB
	// lastTarget is a cache of the last RIB entries that were returned from
	// the target RIB.
	lastTarget *rib.RIB
}

// RIBTarget is an interface that abstracts a local and remote RIB in the
// reconciler. It allows the RIB contents to be retrieved and programmed either
// via gRIBI or from a local RIB cache.
type RIBTarget interface {
	// Get returns a RIB containing all network-instances and AFTs that are
	// supported by the RIB.
	Get(context.Context) (*rib.RIB, error)
	// CleanUp is called to indicate that the RIBTarget should remove any
	// state or external connections as it is no longer required.
	CleanUp()
}

// LocalRIB wraps a RIB that is locally available on the system as a gRIBIgo
// RIB type.
type LocalRIB struct {
	r *rib.RIB
}

// Get returns the contents of the local RIB.
func (l *LocalRIB) Get(_ context.Context) (*rib.RIB, error) {
	return l.r, nil
}

// CleanUp implements the RIBTarget interface. No local cleanup is required.
func (l *LocalRIB) CleanUp() {}

var (
	// Compile time check that LocalRIB implements the RIBTarget interface.
	_ RIBTarget = &LocalRIB{}
)

// New returns a new reconciler with the specified intended and target RIBs.
func New(intended, target RIBTarget) *R {
	return &R{
		intended: intended,
		target:   target,
	}
}

// Reconcile performs a reconciliation operation between the intended and specified
// remote RIB.
func (r *R) Reconcile(ctx context.Context) error {
	// Get the current contents of intended and target.
	iRIB, err := r.intended.Get(ctx)
	if err != nil {
		return fmt.Errorf("cannot reconcile RIBs, cannot get contents of intended, %v", err)
	}

	tRIB, err := r.target.Get(ctx)
	if err != nil {
		return fmt.Errorf("cannot reconcile RIBs, cannot get contents of target, %v", err)
	}

	// Perform diff on their contents.

	diffs, err := diff(iRIB, tRIB)
	if err != nil {
		return fmt.Errorf("cannot reconcile RIBs, cannot calculate diff, %v", err)
	}
	_ = diffs

	// Enqueue the operations towards target that bring it in-line with intended.
	// TODO(robjs): Implement enqueuing in client.
	return fmt.Errorf("reconciliation unimplemented")

}

// diff returns the difference between the src and dst RIBs expressed as gRIBI
// AFTOperations. That is to say, for each network instance RIB within the RIBs:
//
//   - entries that are present in src but not dst are returned as ADD
//     operations.
//   - entries that are present in src but not dst and their contents diff are
//     returned as MODIFY operations.
//   - entries that are not present in src but are present in dst are returned
//     as DELETE operations.
func diff(src, dst *rib.RIB) ([]*spb.AFTOperation, error) {
	return nil, fmt.Errorf("unimplemented")
}

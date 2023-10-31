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
	"reflect"
	"sync/atomic"

	"github.com/openconfig/gribigo/aft"
	"github.com/openconfig/gribigo/rib"

	spb "github.com/openconfig/gribi/v1/proto/service"
)

type R struct {
	// intended and target are the mechanisms by which to access the intended
	// RIB (source of truth) and the target it is to be reconciled with.
	intended, target RIBTarget
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

// NewLocalRIB returns a new LocalRIB instance.
func NewLocalRIB(r *rib.RIB) *LocalRIB {
	return &LocalRIB{r: r}
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

// Reconcile calculates the required gRIBI actions to institute a reconciliation
// operation between the intended and  remote RIB. The specified ID is used as the
// base for the operation ID within gRIBI. Reconcile returns a set of operations
// in the form of a ReconcileOps struct.
//
// Within the returned ReconcileOps:
//   - The Add field indicates entries that are to be newly added to the remote RIB.
//   - The Replace field indicates entries that are replacing entries within the remote RIB,
//     the replaces will be implicit (i.e., expressed as gRIBI ADD operations) unless the
//     ExplicitReplace option is provided.
//   - The Delete field indicates entries that are to be deleted from the remote RIB.
//
// Within each of these fields, entries are broken down into "top-level" entries which are from
// the IPv4, IPv6, or MPLS AFTs, and those that correspond to next-hop-group (NHG) or next-hop (NH)
// entries. This allows the client receiving these entries to enqueue them in the correct order
// to ensure that there are no forward references, and implement make-before-break.
func (r *R) Reconcile(ctx context.Context, id *atomic.Uint64) (*ReconcileOps, error) {
	// Get the current contents of intended and target.
	iRIB, err := r.intended.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot reconcile RIBs, cannot get contents of intended, %v", err)
	}

	tRIB, err := r.target.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot reconcile RIBs, cannot get contents of target, %v", err)
	}

	// Perform diff on their contents.
	// TODO(robjs): Plumb through explicitReplace map.
	diffs, err := diff(iRIB, tRIB, nil, id)
	if err != nil {
		return nil, fmt.Errorf("cannot reconcile RIBs, cannot calculate diff, %v", err)
	}
	return diffs, nil
}

// Ops stores a set of operations with their corresponding types. Operations
// are stored as NH (nexthop), NHG (next-hop-group) and top-level (MPLS, IPv4,
// IPv6). This allows a gRIBI client to sequence the Ops suitably.
type Ops struct {
	// NH stores the next-hop operations in the operation set.
	NH []*spb.AFTOperation
	// NHG stores the next-hop-group operations in the operation set.
	NHG []*spb.AFTOperation
	// TopLevel stores the IPv4, IPv6, and MPLS operations in the operation set.
	TopLevel []*spb.AFTOperation
}

// ReconcileOps stores the operations that are required for a specific reconciliation
// run.
type ReconcileOps struct {
	// Add stores the operations that are explicitly adding new entries.
	Add *Ops
	// Replace stores the operations that are implicit or explicit replaces of
	// existing entries.
	Replace *Ops
	// Delete stores the operations that are removing entries.
	Delete *Ops
}

// NewReconcileOps returns a new reconcileOps struct with the fields initialised.
func NewReconcileOps() *ReconcileOps {
	return &ReconcileOps{
		Add:     &Ops{},
		Replace: &Ops{},
		Delete:  &Ops{},
	}
}

// diff returns the difference between the src and dst RIBs expressed as gRIBI
// AFTOperations. That is to say, for each network instance RIB within the RIBs:
//
//   - entries that are present in src but not dst are returned as ADD
//     operations.
//   - entries that are present in src but not dst and their contents diff are
//     returned as ADD operations. This takes advantage of the implicit replace
//     functionality implemented by gRIBI.
//   - entries that are not present in src but are present in dst are returned
//     as DELETE operations.
//
// If an entry within the explicitReplace map is set to true then explicit, rather
// than implicit replaces are generated for that function.
func diff(src, dst *rib.RIB, explicitReplace map[spb.AFTType]bool, id *atomic.Uint64) (*ReconcileOps, error) {
	if src == nil || dst == nil {
		return nil, fmt.Errorf("invalid nil input RIBs, src: %v, dst: %v", src, dst)
	}

	// Re-map ALL into the supported address families.
	if _, ok := explicitReplace[spb.AFTType_ALL]; ok {
		explicitReplace = map[spb.AFTType]bool{
			spb.AFTType_IPV4:          true,
			spb.AFTType_MPLS:          true,
			spb.AFTType_NEXTHOP:       true,
			spb.AFTType_NEXTHOP_GROUP: true,
		}
	}

	srcContents, err := src.RIBContents()
	if err != nil {
		return nil, fmt.Errorf("cannot copy source RIB contents, err: %v", err)
	}
	dstContents, err := dst.RIBContents()
	if err != nil {
		return nil, fmt.Errorf("cannot copy destination RIB contents, err: %v", err)
	}

	ops := NewReconcileOps()

	for srcNI, srcNIEntries := range srcContents {
		dstNIEntries, ok := dstContents[srcNI]
		if !ok {
			dstNIEntries = &aft.RIB{}
			dstNIEntries.GetOrCreateAfts()
		}

		// For each AFT:
		//  * if a key is present in src but not in dst -> generate an ADD
		//  * if a key is present in src and in dst -> diff, and generate an ADD if the contents differ.
		//  * if a key is present in dst, but not in src -> generate a DELETE.
		for pfx, srcE := range srcNIEntries.GetAfts().Ipv4Entry {
			if dstE, ok := dstNIEntries.GetAfts().Ipv4Entry[pfx]; !ok || !reflect.DeepEqual(srcE, dstE) {
				opType := spb.AFTOperation_ADD
				if ok && explicitReplace[spb.AFTType_IPV4] {
					opType = spb.AFTOperation_REPLACE
				}
				id.Add(1)
				op, err := v4Operation(opType, srcNI, id, srcE)
				if err != nil {
					return nil, err
				}

				// If this entry already exists then this is an addition rather than a replace.
				switch ok {
				case true:
					ops.Replace.TopLevel = append(ops.Replace.TopLevel, op)
				case false:
					ops.Add.TopLevel = append(ops.Add.TopLevel, op)
				}
			}
		}

		for lbl, srcE := range srcNIEntries.GetAfts().LabelEntry {
			if dstE, ok := dstNIEntries.GetAfts().LabelEntry[lbl]; !ok || !reflect.DeepEqual(srcE, dstE) {
				opType := spb.AFTOperation_ADD
				if ok && explicitReplace[spb.AFTType_MPLS] {
					opType = spb.AFTOperation_REPLACE
				}
				id.Add(1)
				op, err := mplsOperation(opType, srcNI, id, srcE)
				if err != nil {
					return nil, err
				}

				// If this entry already exists then this is an addition rather than a replace.
				switch ok {
				case true:
					ops.Replace.TopLevel = append(ops.Replace.TopLevel, op)
				case false:
					ops.Add.TopLevel = append(ops.Add.TopLevel, op)
				}
			}
		}

		for nhgID, srcE := range srcNIEntries.GetAfts().NextHopGroup {
			if dstE, ok := dstNIEntries.GetAfts().NextHopGroup[nhgID]; !ok || !reflect.DeepEqual(srcE, dstE) {
				opType := spb.AFTOperation_ADD
				if ok && explicitReplace[spb.AFTType_NEXTHOP_GROUP] {
					opType = spb.AFTOperation_REPLACE
				}
				id.Add(1)
				op, err := nhgOperation(opType, srcNI, id, srcE)
				if err != nil {
					return nil, err
				}

				// If this entry already exists then this is an addition rather than a replace.
				switch ok {
				case true:
					ops.Replace.NHG = append(ops.Replace.NHG, op)
				case false:
					ops.Add.NHG = append(ops.Add.NHG, op)
				}
			}
		}

		for nhID, srcE := range srcNIEntries.GetAfts().NextHop {
			if dstE, ok := dstNIEntries.GetAfts().NextHop[nhID]; !ok || !reflect.DeepEqual(srcE, dstE) {
				opType := spb.AFTOperation_ADD
				if ok && explicitReplace[spb.AFTType_NEXTHOP] {
					opType = spb.AFTOperation_REPLACE
				}
				id.Add(1)
				op, err := nhOperation(opType, srcNI, id, srcE)
				if err != nil {
					return nil, err
				}

				// If this entry already exists then this is an addition rather than a replace.
				switch ok {
				case true:
					ops.Replace.NH = append(ops.Replace.NH, op)
				case false:
					ops.Add.NH = append(ops.Add.NH, op)
				}
			}
		}

		// Delete operations.
		for pfx, dstE := range dstNIEntries.GetAfts().Ipv4Entry {
			if _, ok := srcNIEntries.GetAfts().Ipv4Entry[pfx]; !ok {
				id.Add(1)
				op, err := v4Operation(spb.AFTOperation_DELETE, srcNI, id, dstE)
				if err != nil {
					return nil, err
				}
				ops.Delete.TopLevel = append(ops.Delete.TopLevel, op)
			}
		}

		for lbl, dstE := range dstNIEntries.GetAfts().LabelEntry {
			if _, ok := srcNIEntries.GetAfts().LabelEntry[lbl]; !ok {
				id.Add(1)
				op, err := mplsOperation(spb.AFTOperation_DELETE, srcNI, id, dstE)
				if err != nil {
					return nil, err
				}
				ops.Delete.TopLevel = append(ops.Delete.TopLevel, op)
			}
		}

		for nhgID, dstE := range dstNIEntries.GetAfts().NextHopGroup {
			if _, ok := srcNIEntries.GetAfts().NextHopGroup[nhgID]; !ok {
				id.Add(1)
				op, err := nhgOperation(spb.AFTOperation_DELETE, srcNI, id, dstE)
				if err != nil {
					return nil, err
				}
				ops.Delete.NHG = append(ops.Delete.NHG, op)
			}
		}

		for nhID, dstE := range dstNIEntries.GetAfts().NextHop {
			if _, ok := srcNIEntries.GetAfts().NextHop[nhID]; !ok {
				id.Add(1)
				op, err := nhOperation(spb.AFTOperation_DELETE, srcNI, id, dstE)
				if err != nil {
					return nil, err
				}
				ops.Delete.NH = append(ops.Delete.NH, op)
			}
		}
	}

	return ops, nil
}

// v4Operation builds a gRIBI IPv4 operation with the specified method corresponding to a
// prefix in the network instance ni, using the specified ID for the operation. The contents
// of the operation are the entry e.
func v4Operation(method spb.AFTOperation_Operation, ni string, id *atomic.Uint64, e *aft.Afts_Ipv4Entry) (*spb.AFTOperation, error) {
	p, err := rib.ConcreteIPv4Proto(e)
	if err != nil {
		return nil, fmt.Errorf("cannot create operation for prefix %s, %v", e.GetPrefix(), err)
	}
	return &spb.AFTOperation{
		Id:              id.Load(),
		NetworkInstance: ni,
		Op:              method,
		Entry: &spb.AFTOperation_Ipv4{
			Ipv4: p,
		},
	}, nil
}

// nhgOperation builds a gRIBI NHG operation with the specified method, corresponding to a
// NHG in network instance ni, using the specified ID for the operation. The
// contents of the operation are the entry e.
func nhgOperation(method spb.AFTOperation_Operation, ni string, id *atomic.Uint64, e *aft.Afts_NextHopGroup) (*spb.AFTOperation, error) {
	p, err := rib.ConcreteNextHopGroupProto(e)
	if err != nil {
		return nil, fmt.Errorf("cannot create operation for NHG %d, %v", e.GetId(), err)
	}
	return &spb.AFTOperation{
		Id:              id.Load(),
		NetworkInstance: ni,
		Op:              method,
		Entry: &spb.AFTOperation_NextHopGroup{
			NextHopGroup: p,
		},
	}, nil
}

// nhOperation builds a gRIBI NH operation with the specified method, corresponding to a
// NH in network instance ni, using the specified ID for the operation. The contents
// of the operation are the entry e.
func nhOperation(method spb.AFTOperation_Operation, ni string, id *atomic.Uint64, e *aft.Afts_NextHop) (*spb.AFTOperation, error) {
	p, err := rib.ConcreteNextHopProto(e)
	if err != nil {
		return nil, fmt.Errorf("cannot create operation for NH %d, %v", e.GetIndex(), err)
	}
	return &spb.AFTOperation{
		Id:              id.Load(),
		NetworkInstance: ni,
		Op:              method,
		Entry: &spb.AFTOperation_NextHop{
			NextHop: p,
		},
	}, nil
}

// mplsOperation builds a gRIBI LabelEntry operation with the specified method corresponding to
// the MPLS label entry lbl. The operation is targeted at network instance ni, and uses the specified
// ID. The contents of the operation are the entry e.
func mplsOperation(method spb.AFTOperation_Operation, ni string, id *atomic.Uint64, e *aft.Afts_LabelEntry) (*spb.AFTOperation, error) {
	p, err := rib.ConcreteMPLSProto(e)
	if err != nil {
		return nil, fmt.Errorf("cannot create operation for label %d, %v", e.GetLabel(), err)
	}
	return &spb.AFTOperation{
		Id:              id.Load(),
		NetworkInstance: ni,
		Op:              method,
		Entry: &spb.AFTOperation_Mpls{
			Mpls: p,
		},
	}, nil
}

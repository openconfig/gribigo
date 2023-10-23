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

	"github.com/openconfig/gribigo/aft"
	"github.com/openconfig/gribigo/rib"
	"k8s.io/klog"

	spb "github.com/openconfig/gribi/v1/proto/service"
)

type R struct {
	// intended and target are the mechanisms by which to access the intended
	// RIB (source of truth) and the target it is to be reconciled with.
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
	// TODO(robjs): Plumb through explicitReplace map.
	diffs, err := diff(iRIB, tRIB, nil)
	if err != nil {
		return fmt.Errorf("cannot reconcile RIBs, cannot calculate diff, %v", err)
	}
	_ = diffs
	_, _ = r.lastIntended, r.lastTarget

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
//     returned as ADD operations. This takes advantage of the implicit replace
//     functionality implemented by gRIBI.
//   - entries that are not present in src but are present in dst are returned
//     as DELETE operations.
//
// If an entry within the explicitReplace map is set to true then explicit, rather
// than implicit replaces are generated for that function.
func diff(src, dst *rib.RIB, explicitReplace map[spb.AFTType]bool) ([]*spb.AFTOperation, error) {
	srcContents, err := src.RIBContents()
	if err != nil {
		return nil, fmt.Errorf("cannot copy source RIB contents, err: %v", err)
	}
	dstContents, err := dst.RIBContents()
	if err != nil {
		return nil, fmt.Errorf("cannot copy destination RIB contents, err: %v", err)
	}

	// Store the "top-level" operations (i.e., IPv4, IPv6, MPLS) and then the NHG and NHs
	// separately. This allows us to return the operations separately so that they can be
	// ordered in terms of programming. NHs need to be installed/replaced before NHGs, and
	// then subsequently top-level entries.
	topLevelOps, nhgOps, nhOps := []*spb.AFTOperation{}, []*spb.AFTOperation{}, []*spb.AFTOperation{}
	var id uint64
	for srcNI, srcNIEntries := range srcContents {
		dstNIEntries, ok := dstContents[srcNI]
		if !ok {
			// The network instance does not exist in the destination therefore
			// all entries are ADDs.
			for pfx, e := range srcNIEntries.GetAfts().Ipv4Entry {
				id++
				op, err := v4Operation(spb.AFTOperation_ADD, srcNI, pfx, id, e)
				if err != nil {
					return nil, err
				}
				topLevelOps = append(topLevelOps, op)
			}

			for nhgID, e := range srcNIEntries.GetAfts().NextHopGroup {
				id++
				op, err := nhgOperation(spb.AFTOperation_ADD, srcNI, nhgID, id, e)
				if err != nil {
					return nil, err
				}
				nhgOps = append(nhgOps, op)
			}

			continue
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
				id++
				op, err := v4Operation(opType, srcNI, pfx, id, srcE)
				if err != nil {
					return nil, err
				}
				topLevelOps = append(topLevelOps, op)
			}
		}

		for nhgID, srcE := range srcNIEntries.GetAfts().NextHopGroup {
			if dstE, ok := dstNIEntries.GetAfts().NextHopGroup[nhgID]; !ok || !reflect.DeepEqual(srcE, dstE) {
				opType := spb.AFTOperation_ADD
				if ok && explicitReplace[spb.AFTType_NEXTHOP_GROUP] {
					opType = spb.AFTOperation_REPLACE
				}
				id++
				op, err := nhgOperation(opType, srcNI, nhgID, id, srcE)
				if err != nil {
					return nil, err
				}
				nhgOps = append(nhgOps, op)
			}
		}

		for pfx, dstE := range dstNIEntries.GetAfts().Ipv4Entry {
			if _, ok := srcNIEntries.GetAfts().Ipv4Entry[pfx]; !ok {
				id++
				op, err := v4Operation(spb.AFTOperation_DELETE, srcNI, pfx, id, dstE)
				if err != nil {
					return nil, err
				}
				topLevelOps = append(topLevelOps, op)
			}
		}

		for nhgID, dstE := range dstNIEntries.GetAfts().NextHopGroup {
			if _, ok := srcNIEntries.GetAfts().NextHopGroup[nhgID]; !ok {
				id++
				op, err := nhgOperation(spb.AFTOperation_DELETE, srcNI, nhgID, id, dstE)
				if err != nil {
					return nil, err
				}
				nhgOps = append(nhgOps, op)
			}
		}

		if srcN, dstN := len(srcNIEntries.GetAfts().NextHop), len(dstNIEntries.GetAfts().NextHop); srcN != 0 || dstN != 0 {
			// TODO(robjs): Implement mapping of NH entries.
			klog.Warningf("next-hop reconcilation unimplemented, NHG entries, src: %d, dst: %d", srcN, dstN)
		}
	}

	ops := append([]*spb.AFTOperation{}, nhOps...)
	ops = append(ops, nhgOps...)
	ops = append(ops, topLevelOps...)

	return ops, nil
}

// v4Operation builds a gRIBI IPv4 operation with the specified method corresponding to the
// prefix pfx in network instance ni, using the specified ID for the operation. The contents
// of the operation are the entry e.
func v4Operation(method spb.AFTOperation_Operation, ni, pfx string, id uint64, e *aft.Afts_Ipv4Entry) (*spb.AFTOperation, error) {
	p, err := rib.ConcreteIPv4Proto(e)
	if err != nil {
		return nil, fmt.Errorf("cannot create operation for prefix %s, %v", pfx, err)
	}
	return &spb.AFTOperation{
		Id:              id,
		NetworkInstance: ni,
		Op:              method,
		Entry: &spb.AFTOperation_Ipv4{
			Ipv4: p,
		},
	}, nil
}

// nhgOperation builds a gRIBI NHG operation with the specified method, corresponding to the
// NHG ID nhgID, in network instance ni, using the specified ID for the operation. The
// contents of the operation are the entry e.
func nhgOperation(method spb.AFTOperation_Operation, ni string, nhgID, id uint64, e *aft.Afts_NextHopGroup) (*spb.AFTOperation, error) {
	p, err := rib.ConcreteNextHopGroupProto(e)
	if err != nil {
		return nil, fmt.Errorf("cannot create operation for NHG %d, %v", nhgID, err)
	}
	return &spb.AFTOperation{
		Id:              id,
		NetworkInstance: ni,
		Op:              method,
		Entry: &spb.AFTOperation_NextHopGroup{
			NextHopGroup: p,
		},
	}, nil
}

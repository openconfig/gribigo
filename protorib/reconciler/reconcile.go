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

// Package reconciler reconciles the contents of two protobuf-native gRIBI RIBs --
// the intended RIB is assumed to contain the desired RIB entries, whereas the
// target RIB is to be programmed. The reconciler:
//
//   - Uses the messages that are returned from the `Get` RPC to build the contents of
//     an external RIB.
//   - Calculates a diff between the two RIBs directly over their stored protobuf maps.
//   - Emits gRIBI operations to make the target RIB consistent with the intended RIB.
package reconciler

import (
	"bytes"
	"context"
	"fmt"
	"sync/atomic"

	"github.com/openconfig/gribigo/protorib"
	"google.golang.org/protobuf/proto"

	aftpb "github.com/openconfig/gribi/v1/proto/gribi_aft"
	spb "github.com/openconfig/gribi/v1/proto/service"
	wpb "github.com/openconfig/ygot/proto/ywrapper"
)

// R holds the intended and target RIB handles for a reconciliation session.
type R struct {
	// intended is the mechanism used to access the desired RIB state.
	intended RIBTarget
	// target is the mechanism used to access the target RIB state to be reconciled.
	target RIBTarget
}

// RIBTarget is an interface that abstracts a local and remote RIB in the
// reconciler. It allows the RIB contents to be retrieved and programmed either
// via gRIBI or from a local RIB cache.
type RIBTarget interface {
	// Get returns a RIB containing all network-instances and AFTs that are
	// supported by the RIB.
	Get(context.Context) (*protorib.RIB, error)
	// CleanUp is called to indicate that the RIBTarget should remove any
	// state or external connections as it is no longer required.
	CleanUp()
}

// LocalRIB wraps a RIB that is locally available on the system as a protorib
// RIB type.
type LocalRIB struct {
	// r is the underlying local protobuf-backed RIB.
	r *protorib.RIB
}

// NewLocalRIB returns a new LocalRIB instance wrapping r.
func NewLocalRIB(r *protorib.RIB) *LocalRIB {
	return &LocalRIB{r: r}
}

// Get returns the contents of the local RIB.
func (l *LocalRIB) Get(_ context.Context) (*protorib.RIB, error) {
	return l.r, nil
}

// CleanUp implements the RIBTarget interface. No local cleanup is required.
func (l *LocalRIB) CleanUp() {}

var (
	// _ verifies at compile time that LocalRIB implements the RIBTarget interface.
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
// operation between the intended and remote RIB. The specified id is used as the
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
	iRIB, err := r.intended.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot reconcile RIBs, cannot get contents of intended, %v", err)
	}

	tRIB, err := r.target.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot reconcile RIBs, cannot get contents of target, %v", err)
	}

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

// Merge adds the operations from the in operations to the receiver ops.
func (o *Ops) Merge(in *Ops) {
	if in == nil {
		return
	}
	o.NH = append(o.NH, in.NH...)
	o.NHG = append(o.NHG, in.NHG...)
	o.TopLevel = append(o.TopLevel, in.TopLevel...)
}

// IsEmpty determines whether the specified Ops contains any operations.
func (o *Ops) IsEmpty() bool {
	return o == nil || len(o.NH) == 0 && len(o.NHG) == 0 && len(o.TopLevel) == 0
}

// cloneAFTOpFallback clones op via protobuf reflection.
func cloneAFTOpFallback(op *spb.AFTOperation) *spb.AFTOperation {
	return proto.Clone(op).(*spb.AFTOperation)
}

// DeepCopy returns a deep copy (including cloning protobufs) of the specified
// operations.
func (o *Ops) DeepCopy() *Ops {
	if o == nil {
		return nil
	}
	return &Ops{
		NH:       protorib.CloneAFTOpSlice(o.NH, cloneAFTOpFallback),
		NHG:      protorib.CloneAFTOpSlice(o.NHG, cloneAFTOpFallback),
		TopLevel: protorib.CloneAFTOpSlice(o.TopLevel, cloneAFTOpFallback),
	}
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

// Merge adds the operations from the in operations to the receiver operations.
func (r *ReconcileOps) Merge(in *ReconcileOps) {
	if in == nil {
		return
	}
	r.Add.Merge(in.Add)
	r.Replace.Merge(in.Replace)
	r.Delete.Merge(in.Delete)
}

// IsEmpty determines whether the specified ReconcileOps contains any operations.
func (r *ReconcileOps) IsEmpty() bool {
	return r == nil || r.Add.IsEmpty() && r.Delete.IsEmpty() && r.Replace.IsEmpty()
}

// DeepCopy returns a copy of a ReconcileOps struct, making a deep copy of all
// of the fields, including cloning protobufs.
func (r *ReconcileOps) DeepCopy() *ReconcileOps {
	if r == nil {
		return nil
	}
	return &ReconcileOps{
		Add:     r.Add.DeepCopy(),
		Delete:  r.Delete.DeepCopy(),
		Replace: r.Replace.DeepCopy(),
	}
}

// reconcileOpsBacking co-locates a ReconcileOps struct with its three Ops
// sub-structs in a single heap allocation.
type reconcileOpsBacking struct {
	// r is the outer ReconcileOps value.
	r ReconcileOps
	// add backs r.Add.
	add Ops
	// replace backs r.Replace.
	replace Ops
	// del backs r.Delete.
	del Ops
}

// NewReconcileOps returns a new ReconcileOps struct with its fields initialised.
func NewReconcileOps() *ReconcileOps {
	b := &reconcileOpsBacking{}
	b.r.Add = &b.add
	b.r.Replace = &b.replace
	b.r.Delete = &b.del
	return &b.r
}

// emptyAFTs is a read-only zero-value AFTs struct used when a network instance
// has no entries.
var emptyAFTs protorib.AFTs

// diff returns the difference between the src and dst RIBs expressed as gRIBI
// AFTOperations. For each network instance RIB within the RIBs:
//
//   - entries that are present in src but not dst are returned as ADD
//     operations.
//   - entries that are present in both src and dst whose contents differ are
//     returned as ADD (implicit replace) or REPLACE operations.
//   - entries that are not present in src but are present in dst are returned
//     as DELETE operations.
//
// If an entry within the explicitReplace map is set to true then explicit, rather
// than implicit replaces are generated for that address family.
func diff(src, dst *protorib.RIB, explicitReplace map[spb.AFTType]bool, id *atomic.Uint64) (*ReconcileOps, error) {
	if src == nil || dst == nil {
		return nil, fmt.Errorf("invalid nil input RIBs, src: %v, dst: %v", src, dst)
	}

	if _, ok := explicitReplace[spb.AFTType_ALL]; ok {
		explicitReplace = map[spb.AFTType]bool{
			spb.AFTType_IPV4:          true,
			spb.AFTType_IPV6:          true,
			spb.AFTType_MPLS:          true,
			spb.AFTType_NEXTHOP:       true,
			spb.AFTType_NEXTHOP_GROUP: true,
		}
	}

	ops := NewReconcileOps()

	for _, srcNI := range src.KnownNetworkInstances() {
		srcRH, ok := src.NetworkInstanceRIB(srcNI)
		if !ok || srcRH == nil {
			continue
		}
		dstRH, ok := dst.NetworkInstanceRIB(srcNI)
		var err error
		switch {
		case !ok || dstRH == nil:
			err = srcRH.ReadAFTs(func(srcAFTs *protorib.AFTs) error {
				return diffAFTs(srcNI, srcAFTs, nil, explicitReplace, id, ops)
			})
		case srcRH == dstRH:
			err = srcRH.ReadAFTs(func(srcAFTs *protorib.AFTs) error {
				return diffAFTs(srcNI, srcAFTs, srcAFTs, explicitReplace, id, ops)
			})
		default:
			err = srcRH.ReadAFTs(func(srcAFTs *protorib.AFTs) error {
				return dstRH.ReadAFTs(func(dstAFTs *protorib.AFTs) error {
					return diffAFTs(srcNI, srcAFTs, dstAFTs, explicitReplace, id, ops)
				})
			})
		}
		if err != nil {
			return nil, err
		}
	}

	return ops, nil
}

// diffAFTs computes the gRIBI operations required to reconcile dstAFTs with
// srcAFTs in network instance srcNI and appends them to ops.
func diffAFTs(srcNI string, srcAFTs, dstAFTs *protorib.AFTs, explicitReplace map[spb.AFTType]bool, id *atomic.Uint64, ops *ReconcileOps) error {
	if srcAFTs == nil {
		srcAFTs = &emptyAFTs
	}
	if dstAFTs == nil {
		dstAFTs = &emptyAFTs
	}

	if len(dstAFTs.IPv4Entry) == 0 && len(dstAFTs.IPv6Entry) == 0 && len(dstAFTs.LabelEntry) == 0 {
		if addTop := len(srcAFTs.IPv4Entry) + len(srcAFTs.IPv6Entry) + len(srcAFTs.LabelEntry); addTop > 0 && cap(ops.Add.TopLevel)-len(ops.Add.TopLevel) < addTop {
			grown := make([]*spb.AFTOperation, len(ops.Add.TopLevel), len(ops.Add.TopLevel)+addTop)
			copy(grown, ops.Add.TopLevel)
			ops.Add.TopLevel = grown
		}
	}
	if len(dstAFTs.NextHopGroup) == 0 {
		if addNHG := len(srcAFTs.NextHopGroup); addNHG > 0 && cap(ops.Add.NHG)-len(ops.Add.NHG) < addNHG {
			grown := make([]*spb.AFTOperation, len(ops.Add.NHG), len(ops.Add.NHG)+addNHG)
			copy(grown, ops.Add.NHG)
			ops.Add.NHG = grown
		}
	}
	if len(dstAFTs.NextHop) == 0 {
		if addNH := len(srcAFTs.NextHop); addNH > 0 && cap(ops.Add.NH)-len(ops.Add.NH) < addNH {
			grown := make([]*spb.AFTOperation, len(ops.Add.NH), len(ops.Add.NH)+addNH)
			copy(grown, ops.Add.NH)
			ops.Add.NH = grown
		}
	}

	for pfx, srcE := range srcAFTs.IPv4Entry {
		if dstE, ok := dstAFTs.IPv4Entry[pfx]; !ok || !ipv4EntryEqual(srcE, dstE) {
			opType := spb.AFTOperation_ADD
			if ok && explicitReplace[spb.AFTType_IPV4] {
				opType = spb.AFTOperation_REPLACE
			}
			id.Add(1)
			op := v4Operation(opType, srcNI, id, srcE)
			switch ok {
			case true:
				ops.Replace.TopLevel = append(ops.Replace.TopLevel, op)
			case false:
				ops.Add.TopLevel = append(ops.Add.TopLevel, op)
			}
		}
	}

	for pfx, srcE := range srcAFTs.IPv6Entry {
		if dstE, ok := dstAFTs.IPv6Entry[pfx]; !ok || !ipv6EntryEqual(srcE, dstE) {
			opType := spb.AFTOperation_ADD
			if ok && explicitReplace[spb.AFTType_IPV6] {
				opType = spb.AFTOperation_REPLACE
			}
			id.Add(1)
			op := v6Operation(opType, srcNI, id, srcE)
			switch ok {
			case true:
				ops.Replace.TopLevel = append(ops.Replace.TopLevel, op)
			case false:
				ops.Add.TopLevel = append(ops.Add.TopLevel, op)
			}
		}
	}

	for lbl, srcE := range srcAFTs.LabelEntry {
		if dstE, ok := dstAFTs.LabelEntry[lbl]; !ok || !mplsEntryEqual(srcE, dstE) {
			opType := spb.AFTOperation_ADD
			if ok && explicitReplace[spb.AFTType_MPLS] {
				opType = spb.AFTOperation_REPLACE
			}
			id.Add(1)
			op := mplsOperation(opType, srcNI, id, srcE)
			switch ok {
			case true:
				ops.Replace.TopLevel = append(ops.Replace.TopLevel, op)
			case false:
				ops.Add.TopLevel = append(ops.Add.TopLevel, op)
			}
		}
	}

	for nhgID, srcE := range srcAFTs.NextHopGroup {
		if dstE, ok := dstAFTs.NextHopGroup[nhgID]; !ok || !nextHopGroupEqual(srcE, dstE) {
			opType := spb.AFTOperation_ADD
			if ok && explicitReplace[spb.AFTType_NEXTHOP_GROUP] {
				opType = spb.AFTOperation_REPLACE
			}
			id.Add(1)
			op := nhgOperation(opType, srcNI, id, srcE)
			switch ok {
			case true:
				ops.Replace.NHG = append(ops.Replace.NHG, op)
			case false:
				ops.Add.NHG = append(ops.Add.NHG, op)
			}
		}
	}

	for nhID, srcE := range srcAFTs.NextHop {
		if dstE, ok := dstAFTs.NextHop[nhID]; !ok || !nextHopEqual(srcE, dstE) {
			opType := spb.AFTOperation_ADD
			if ok && explicitReplace[spb.AFTType_NEXTHOP] {
				opType = spb.AFTOperation_REPLACE
			}
			id.Add(1)
			op := nhOperation(opType, srcNI, id, srcE)
			switch ok {
			case true:
				ops.Replace.NH = append(ops.Replace.NH, op)
			case false:
				ops.Add.NH = append(ops.Add.NH, op)
			}
		}
	}

	for pfx, dstE := range dstAFTs.IPv4Entry {
		if _, ok := srcAFTs.IPv4Entry[pfx]; !ok {
			id.Add(1)
			ops.Delete.TopLevel = append(ops.Delete.TopLevel, v4Operation(spb.AFTOperation_DELETE, srcNI, id, dstE))
		}
	}

	for pfx, dstE := range dstAFTs.IPv6Entry {
		if _, ok := srcAFTs.IPv6Entry[pfx]; !ok {
			id.Add(1)
			ops.Delete.TopLevel = append(ops.Delete.TopLevel, v6Operation(spb.AFTOperation_DELETE, srcNI, id, dstE))
		}
	}

	for lbl, dstE := range dstAFTs.LabelEntry {
		if _, ok := srcAFTs.LabelEntry[lbl]; !ok {
			id.Add(1)
			ops.Delete.TopLevel = append(ops.Delete.TopLevel, mplsOperation(spb.AFTOperation_DELETE, srcNI, id, dstE))
		}
	}

	for nhgID, dstE := range dstAFTs.NextHopGroup {
		if _, ok := srcAFTs.NextHopGroup[nhgID]; !ok {
			id.Add(1)
			ops.Delete.NHG = append(ops.Delete.NHG, nhgOperation(spb.AFTOperation_DELETE, srcNI, id, dstE))
		}
	}

	for nhID, dstE := range dstAFTs.NextHop {
		if _, ok := srcAFTs.NextHop[nhID]; !ok {
			id.Add(1)
			ops.Delete.NH = append(ops.Delete.NH, nhOperation(spb.AFTOperation_DELETE, srcNI, id, dstE))
		}
	}

	return nil
}

// uintValueEqual reports whether two UintValue wrapper protobufs are equal.
func uintValueEqual(a, b *wpb.UintValue) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Value == b.Value
}

// stringValueEqual reports whether two StringValue wrapper protobufs are equal.
func stringValueEqual(a, b *wpb.StringValue) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Value == b.Value
}

// boolValueEqual reports whether two BoolValue wrapper protobufs are equal.
func boolValueEqual(a, b *wpb.BoolValue) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Value == b.Value
}

// bytesValueEqual reports whether two BytesValue wrapper protobufs are equal.
func bytesValueEqual(a, b *wpb.BytesValue) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return bytes.Equal(a.Value, b.Value)
}

// ipv4EntryEqual reports whether two Afts_Ipv4EntryKey protobufs are deeply equal.
func ipv4EntryEqual(a, b *aftpb.Afts_Ipv4EntryKey) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil || a.Prefix != b.Prefix {
		return false
	}
	ae, be := a.Ipv4Entry, b.Ipv4Entry
	if ae == be {
		return true
	}
	if ae == nil || be == nil {
		return false
	}
	return ae.DecapsulateHeader == be.DecapsulateHeader &&
		uintValueEqual(ae.NextHopGroup, be.NextHopGroup) &&
		stringValueEqual(ae.NextHopGroupNetworkInstance, be.NextHopGroupNetworkInstance) &&
		bytesValueEqual(ae.EntryMetadata, be.EntryMetadata)
}

// ipv6EntryEqual reports whether two Afts_Ipv6EntryKey protobufs are deeply equal.
func ipv6EntryEqual(a, b *aftpb.Afts_Ipv6EntryKey) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil || a.Prefix != b.Prefix {
		return false
	}
	ae, be := a.Ipv6Entry, b.Ipv6Entry
	if ae == be {
		return true
	}
	if ae == nil || be == nil {
		return false
	}
	return ae.DecapsulateHeader == be.DecapsulateHeader &&
		uintValueEqual(ae.NextHopGroup, be.NextHopGroup) &&
		stringValueEqual(ae.NextHopGroupNetworkInstance, be.NextHopGroupNetworkInstance) &&
		bytesValueEqual(ae.EntryMetadata, be.EntryMetadata)
}

// poppedMPLSStackEqual reports whether two PoppedMplsLabelStackUnion slices are deeply equal.
func poppedMPLSStackEqual(a, b []*aftpb.Afts_LabelEntry_PoppedMplsLabelStackUnion) bool {
	if len(a) != len(b) {
		return false
	}
	for i, au := range a {
		bu := b[i]
		if au == bu {
			continue
		}
		if au == nil || bu == nil {
			return false
		}
		if au.PoppedMplsLabelStackUint64 != bu.PoppedMplsLabelStackUint64 ||
			au.PoppedMplsLabelStackOpenconfigmplstypesmplslabelenum != bu.PoppedMplsLabelStackOpenconfigmplstypesmplslabelenum {
			return false
		}
	}
	return true
}

// mplsEntryEqual reports whether two Afts_LabelEntryKey protobufs are deeply equal.
func mplsEntryEqual(a, b *aftpb.Afts_LabelEntryKey) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	ak, aErr := protorib.MPLSLabelKeyFromProto(a)
	bk, bErr := protorib.MPLSLabelKeyFromProto(b)
	if aErr != nil || bErr != nil || ak != bk {
		return false
	}
	ae, be := a.LabelEntry, b.LabelEntry
	if ae == be {
		return true
	}
	if ae == nil || be == nil {
		return false
	}
	return uintValueEqual(ae.NextHopGroup, be.NextHopGroup) &&
		stringValueEqual(ae.NextHopGroupNetworkInstance, be.NextHopGroupNetworkInstance) &&
		bytesValueEqual(ae.EntryMetadata, be.EntryMetadata) &&
		poppedMPLSStackEqual(ae.PoppedMplsLabelStack, be.PoppedMplsLabelStack)
}

// nextHopGroupNHEqual reports whether two Afts_NextHopGroup_NextHop protobufs are deeply equal.
func nextHopGroupNHEqual(a, b *aftpb.Afts_NextHopGroup_NextHop) bool {
	if a == b {
		return true
	}
	if a == nil {
		return b.GetWeight() == nil
	}
	if b == nil {
		return a.GetWeight() == nil
	}
	return uintValueEqual(a.Weight, b.Weight)
}

// nextHopGroupNHSetsEqual reports whether two NextHopKey slices contain the
// same keyed NextHop entries regardless of slice order.
func nextHopGroupNHSetsEqual(a, b []*aftpb.Afts_NextHopGroup_NextHopKey) bool {
	if len(a) != len(b) {
		return false
	}
	for i, aNH := range a {
		if aNH == nil {
			return false
		}
		if bNH := b[i]; bNH != nil && bNH.Index == aNH.Index {
			if !nextHopGroupNHEqual(aNH.NextHop, bNH.NextHop) {
				return false
			}
			continue
		}
		found := false
		for _, bNH := range b {
			if bNH != nil && bNH.Index == aNH.Index {
				if !nextHopGroupNHEqual(aNH.NextHop, bNH.NextHop) {
					return false
				}
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// nextHopGroupEqual reports whether two Afts_NextHopGroupKey protobufs are deeply equal.
func nextHopGroupEqual(a, b *aftpb.Afts_NextHopGroupKey) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil || a.Id != b.Id {
		return false
	}
	ae, be := a.NextHopGroup, b.NextHopGroup
	if ae == be {
		return true
	}
	if ae == nil || be == nil {
		return false
	}
	return uintValueEqual(ae.BackupNextHopGroup, be.BackupNextHopGroup) &&
		uintValueEqual(ae.Color, be.Color) &&
		nextHopGroupNHSetsEqual(ae.NextHop, be.NextHop)
}

// nextHopInterfaceRefEqual reports whether two Afts_NextHop_InterfaceRef protobufs are deeply equal.
func nextHopInterfaceRefEqual(a, b *aftpb.Afts_NextHop_InterfaceRef) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return stringValueEqual(a.Interface, b.Interface) &&
		uintValueEqual(a.Subinterface, b.Subinterface)
}

// nextHopGreEqual reports whether two Afts_NextHop_Gre protobufs are deeply equal.
func nextHopGreEqual(a, b *aftpb.Afts_NextHop_Gre) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return stringValueEqual(a.DstIp, b.DstIp) &&
		stringValueEqual(a.SrcIp, b.SrcIp) &&
		uintValueEqual(a.Ttl, b.Ttl)
}

// nextHopIPInIPEqual reports whether two Afts_NextHop_IpInIp protobufs are deeply equal.
func nextHopIPInIPEqual(a, b *aftpb.Afts_NextHop_IpInIp) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return stringValueEqual(a.DstIp, b.DstIp) &&
		stringValueEqual(a.SrcIp, b.SrcIp)
}

// pushedMPLSStackEqual reports whether two PushedMplsLabelStackUnion slices are deeply equal.
func pushedMPLSStackEqual(a, b []*aftpb.Afts_NextHop_PushedMplsLabelStackUnion) bool {
	if len(a) != len(b) {
		return false
	}
	for i, au := range a {
		bu := b[i]
		if au == bu {
			continue
		}
		if au == nil || bu == nil {
			return false
		}
		if au.PushedMplsLabelStackUint64 != bu.PushedMplsLabelStackUint64 ||
			au.PushedMplsLabelStackOpenconfigmplstypesmplslabelenum != bu.PushedMplsLabelStackOpenconfigmplstypesmplslabelenum {
			return false
		}
	}
	return true
}

// encapHeaderGreEqual reports whether two Afts_NextHop_EncapHeader_Gre protobufs are deeply equal.
func encapHeaderGreEqual(a, b *aftpb.Afts_NextHop_EncapHeader_Gre) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return stringValueEqual(a.DstIp, b.DstIp) &&
		stringValueEqual(a.SrcIp, b.SrcIp) &&
		uintValueEqual(a.Ttl, b.Ttl)
}

// encapHeaderIpv4Equal reports whether two Afts_NextHop_EncapHeader_Ipv4 protobufs are deeply equal.
func encapHeaderIpv4Equal(a, b *aftpb.Afts_NextHop_EncapHeader_Ipv4) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return stringValueEqual(a.DstIp, b.DstIp) &&
		stringValueEqual(a.SrcIp, b.SrcIp)
}

// encapHeaderIpv6Equal reports whether two Afts_NextHop_EncapHeader_Ipv6 protobufs are deeply equal.
func encapHeaderIpv6Equal(a, b *aftpb.Afts_NextHop_EncapHeader_Ipv6) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return stringValueEqual(a.DstIp, b.DstIp) &&
		stringValueEqual(a.SrcIp, b.SrcIp)
}

// encapHeaderMPLSStackEqual reports whether two MplsLabelStackUnion slices are deeply equal.
func encapHeaderMPLSStackEqual(a, b []*aftpb.Afts_NextHop_EncapHeader_Mpls_MplsLabelStackUnion) bool {
	if len(a) != len(b) {
		return false
	}
	for i, au := range a {
		bu := b[i]
		if au == bu {
			continue
		}
		if au == nil || bu == nil {
			return false
		}
		if au.MplsLabelStackUint64 != bu.MplsLabelStackUint64 ||
			au.MplsLabelStackOpenconfigmplstypesmplslabelenum != bu.MplsLabelStackOpenconfigmplstypesmplslabelenum {
			return false
		}
	}
	return true
}

// encapHeaderMplsEqual reports whether two Afts_NextHop_EncapHeader_Mpls protobufs are deeply equal.
func encapHeaderMplsEqual(a, b *aftpb.Afts_NextHop_EncapHeader_Mpls) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return uintValueEqual(a.TrafficClass, b.TrafficClass) &&
		encapHeaderMPLSStackEqual(a.MplsLabelStack, b.MplsLabelStack)
}

// encapHeaderUDPV4Equal reports whether two Afts_NextHop_EncapHeader_UdpV4 protobufs are deeply equal.
func encapHeaderUDPV4Equal(a, b *aftpb.Afts_NextHop_EncapHeader_UdpV4) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return uintValueEqual(a.Dscp, b.Dscp) &&
		stringValueEqual(a.DstIp, b.DstIp) &&
		uintValueEqual(a.DstUdpPort, b.DstUdpPort) &&
		uintValueEqual(a.IpTtl, b.IpTtl) &&
		stringValueEqual(a.SrcIp, b.SrcIp) &&
		uintValueEqual(a.SrcUdpPort, b.SrcUdpPort)
}

// encapHeaderUDPV6Equal reports whether two Afts_NextHop_EncapHeader_UdpV6 protobufs are deeply equal.
func encapHeaderUDPV6Equal(a, b *aftpb.Afts_NextHop_EncapHeader_UdpV6) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return uintValueEqual(a.Dscp, b.Dscp) &&
		stringValueEqual(a.DstIp, b.DstIp) &&
		uintValueEqual(a.DstUdpPort, b.DstUdpPort) &&
		uintValueEqual(a.IpTtl, b.IpTtl) &&
		stringValueEqual(a.SrcIp, b.SrcIp) &&
		uintValueEqual(a.SrcUdpPort, b.SrcUdpPort)
}

// encapHeaderEqual reports whether two Afts_NextHop_EncapHeader protobufs are deeply equal.
func encapHeaderEqual(a, b *aftpb.Afts_NextHop_EncapHeader) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Type == b.Type &&
		encapHeaderGreEqual(a.Gre, b.Gre) &&
		encapHeaderIpv4Equal(a.Ipv4, b.Ipv4) &&
		encapHeaderIpv6Equal(a.Ipv6, b.Ipv6) &&
		encapHeaderMplsEqual(a.Mpls, b.Mpls) &&
		encapHeaderUDPV4Equal(a.UdpV4, b.UdpV4) &&
		encapHeaderUDPV6Equal(a.UdpV6, b.UdpV6)
}

// encapHeaderSetsEqual reports whether two EncapHeaderKey slices contain the
// same keyed EncapHeader entries regardless of slice order.
func encapHeaderSetsEqual(a, b []*aftpb.Afts_NextHop_EncapHeaderKey) bool {
	if len(a) != len(b) {
		return false
	}
	for i, aEH := range a {
		if aEH == nil {
			return false
		}
		if bEH := b[i]; bEH != nil && bEH.Index == aEH.Index {
			if !encapHeaderEqual(aEH.EncapHeader, bEH.EncapHeader) {
				return false
			}
			continue
		}
		found := false
		for _, bEH := range b {
			if bEH != nil && bEH.Index == aEH.Index {
				if !encapHeaderEqual(aEH.EncapHeader, bEH.EncapHeader) {
					return false
				}
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// nextHopEqual reports whether two Afts_NextHopKey protobufs are deeply equal.
func nextHopEqual(a, b *aftpb.Afts_NextHopKey) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil || a.Index != b.Index {
		return false
	}
	ae, be := a.NextHop, b.NextHop
	if ae == be {
		return true
	}
	if ae == nil || be == nil {
		return false
	}
	return ae.DecapsulateHeader == be.DecapsulateHeader &&
		ae.EncapsulateHeader == be.EncapsulateHeader &&
		stringValueEqual(ae.IpAddress, be.IpAddress) &&
		stringValueEqual(ae.MacAddress, be.MacAddress) &&
		stringValueEqual(ae.NetworkInstance, be.NetworkInstance) &&
		boolValueEqual(ae.PopTopLabel, be.PopTopLabel) &&
		stringValueEqual(ae.TunnelSrcIpAddress, be.TunnelSrcIpAddress) &&
		uintValueEqual(ae.VniLabel, be.VniLabel) &&
		nextHopInterfaceRefEqual(ae.InterfaceRef, be.InterfaceRef) &&
		nextHopGreEqual(ae.Gre, be.Gre) &&
		nextHopIPInIPEqual(ae.IpInIp, be.IpInIp) &&
		pushedMPLSStackEqual(ae.PushedMplsLabelStack, be.PushedMplsLabelStack) &&
		encapHeaderSetsEqual(ae.EncapHeader, be.EncapHeader)
}

// v4Operation builds a gRIBI IPv4 operation with the specified method corresponding to a
// prefix in the network instance ni, using the specified id for the operation. The contents
// of the operation reference the immutable entry e directly.
func v4Operation(method spb.AFTOperation_Operation, ni string, id *atomic.Uint64, e *aftpb.Afts_Ipv4EntryKey) *spb.AFTOperation {
	var b struct {
		op    spb.AFTOperation
		entry spb.AFTOperation_Ipv4
	}
	b.entry.Ipv4 = e
	b.op.Id = id.Load()
	b.op.NetworkInstance = ni
	b.op.Op = method
	b.op.Entry = &b.entry
	return &b.op
}

// v6Operation builds a gRIBI IPv6 operation with the specified method corresponding to a
// prefix in the network instance ni, using the specified id for the operation. The contents
// of the operation reference the immutable entry e directly.
func v6Operation(method spb.AFTOperation_Operation, ni string, id *atomic.Uint64, e *aftpb.Afts_Ipv6EntryKey) *spb.AFTOperation {
	var b struct {
		op    spb.AFTOperation
		entry spb.AFTOperation_Ipv6
	}
	b.entry.Ipv6 = e
	b.op.Id = id.Load()
	b.op.NetworkInstance = ni
	b.op.Op = method
	b.op.Entry = &b.entry
	return &b.op
}

// nhgOperation builds a gRIBI NHG operation with the specified method corresponding to an
// NHG in network instance ni, using the specified id for the operation. The contents of
// the operation reference the immutable entry e directly.
func nhgOperation(method spb.AFTOperation_Operation, ni string, id *atomic.Uint64, e *aftpb.Afts_NextHopGroupKey) *spb.AFTOperation {
	var b struct {
		op    spb.AFTOperation
		entry spb.AFTOperation_NextHopGroup
	}
	b.entry.NextHopGroup = e
	b.op.Id = id.Load()
	b.op.NetworkInstance = ni
	b.op.Op = method
	b.op.Entry = &b.entry
	return &b.op
}

// nhOperation builds a gRIBI NH operation with the specified method corresponding to an
// NH in network instance ni, using the specified id for the operation. The contents of
// the operation reference the immutable entry e directly.
func nhOperation(method spb.AFTOperation_Operation, ni string, id *atomic.Uint64, e *aftpb.Afts_NextHopKey) *spb.AFTOperation {
	var b struct {
		op    spb.AFTOperation
		entry spb.AFTOperation_NextHop
	}
	b.entry.NextHop = e
	b.op.Id = id.Load()
	b.op.NetworkInstance = ni
	b.op.Op = method
	b.op.Entry = &b.entry
	return &b.op
}

// mplsOperation builds a gRIBI LabelEntry operation with the specified method corresponding to
// an MPLS label entry in network instance ni, using the specified id for the operation. The
// contents of the operation reference the immutable entry e directly.
func mplsOperation(method spb.AFTOperation_Operation, ni string, id *atomic.Uint64, e *aftpb.Afts_LabelEntryKey) *spb.AFTOperation {
	var b struct {
		op    spb.AFTOperation
		entry spb.AFTOperation_Mpls
	}
	b.entry.Mpls = e
	b.op.Id = id.Load()
	b.op.NetworkInstance = ni
	b.op.Op = method
	b.op.Entry = &b.entry
	return &b.op
}

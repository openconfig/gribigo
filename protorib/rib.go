// Copyright 2026 Google LLC
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

// Package protorib implements a protobuf-native RIB for a gRIBI server.
package protorib

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"sort"
	"sync"
	"time"

	log "github.com/golang/glog"
	"github.com/openconfig/gribigo/constants"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"

	aftpb "github.com/openconfig/gribi/v1/proto/gribi_aft"
	enumpb "github.com/openconfig/gribi/v1/proto/gribi_aft/enums"
	spb "github.com/openconfig/gribi/v1/proto/service"
	wpb "github.com/openconfig/ygot/proto/ywrapper"
)

// unixTS returns the current Unix timestamp in nanoseconds since the epoch.
// It is defined as a variable so unit tests can override it deterministically.
var unixTS = time.Now().UnixNano

// MPLSLabelKey identifies a label entry in the MPLS AFT map.
type MPLSLabelKey struct {
	// Enum is the enumerated MPLS label value when set.
	Enum enumpb.OpenconfigMplsTypesMplsLabelEnum
	// Label is the numeric uint64 MPLS label value when Enum is unset.
	Label uint64
}

// AFTs holds the protobuf-backed AFT maps for a single network instance.
type AFTs struct {
	// IPv4Entry maps an IPv4 CIDR prefix to its immutable entry protobuf.
	IPv4Entry map[string]*aftpb.Afts_Ipv4EntryKey
	// IPv6Entry maps an IPv6 CIDR prefix to its immutable entry protobuf.
	IPv6Entry map[string]*aftpb.Afts_Ipv6EntryKey
	// LabelEntry maps an MPLS label key to its immutable entry protobuf.
	LabelEntry map[MPLSLabelKey]*aftpb.Afts_LabelEntryKey
	// NextHopGroup maps a next-hop-group ID to its immutable entry protobuf.
	NextHopGroup map[uint64]*aftpb.Afts_NextHopGroupKey
	// NextHop maps a next-hop index to its immutable entry protobuf.
	NextHop map[uint64]*aftpb.Afts_NextHopKey
}

// RIBHookFn is a function executed as a post-change hook following a RIB modification.
type RIBHookFn func(constants.OpType, int64, string, proto.Message)

// ResolvedDetails is an interface implemented by any type returned as part of resolved AFT details.
type ResolvedDetails interface {
	isResolvedDetail()
}

// ResolvedEntryFn is a function called for entries that can be fully resolved within the RIB.
type ResolvedEntryFn func(ribs map[string]*AFTs, optype constants.OpType, netinst string, aft constants.AFT, key any, dets ...ResolvedDetails)

// RIBHolderCheckFunc is a function used to check whether a candidate RIB entry is eligible for an operation.
type RIBHolderCheckFunc func(constants.OpType, string, *AFTs) (bool, error)

// niObjKey identifies a NextHop or NextHopGroup by network instance name and numeric ID.
type niObjKey struct {
	// ni is the network instance name.
	ni string
	// id is the NextHop index or NextHopGroup ID.
	id uint64
}

// RIB stores a protobuf-native representation of a gRIBI RIB across network instances.
type RIB struct {
	// nrMu protects the niRIB map and postChangeHook.
	nrMu sync.RWMutex
	// niRIB maps each network instance name to its RIBHolder.
	niRIB map[string]*RIBHolder

	// defaultName is the name assigned to the default network instance.
	defaultName string
	// ribCheck indicates whether this RIB runs the consistency check function.
	ribCheck bool
	// disableForwardReferences indicates whether unresolved forward references are disallowed.
	disableForwardReferences bool

	// pendMu protects pendingEntries and the dependency index maps.
	pendMu sync.RWMutex
	// pendingEntries maps operation IDs to operations awaiting dependency resolution.
	pendingEntries map[uint64]*pendingEntry
	// pendingByNH indexes pending NextHopGroup operation IDs by the (network instance, NextHop index) they await.
	pendingByNH map[niObjKey]map[uint64]struct{}
	// pendingByNHG indexes pending route operation IDs by the (network instance, NextHopGroup ID) they await.
	pendingByNHG map[niObjKey]map[uint64]struct{}
	// indexedPending tracks which operation IDs in pendingEntries have been indexed in pendingByNH or pendingByNHG.
	indexedPending map[uint64]struct{}

	// postChangeHook is propagated to newly created network instance RIBHolders.
	postChangeHook RIBHookFn
	// resolvedEntryHook is called for top-level entries that resolve in the RIB.
	resolvedEntryHook ResolvedEntryFn
}

// RIBHolder holds the AFT maps and reference counts for a single network instance.
type RIBHolder struct {
	// name is the network instance name.
	name string

	// mu protects the AFTs structure r and postChangeHook.
	mu sync.RWMutex
	// r holds the protobuf-backed AFT maps for this network instance.
	r *AFTs

	// checkFn validates whether a candidate operation should proceed.
	checkFn func(op constants.OpType, a *AFTs) (bool, error)

	// postChangeHook is executed after each operation within the RIBHolder completes.
	postChangeHook RIBHookFn

	// refCounts stores reference counters for NextHops and NextHopGroups within the network instance.
	refCounts *niRefCounter

	// disableForwardRef indicates that this RIBHolder disallows references to entities that do not yet exist.
	disableForwardRef bool
}

// niRefCounter stores reference counters for a network instance.
type niRefCounter struct {
	// mu protects NextHop and NextHopGroup maps.
	mu sync.RWMutex
	// NextHop maps a NextHop index to its reference count.
	NextHop map[uint64]uint64
	// NextHopGroup maps a NextHopGroup ID to its reference count.
	NextHopGroup map[uint64]uint64
	// parent is the owning RIB when this RIBHolder uses the default RIB checkFn.
	parent *RIB
	// defaultCheckFnPtr is the code pointer of the default checkFn closure for this RIBHolder.
	defaultCheckFnPtr uintptr
}

// defaultParentRIB returns the owning RIB when r.checkFn is the unmodified default RIB checkFn.
func (r *RIBHolder) defaultParentRIB() *RIB {
	if r.checkFn == nil || r.refCounts == nil || r.refCounts.parent == nil {
		return nil
	}
	if reflect.ValueOf(r.checkFn).Pointer() != r.refCounts.defaultCheckFnPtr {
		return nil
	}
	return r.refCounts.parent
}

// String returns a human-readable string summary of the RIBHolder.
func (r *RIBHolder) String() string {
	if r == nil {
		return "nil RIBHolder"
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.r == nil {
		return "invalid RIB"
	}
	return fmt.Sprintf("RIBHolder(%s){ipv4:%d, ipv6:%d, mpls:%d, nhg:%d, nh:%d}",
		r.name, len(r.r.IPv4Entry), len(r.r.IPv6Entry), len(r.r.LabelEntry), len(r.r.NextHopGroup), len(r.r.NextHop))
}

// SetCheckFunc assigns a custom candidate check function on the RIBHolder.
func (r *RIBHolder) SetCheckFunc(fn func(constants.OpType, *AFTs) (bool, error)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checkFn = fn
}

// SetPostChangeHook assigns a post-change hook function on the RIBHolder.
func (r *RIBHolder) SetPostChangeHook(fn RIBHookFn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.postChangeHook = fn
}

// RIBOpt is an interface implemented by options passed to New.
type RIBOpt interface {
	isRIBOpt()
}

// RIBHolderOpt is an interface implemented by options passed to NewRIBHolder.
type RIBHolderOpt interface {
	isRHOpt()
}

// ForwardRefOpt is an interface implemented by options that can be passed to both New and NewRIBHolder.
type ForwardRefOpt interface {
	RIBOpt
	RIBHolderOpt
}

// DisableRIBCheckFn specifies that consistency checking functions should be disabled for the RIB.
func DisableRIBCheckFn() RIBOpt { return &disableCheckFn{} }

// disableCheckFn is the internal implementation of DisableRIBCheckFn.
type disableCheckFn struct{}

// isRIBOpt implements the RIBOpt interface.
func (*disableCheckFn) isRIBOpt() {}

// hasDisableCheckFn reports whether opt contains the disableCheckFn option.
func hasDisableCheckFn(opt []RIBOpt) bool {
	for _, o := range opt {
		if _, ok := o.(*disableCheckFn); ok {
			return true
		}
	}
	return false
}

// DisableForwardReferences specifies that unresolved forward references should be disallowed in the RIB.
func DisableForwardReferences() ForwardRefOpt { return &disableForwardRef{} }

// disableForwardRef is the internal implementation of DisableForwardReferences.
type disableForwardRef struct{}

// isRIBOpt implements the RIBOpt interface.
func (*disableForwardRef) isRIBOpt() {}

// isRHOpt implements the RIBHolderOpt interface.
func (*disableForwardRef) isRHOpt() {}

// hasDisableForwardRef reports whether opt contains the disableForwardRef option.
func hasDisableForwardRef(opt []RIBOpt) bool {
	for _, o := range opt {
		if _, ok := o.(*disableForwardRef); ok {
			return true
		}
	}
	return false
}

// hasRHDisableForwardRef reports whether opt contains the disableForwardRef option for a RIBHolder.
func hasRHDisableForwardRef(opt []RIBHolderOpt) bool {
	for _, o := range opt {
		if _, ok := o.(*disableForwardRef); ok {
			return true
		}
	}
	return false
}

// customCheckFn is a RIBHolderOpt that configures a candidate check function on a RIBHolder.
type customCheckFn struct {
	// fn is the check function invoked for candidate RIB operations.
	fn RIBHolderCheckFunc
}

// isRHOpt implements the RIBHolderOpt interface.
func (*customCheckFn) isRHOpt() {}

// RIBHolderCheckFn returns an option that configures fn to run on each RIBHolder change.
func RIBHolderCheckFn(fn func(constants.OpType, string, *AFTs) (bool, error)) RIBHolderOpt {
	return &customCheckFn{fn: fn}
}

// hasCheckFn returns the customCheckFn option from opts if present.
func hasCheckFn(opts []RIBHolderOpt) *customCheckFn {
	for _, o := range opts {
		if f, ok := o.(*customCheckFn); ok {
			return f
		}
	}
	return nil
}

// New returns a new RIB initialised with a default network instance named dn.
func New(dn string, opt ...RIBOpt) *RIB {
	r := &RIB{
		niRIB:          map[string]*RIBHolder{},
		defaultName:    dn,
		pendingEntries: map[uint64]*pendingEntry{},
	}

	var rhOpt []RIBHolderOpt
	checkRIB := !hasDisableCheckFn(opt)
	if checkRIB {
		rhOpt = append(rhOpt, RIBHolderCheckFn(r.checkFn))
	}
	r.ribCheck = checkRIB

	if hasDisableForwardRef(opt) {
		rhOpt = append(rhOpt, DisableForwardReferences())
		r.disableForwardReferences = true
	}

	rh := NewRIBHolder(dn, rhOpt...)
	if checkRIB && rh.checkFn != nil {
		rh.refCounts.parent = r
		rh.refCounts.defaultCheckFnPtr = reflect.ValueOf(rh.checkFn).Pointer()
	}
	r.niRIB[dn] = rh
	return r
}

// NewRIBHolder returns a new RIBHolder for a single network instance named name.
func NewRIBHolder(name string, opts ...RIBHolderOpt) *RIBHolder {
	r := &RIBHolder{
		name: name,
		r: &AFTs{
			IPv4Entry:    map[string]*aftpb.Afts_Ipv4EntryKey{},
			IPv6Entry:    map[string]*aftpb.Afts_Ipv6EntryKey{},
			LabelEntry:   map[MPLSLabelKey]*aftpb.Afts_LabelEntryKey{},
			NextHopGroup: map[uint64]*aftpb.Afts_NextHopGroupKey{},
			NextHop:      map[uint64]*aftpb.Afts_NextHopKey{},
		},
		refCounts: &niRefCounter{
			NextHop:      map[uint64]uint64{},
			NextHopGroup: map[uint64]uint64{},
		},
	}

	if fn := hasCheckFn(opts); fn != nil {
		r.checkFn = func(op constants.OpType, a *AFTs) (bool, error) {
			return fn.fn(op, name, a)
		}
	}

	if hasRHDisableForwardRef(opts) {
		r.disableForwardRef = true
	}

	return r
}

// checkFn dispatches candidate add and delete checks to canResolve and canDelete.
func (r *RIB) checkFn(t constants.OpType, ni string, candidate *AFTs) (bool, error) {
	switch t {
	case constants.Add:
		return r.canResolve(ni, candidate)
	case constants.Delete:
		return r.canDelete(ni, candidate)
	}
	return false, fmt.Errorf("invalid unknown operation type, %s", t)
}

// pendingEntry describes an AFT operation awaiting dependency resolution in the RIB.
type pendingEntry struct {
	// ni is the target network instance name.
	ni string
	// op is the pending AFTOperation.
	op *spb.AFTOperation
}

// SetPostChangeHook assigns fn as the post-change hook across all network instances in the RIB.
func (r *RIB) SetPostChangeHook(fn RIBHookFn) {
	r.nrMu.Lock()
	defer r.nrMu.Unlock()
	r.postChangeHook = fn
	for _, nir := range r.niRIB {
		nir.SetPostChangeHook(fn)
	}
}

// SetResolvedEntryHook assigns fn as the resolved-entry hook on the RIB.
func (r *RIB) SetResolvedEntryHook(fn ResolvedEntryFn) {
	r.resolvedEntryHook = fn
}

// NetworkInstanceRIB returns the RIBHolder for network instance s and whether it exists.
func (r *RIB) NetworkInstanceRIB(s string) (*RIBHolder, bool) {
	r.nrMu.RLock()
	defer r.nrMu.RUnlock()
	rh, ok := r.niRIB[s]
	return rh, ok
}

// AddNetworkInstance adds a new network instance with the specified name to the RIB.
func (r *RIB) AddNetworkInstance(name string) error {
	r.nrMu.Lock()
	defer r.nrMu.Unlock()

	if r.niRIB[name] != nil {
		return fmt.Errorf("RIB %s already exists", name)
	}

	var rhOpt []RIBHolderOpt
	if r.ribCheck {
		rhOpt = append(rhOpt, RIBHolderCheckFn(r.checkFn))
	}
	if r.disableForwardReferences {
		rhOpt = append(rhOpt, DisableForwardReferences())
	}

	rh := NewRIBHolder(name, rhOpt...)
	if r.ribCheck && rh.checkFn != nil {
		rh.refCounts.parent = r
		rh.refCounts.defaultCheckFnPtr = reflect.ValueOf(rh.checkFn).Pointer()
	}
	if r.postChangeHook != nil {
		rh.postChangeHook = r.postChangeHook
	}
	r.niRIB[name] = rh
	return nil
}

// KnownNetworkInstances returns the sorted names of all network instances in the RIB.
func (r *RIB) KnownNetworkInstances() []string {
	r.nrMu.RLock()
	defer r.nrMu.RUnlock()
	names := make([]string, 0, len(r.niRIB))
	for n := range r.niRIB {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// RangeNetworkInstances calls fn under a read lock for each network instance in the RIB.
func (r *RIB) RangeNetworkInstances(fn func(name string, rh *RIBHolder) error) error {
	r.nrMu.RLock()
	defer r.nrMu.RUnlock()
	for name, rh := range r.niRIB {
		if err := fn(name, rh); err != nil {
			return err
		}
	}
	return nil
}

// RIBContents returns a map of shallow-copied AFTs keyed by network instance name.
// Because stored entry protobufs are immutable once installed, cloning only copies map headers.
func (r *RIB) RIBContents() (map[string]*AFTs, error) {
	return r.copyRIBs()
}

// copyRIBs returns a per-network-instance map of shallow-cloned AFTs structures.
func (r *RIB) copyRIBs() (map[string]*AFTs, error) {
	r.nrMu.RLock()
	defer r.nrMu.RUnlock()

	out := make(map[string]*AFTs, len(r.niRIB))
	for name, niR := range r.niRIB {
		dup, err := niR.cloneAFTs()
		if err != nil {
			return nil, fmt.Errorf("cannot copy RIB for NI %s, %v", name, err)
		}
		out[name] = dup
	}
	return out, nil
}

// cloneAFTs returns a shallow copy of the AFT maps held by r under a read lock.
func (r *RIBHolder) cloneAFTs() (*AFTs, error) {
	if r == nil {
		return nil, errors.New("cannot copy nil RIBHolder")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.r == nil {
		return nil, errors.New("cannot copy nil RIB structure")
	}
	return &AFTs{
		IPv4Entry:    maps.Clone(r.r.IPv4Entry),
		IPv6Entry:    maps.Clone(r.r.IPv6Entry),
		LabelEntry:   maps.Clone(r.r.LabelEntry),
		NextHopGroup: maps.Clone(r.r.NextHopGroup),
		NextHop:      maps.Clone(r.r.NextHop),
	}, nil
}

// ReadAFTs calls fn under a read lock on the RIBHolder with its underlying AFTs.
func (r *RIBHolder) ReadAFTs(fn func(a *AFTs) error) error {
	if r == nil {
		return errors.New("cannot read nil RIBHolder")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.r == nil {
		return errors.New("invalid RIB structure, nil")
	}
	return fn(r.r)
}

// ReadAfts is an alias for ReadAFTs for compatibility with callers using rib naming.
func (r *RIBHolder) ReadAfts(fn func(a *AFTs) error) error {
	return r.ReadAFTs(fn)
}

// String returns a human-readable string representation of the RIB.
func (r *RIB) String() string {
	r.nrMu.RLock()
	defer r.nrMu.RUnlock()
	buf := &bytes.Buffer{}
	for ni, niR := range r.niRIB {
		buf.WriteString(fmt.Sprintf("%s:\n-----\n%s\n", ni, niR))
	}
	return buf.String()
}

// OpResult contains the result of an AFT operation (Add, Replace, or Delete).
type OpResult struct {
	// ID is the operation ID from the input request.
	ID uint64
	// Op is the AFTOperation that was performed.
	Op *spb.AFTOperation
	// Error describes any fatal error encountered while processing the operation.
	Error string
}

// String returns the OpResult as a human-readable string.
func (o *OpResult) String() string {
	return fmt.Sprintf("ID: %d, Type: %s, Error: %v", o.ID, prototext.Format(o.Op), o.Error)
}

// AddEntry adds or replaces the entry described in op within network instance ni.
func (r *RIB) AddEntry(ni string, op *spb.AFTOperation) ([]*OpResult, []*OpResult, error) {
	if ni == "" {
		return nil, nil, fmt.Errorf("invalid network instance, %s", ni)
	}

	var oks, fails []*OpResult
	if err := r.addEntryInternal(ni, op, &oks, &fails, nil); err != nil {
		return nil, nil, err
	}

	return oks, fails, nil
}

// addEntryInternal implements AddEntry and recursively resolves pending entries.
func (r *RIB) addEntryInternal(ni string, op *spb.AFTOperation, oks, fails *[]*OpResult, installStack map[uint64]bool) error {
	if installStack != nil && installStack[op.GetId()] {
		return nil
	}
	niR, ok := r.NetworkInstanceRIB(ni)
	if !ok || !niR.IsValid() {
		return fmt.Errorf("invalid network instance, %s", ni)
	}

	explicitReplace := op.GetOp() == spb.AFTOperation_REPLACE

	var (
		installed          bool
		opErr              error
		v4Prefix, v6Prefix string
		mplsLabel          uint64
		addedNHG           uint64
		addedNH            uint64
	)

	switch t := op.Entry.(type) {
	case *spb.AFTOperation_Ipv4:
		if log.V(2) {
			log.Infof("[op %d] attempting to add IPv4 prefix %s", op.GetId(), t.Ipv4.GetPrefix())
		}
		orig, done, err := niR.AddIPv4(t.Ipv4, explicitReplace)
		switch {
		case err != nil:
			opErr = err
		case done:
			installed = true
			v4Prefix = t.Ipv4.GetPrefix()
			handleReferences(r, niR, orig.GetIpv4Entry(), t.Ipv4.GetIpv4Entry())
		}
	case *spb.AFTOperation_Ipv6:
		v6Prefix = t.Ipv6.GetPrefix()
		if log.V(2) {
			log.Infof("[op %d] attempting to add IPv6 prefix %s", op.GetId(), t.Ipv6.GetPrefix())
		}
		orig, done, err := niR.AddIPv6(t.Ipv6, explicitReplace)
		switch {
		case err != nil:
			opErr = err
		case done:
			installed = true
			handleReferences(r, niR, orig.GetIpv6Entry(), t.Ipv6.GetIpv6Entry())
		}
	case *spb.AFTOperation_Mpls:
		mplsLabel = t.Mpls.GetLabelUint64()
		if log.V(2) {
			log.Infof("[op %d] attempting to add MPLS label entry %d", op.GetId(), mplsLabel)
		}
		orig, done, err := niR.AddMPLS(t.Mpls, explicitReplace)
		switch {
		case err != nil:
			opErr = err
		case done:
			installed = true
			handleReferences(r, niR, orig.GetLabelEntry(), t.Mpls.GetLabelEntry())
		}
	case *spb.AFTOperation_NextHopGroup:
		if log.V(2) {
			log.Infof("[op %d] attempting to add NHG ID %d", op.GetId(), t.NextHopGroup.GetId())
		}
		orig, done, err := niR.AddNextHopGroup(t.NextHopGroup, explicitReplace)
		switch {
		case err != nil:
			opErr = err
		case done:
			r.handleNHGReferences(niR, orig.GetNextHopGroup(), t.NextHopGroup.GetNextHopGroup())
			installed = true
			addedNHG = t.NextHopGroup.GetId()
		}
	case *spb.AFTOperation_NextHop:
		if log.V(2) {
			log.Infof("[op %d] attempting to add NH Index %d", op.GetId(), t.NextHop.GetIndex())
		}
		_, done, err := niR.AddNextHop(t.NextHop, explicitReplace)
		switch {
		case err != nil:
			opErr = err
		case done:
			installed = true
			addedNH = t.NextHop.GetIndex()
		}
	default:
		return status.Newf(codes.Unimplemented, "unsupported AFT operation type %T", t).Err()
	}

	switch {
	case opErr != nil:
		*fails = append(*fails, &OpResult{
			ID:    op.GetId(),
			Op:    op,
			Error: opErr.Error(),
		})
	case installed:
		if installStack != nil {
			installStack[op.GetId()] = true
		}
		if log.V(2) {
			log.Infof("operation %d installed in RIB successfully", op.GetId())
		}

		r.rmPending(op.GetId())

		*oks = append(*oks, &OpResult{
			ID: op.GetId(),
			Op: op,
		})

		var (
			call bool
			aft  constants.AFT
			key  any
		)
		switch {
		case v4Prefix != "":
			call = true
			aft = constants.IPv4
			key = v4Prefix
		case mplsLabel != 0:
			call = true
			aft = constants.MPLS
			key = mplsLabel
		case v6Prefix != "":
			call = true
			aft = constants.IPv6
			key = v6Prefix
		}
		if call {
			if err := r.callResolvedEntryHook(constants.Add, ni, aft, key); err != nil {
				return fmt.Errorf("cannot run resolvedEntryHook, %v", err)
			}
		}

		var candidates []*pendingEntry
		switch {
		case addedNH != 0:
			candidates = r.getPendingForNH(ni, addedNH)
		case addedNHG != 0:
			candidates = r.getPendingForNHG(ni, addedNHG)
		}
		if len(candidates) > 0 {
			if installStack == nil {
				installStack = map[uint64]bool{op.GetId(): true}
			}
			for _, e := range candidates {
				if err := r.addEntryInternal(e.ni, e.op, oks, fails, installStack); err != nil {
					return err
				}
			}
		}
	default:
		if !r.disableForwardReferences {
			r.addPending(op.GetId(), &pendingEntry{
				ni: ni,
				op: op,
			})
		} else {
			*fails = append(*fails, &OpResult{
				ID:    op.GetId(),
				Op:    op,
				Error: fmt.Sprintf("operation %d has unresolved dependencies", op.GetId()),
			})
		}
	}

	return nil
}

// topLevelEntryProto is an interface implemented by IPv4, IPv6, and MPLS entry protobuf messages.
type topLevelEntryProto interface {
	*aftpb.Afts_Ipv4Entry | *aftpb.Afts_Ipv6Entry | *aftpb.Afts_LabelEntry
	GetNextHopGroupNetworkInstance() *wpb.StringValue
	GetNextHopGroup() *wpb.UintValue
}

// handleReferences updates NextHopGroup reference counts when a top-level entry is added or replaced.
func handleReferences[P topLevelEntryProto](r *RIB, niRIB *RIBHolder, original, updated P) {
	incRefCounts := true
	newNHGNI := updated.GetNextHopGroupNetworkInstance().GetValue()
	newNHG := updated.GetNextHopGroup().GetValue()
	if original != nil {
		origNHGNI := original.GetNextHopGroupNetworkInstance().GetValue()
		origNHG := original.GetNextHopGroup().GetValue()

		switch {
		case newNHGNI == origNHGNI && newNHG == origNHG:
			incRefCounts = false
		case newNHGNI != origNHGNI || newNHG != origNHG:
			rr, err := r.refdRIB(niRIB, origNHGNI)
			switch err {
			case nil:
				rr.decNHGRefCount(origNHG)
			default:
				log.Errorf("cannot find NHG network instance %s", origNHGNI)
			}
		}
	}

	if incRefCounts {
		referencingRIB, err := r.refdRIB(niRIB, newNHGNI)
		switch err {
		case nil:
			referencingRIB.incNHGRefCount(newNHG)
		default:
			log.Errorf("cannot find network instance %s", newNHGNI)
		}
	}
}

// handleNHGReferences updates NextHop reference counts when a NextHopGroup is added or replaced.
func (r *RIB) handleNHGReferences(niRIB *RIBHolder, original, updated *aftpb.Afts_NextHopGroup) {
	for _, nh := range updated.GetNextHop() {
		niRIB.incNHRefCount(nh.GetIndex())
	}
	if original != nil {
		for _, nh := range original.GetNextHop() {
			niRIB.decNHRefCount(nh.GetIndex())
		}
	}
}

// callResolvedEntryHook invokes resolvedEntryHook asynchronously with a snapshot of the RIB.
func (r *RIB) callResolvedEntryHook(optype constants.OpType, netinst string, aft constants.AFT, key any) error {
	if r.resolvedEntryHook == nil {
		return nil
	}

	ribs, err := r.copyRIBs()
	if err != nil {
		return err
	}
	go r.resolvedEntryHook(ribs, optype, netinst, aft, key)
	return nil
}

// refdRIB returns the RIBHolder for ref, or ni when ref is empty.
func (r *RIB) refdRIB(ni *RIBHolder, ref string) (*RIBHolder, error) {
	referencingRIB := ni
	if ref != "" {
		rr, ok := r.NetworkInstanceRIB(ref)
		if !ok {
			return nil, status.Newf(codes.InvalidArgument, "invalid network-instance %s specified in entry", ref).Err()
		}
		referencingRIB = rr
	}
	return referencingRIB, nil
}

// DeleteEntry removes the entry specified by op from network instance ni.
func (r *RIB) DeleteEntry(ni string, op *spb.AFTOperation) ([]*OpResult, []*OpResult, error) {
	niR, ok := r.NetworkInstanceRIB(ni)
	if !ok || !niR.IsValid() {
		return nil, nil, fmt.Errorf("invalid network instance, %s", ni)
	}

	var (
		oks, fails   []*OpResult
		removed      bool
		err          error
		originalv4   *aftpb.Afts_Ipv4EntryKey
		originalv6   *aftpb.Afts_Ipv6EntryKey
		originalNHG  *aftpb.Afts_NextHopGroupKey
		originalMPLS *aftpb.Afts_LabelEntryKey
	)

	if op == nil || op.Entry == nil {
		return nil, nil, status.Newf(codes.InvalidArgument, "invalid nil AFT operation, %v", op).Err()
	}
	switch t := op.Entry.(type) {
	case *spb.AFTOperation_Ipv4:
		if log.V(2) {
			log.Infof("deleting IPv4 prefix %s", t.Ipv4.GetPrefix())
		}
		originalv4, removed, err = niR.DeleteIPv4(t.Ipv4)
	case *spb.AFTOperation_Ipv6:
		if log.V(2) {
			log.Infof("deleting IPv6 prefix %s", t.Ipv6.GetPrefix())
		}
		originalv6, removed, err = niR.DeleteIPv6(t.Ipv6)
	case *spb.AFTOperation_NextHop:
		if log.V(2) {
			log.Infof("deleting NH Index %d", t.NextHop.GetIndex())
		}
		_, removed, err = niR.DeleteNextHop(t.NextHop)
	case *spb.AFTOperation_NextHopGroup:
		if log.V(2) {
			log.Infof("deleting NHG ID %d", t.NextHopGroup.GetId())
		}
		originalNHG, removed, err = niR.DeleteNextHopGroup(t.NextHopGroup)
	case *spb.AFTOperation_Mpls:
		if log.V(2) {
			log.Infof("deleting MPLS entry %v", t.Mpls.GetLabel())
		}
		originalMPLS, removed, err = niR.DeleteMPLS(t.Mpls)
	default:
		return nil, nil, status.Newf(codes.Unimplemented, "unsupported AFT operation type %T", t).Err()
	}

	var (
		callHook bool
		aft      constants.AFT
		key      any
	)

	switch {
	case err != nil:
		fails = append(fails, &OpResult{
			ID:    op.GetId(),
			Op:    op,
			Error: err.Error(),
		})
	case removed:
		switch {
		case originalv4 != nil:
			v4e := originalv4.GetIpv4Entry()
			referencingRIB, err := r.refdRIB(niR, v4e.GetNextHopGroupNetworkInstance().GetValue())
			if err != nil {
				return nil, nil, err
			}
			referencingRIB.decNHGRefCount(v4e.GetNextHopGroup().GetValue())
			callHook = true
			aft = constants.IPv4
			key = originalv4.GetPrefix()
		case originalv6 != nil:
			v6e := originalv6.GetIpv6Entry()
			referencingRIB, err := r.refdRIB(niR, v6e.GetNextHopGroupNetworkInstance().GetValue())
			if err != nil {
				return nil, nil, err
			}
			referencingRIB.decNHGRefCount(v6e.GetNextHopGroup().GetValue())
			callHook = true
			aft = constants.IPv6
			key = originalv6.GetPrefix()
		case originalNHG != nil:
			for _, nh := range originalNHG.GetNextHopGroup().GetNextHop() {
				niR.decNHRefCount(nh.GetIndex())
			}
		case originalMPLS != nil:
			le := originalMPLS.GetLabelEntry()
			referencingRIB, err := r.refdRIB(niR, le.GetNextHopGroupNetworkInstance().GetValue())
			if err != nil {
				return nil, nil, err
			}
			referencingRIB.decNHGRefCount(le.GetNextHopGroup().GetValue())
			callHook = true
			aft = constants.MPLS
			if originalMPLS.GetLabelUint64() != 0 {
				key = originalMPLS.GetLabelUint64()
			} else {
				key = originalMPLS.GetLabelOpenconfigmplstypesmplslabelenum()
			}
		}

		if log.V(2) {
			log.Infof("operation %d deleted from RIB successfully", op.GetId())
		}
		oks = append(oks, &OpResult{
			ID: op.GetId(),
			Op: op,
		})
	default:
		fails = append(fails, &OpResult{
			ID: op.GetId(),
			Op: op,
		})
	}

	if callHook {
		if err := r.callResolvedEntryHook(constants.Delete, ni, aft, key); err != nil {
			return oks, fails, fmt.Errorf("cannot run resolvedEntryHook, %v", err)
		}
	}
	return oks, fails, nil
}

// indexPendingNHGDepLocked records a pending route entry's NextHopGroup dependency.
// The caller must hold r.pendMu.
func indexPendingNHGDepLocked[P topLevelEntryProto](r *RIB, id uint64, defaultNI string, entry P) {
	if entry == nil {
		return
	}
	targetNI := entry.GetNextHopGroupNetworkInstance().GetValue()
	if targetNI == "" {
		targetNI = defaultNI
	}
	nhgID := entry.GetNextHopGroup().GetValue()
	if r.pendingByNHG == nil {
		r.pendingByNHG = make(map[niObjKey]map[uint64]struct{})
	}
	k := niObjKey{ni: targetNI, id: nhgID}
	s := r.pendingByNHG[k]
	if s == nil {
		s = make(map[uint64]struct{})
		r.pendingByNHG[k] = s
	}
	s[id] = struct{}{}
}

// unindexPendingNHGDepLocked removes a pending route entry's NextHopGroup dependency.
// The caller must hold r.pendMu.
func unindexPendingNHGDepLocked[P topLevelEntryProto](r *RIB, id uint64, defaultNI string, entry P) {
	if entry == nil {
		return
	}
	targetNI := entry.GetNextHopGroupNetworkInstance().GetValue()
	if targetNI == "" {
		targetNI = defaultNI
	}
	k := niObjKey{ni: targetNI, id: entry.GetNextHopGroup().GetValue()}
	if s := r.pendingByNHG[k]; s != nil {
		delete(s, id)
		if len(s) == 0 {
			delete(r.pendingByNHG, k)
		}
	}
}

// indexPendingEntryLocked records the dependencies of pending entry e under id.
// The caller must hold r.pendMu.
func (r *RIB) indexPendingEntryLocked(id uint64, e *pendingEntry) {
	if e == nil || e.op == nil {
		return
	}
	if r.indexedPending == nil {
		r.indexedPending = make(map[uint64]struct{})
	}
	r.indexedPending[id] = struct{}{}
	ni := e.ni
	if ni == "" {
		ni = r.defaultName
	}
	switch t := e.op.Entry.(type) {
	case *spb.AFTOperation_NextHopGroup:
		if r.pendingByNH == nil {
			r.pendingByNH = make(map[niObjKey]map[uint64]struct{})
		}
		for _, nh := range t.NextHopGroup.GetNextHopGroup().GetNextHop() {
			k := niObjKey{ni: ni, id: nh.GetIndex()}
			s := r.pendingByNH[k]
			if s == nil {
				s = make(map[uint64]struct{})
				r.pendingByNH[k] = s
			}
			s[id] = struct{}{}
		}
	case *spb.AFTOperation_Ipv4:
		indexPendingNHGDepLocked(r, id, ni, t.Ipv4.GetIpv4Entry())
	case *spb.AFTOperation_Ipv6:
		indexPendingNHGDepLocked(r, id, ni, t.Ipv6.GetIpv6Entry())
	case *spb.AFTOperation_Mpls:
		indexPendingNHGDepLocked(r, id, ni, t.Mpls.GetLabelEntry())
	}
}

// unindexPendingEntryLocked removes id from the pending dependency index maps.
// The caller must hold r.pendMu.
func (r *RIB) unindexPendingEntryLocked(id uint64, e *pendingEntry) {
	delete(r.indexedPending, id)
	if e == nil || e.op == nil {
		return
	}
	ni := e.ni
	if ni == "" {
		ni = r.defaultName
	}
	switch t := e.op.Entry.(type) {
	case *spb.AFTOperation_NextHopGroup:
		for _, nh := range t.NextHopGroup.GetNextHopGroup().GetNextHop() {
			k := niObjKey{ni: ni, id: nh.GetIndex()}
			if s := r.pendingByNH[k]; s != nil {
				delete(s, id)
				if len(s) == 0 {
					delete(r.pendingByNH, k)
				}
			}
		}
	case *spb.AFTOperation_Ipv4:
		unindexPendingNHGDepLocked(r, id, ni, t.Ipv4.GetIpv4Entry())
	case *spb.AFTOperation_Ipv6:
		unindexPendingNHGDepLocked(r, id, ni, t.Ipv6.GetIpv6Entry())
	case *spb.AFTOperation_Mpls:
		unindexPendingNHGDepLocked(r, id, ni, t.Mpls.GetLabelEntry())
	}
}

// syncPendingIndexLocked indexes any entries in r.pendingEntries inserted directly by unit tests.
// The caller must hold r.pendMu.
func (r *RIB) syncPendingIndexLocked() {
	if len(r.indexedPending) == len(r.pendingEntries) {
		return
	}
	for id, e := range r.pendingEntries {
		if _, ok := r.indexedPending[id]; !ok {
			r.indexPendingEntryLocked(id, e)
		}
	}
}

// getPendingForNH returns pending entries waiting on NextHop (ni, nhIndex), sorted by operation ID.
func (r *RIB) getPendingForNH(ni string, nhIndex uint64) []*pendingEntry {
	r.pendMu.RLock()
	empty := len(r.pendingEntries) == 0
	r.pendMu.RUnlock()
	if empty {
		return nil
	}
	r.pendMu.Lock()
	defer r.pendMu.Unlock()
	if len(r.pendingEntries) == 0 {
		return nil
	}
	r.syncPendingIndexLocked()
	if ni == "" {
		ni = r.defaultName
	}
	ids := r.pendingByNH[niObjKey{ni: ni, id: nhIndex}]
	if len(ids) == 0 {
		return nil
	}
	p := make([]*pendingEntry, 0, len(ids))
	for id := range ids {
		if e := r.pendingEntries[id]; e != nil {
			p = append(p, e)
		}
	}
	if len(p) > 1 {
		sort.Slice(p, func(i, j int) bool {
			return p[i].op.GetId() < p[j].op.GetId()
		})
	}
	return p
}

// getPendingForNHG returns pending entries waiting on NextHopGroup (ni, nhgID), sorted by operation ID.
func (r *RIB) getPendingForNHG(ni string, nhgID uint64) []*pendingEntry {
	r.pendMu.RLock()
	empty := len(r.pendingEntries) == 0
	r.pendMu.RUnlock()
	if empty {
		return nil
	}
	r.pendMu.Lock()
	defer r.pendMu.Unlock()
	if len(r.pendingEntries) == 0 {
		return nil
	}
	r.syncPendingIndexLocked()
	if ni == "" {
		ni = r.defaultName
	}
	ids := r.pendingByNHG[niObjKey{ni: ni, id: nhgID}]
	if len(ids) == 0 {
		return nil
	}
	p := make([]*pendingEntry, 0, len(ids))
	for id := range ids {
		if e := r.pendingEntries[id]; e != nil {
			p = append(p, e)
		}
	}
	if len(p) > 1 {
		sort.Slice(p, func(i, j int) bool {
			return p[i].op.GetId() < p[j].op.GetId()
		})
	}
	return p
}

// addPending records e under operation ID id in pendingEntries and updates the dependency index.
func (r *RIB) addPending(id uint64, e *pendingEntry) {
	r.pendMu.Lock()
	defer r.pendMu.Unlock()
	if old, exists := r.pendingEntries[id]; exists {
		r.unindexPendingEntryLocked(id, old)
	}
	r.pendingEntries[id] = e
	r.indexPendingEntryLocked(id, e)
}

// rmPending removes the operation with ID id from pendingEntries and the dependency index.
func (r *RIB) rmPending(id uint64) {
	r.pendMu.RLock()
	_, exists := r.pendingEntries[id]
	r.pendMu.RUnlock()
	if !exists {
		return
	}
	r.pendMu.Lock()
	defer r.pendMu.Unlock()
	if e, ok := r.pendingEntries[id]; ok {
		r.unindexPendingEntryLocked(id, e)
		delete(r.pendingEntries, id)
	}
}

// canResolve determines whether a single-entry candidate AFTs can be resolved against r.
func (r *RIB) canResolve(netInst string, candidate *AFTs) (bool, error) {
	if candidate == nil {
		return false, errors.New("invalid nil candidate AFT")
	}
	if err := checkCandidate(candidate); err != nil {
		return false, err
	}

	for _, n := range candidate.NextHop {
		if n.GetIndex() == 0 {
			return false, fmt.Errorf("invalid index zero for next-hop in NI %s", netInst)
		}
		return true, nil
	}

	if netInst == "" {
		netInst = r.defaultName
	}
	niRIB, ok := r.NetworkInstanceRIB(netInst)
	if !ok {
		return false, fmt.Errorf("invalid network-instance %s", netInst)
	}

	for _, g := range candidate.NextHopGroup {
		if g.GetId() == 0 {
			return false, fmt.Errorf("invalid zero-index NHG")
		}
		nhs := g.GetNextHopGroup().GetNextHop()
		if len(nhs) == 0 {
			return false, fmt.Errorf("empty next-hop-group")
		}
		for _, n := range nhs {
			if n.GetIndex() == 0 {
				return false, fmt.Errorf("invalid zero index NH in NHG %d, NI %s", g.GetId(), netInst)
			}
			if _, ok := niRIB.GetNextHop(n.GetIndex()); !ok {
				return false, nil
			}
		}
		return true, nil
	}

	nhgResolvable := func(resolveRIB *RIBHolder, otherNI string, nhg uint64) (bool, error) {
		if otherNI != "" {
			resolveRIB, ok = r.NetworkInstanceRIB(otherNI)
			if !ok {
				return false, fmt.Errorf("invalid unknown network-instance for entry, %s", otherNI)
			}
		}
		if _, ok := resolveRIB.GetNextHopGroup(nhg); !ok {
			return false, nil
		}
		return true, nil
	}

	for _, i := range candidate.IPv4Entry {
		v4 := i.GetIpv4Entry()
		if v4.GetNextHopGroup().GetValue() == 0 {
			return false, fmt.Errorf("invalid zero-index NHG in IPv4Entry %s, NI %s", i.GetPrefix(), netInst)
		}
		return nhgResolvable(niRIB, v4.GetNextHopGroupNetworkInstance().GetValue(), v4.GetNextHopGroup().GetValue())
	}

	for _, i := range candidate.IPv6Entry {
		v6 := i.GetIpv6Entry()
		if v6.GetNextHopGroup().GetValue() == 0 {
			return false, fmt.Errorf("invalid zero-index NHG in IPv6Entry %s, NI %s", i.GetPrefix(), netInst)
		}
		return nhgResolvable(niRIB, v6.GetNextHopGroupNetworkInstance().GetValue(), v6.GetNextHopGroup().GetValue())
	}

	for _, i := range candidate.LabelEntry {
		le := i.GetLabelEntry()
		if le.GetNextHopGroup().GetValue() == 0 {
			return false, fmt.Errorf("invalid zero index NHG in LabelEntry %v, NI %s", i.GetLabel(), netInst)
		}
		return nhgResolvable(niRIB, le.GetNextHopGroupNetworkInstance().GetValue(), le.GetNextHopGroup().GetValue())
	}

	return false, errors.New("no entries in specified candidate")
}

// canDelete determines whether a single-entry deletionCandidate AFTs can be safely removed from r.
func (r *RIB) canDelete(netInst string, deletionCandidate *AFTs) (bool, error) {
	if deletionCandidate == nil {
		return false, errors.New("invalid nil candidate AFT")
	}
	if err := checkCandidate(deletionCandidate); err != nil {
		return false, err
	}

	if netInst == "" {
		netInst = r.defaultName
	}
	niRIB, ok := r.NetworkInstanceRIB(netInst)
	if !ok {
		return false, fmt.Errorf("invalid network-instance %s", netInst)
	}

	if len(deletionCandidate.IPv4Entry) != 0 || len(deletionCandidate.LabelEntry) != 0 || len(deletionCandidate.IPv6Entry) != 0 {
		return true, nil
	}

	for id := range deletionCandidate.NextHopGroup {
		switch {
		case id == 0:
			return false, fmt.Errorf("bad NextHopGroup ID 0")
		case !niRIB.nhgExists(id):
			return true, nil
		}
		return !niRIB.nhgReferenced(id), nil
	}

	for idx := range deletionCandidate.NextHop {
		switch {
		case idx == 0:
			return false, fmt.Errorf("bad NextHop ID 0")
		case !niRIB.nhExists(idx):
			return true, nil
		}
		return !niRIB.nhReferenced(idx), nil
	}

	return false, errors.New("no entries in specified candidate")
}

// checkCandidate validates that caft contains exactly one supported AFT entry.
func checkCandidate(caft *AFTs) error {
	total := len(caft.IPv6Entry) + len(caft.LabelEntry) + len(caft.IPv4Entry) + len(caft.NextHopGroup) + len(caft.NextHop)
	switch {
	case total == 0:
		return errors.New("no entries in specified candidate")
	case total > 1:
		return fmt.Errorf("multiple entries are unsupported, got mpls: %v, ipv4: %v, next-hop-group: %v, next-hop: %v", caft.LabelEntry, caft.IPv4Entry, caft.NextHopGroup, caft.NextHop)
	}
	return nil
}

// IsValid reports whether the RIBHolder is initialised and valid to be programmed.
func (r *RIBHolder) IsValid() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.name != "" && r.r != nil
}

// GetIPv4 retrieves the IPv4 entry for prefix from the RIBHolder and reports whether it was found.
func (r *RIBHolder) GetIPv4(prefix string) (*aftpb.Afts_Ipv4EntryKey, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.r == nil {
		return nil, false
	}
	e, ok := r.r.IPv4Entry[prefix]
	if !ok || e == nil {
		return nil, false
	}
	return e, true
}

// GetIPv6 retrieves the IPv6 entry for prefix from the RIBHolder and reports whether it was found.
func (r *RIBHolder) GetIPv6(prefix string) (*aftpb.Afts_Ipv6EntryKey, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.r == nil {
		return nil, false
	}
	e, ok := r.r.IPv6Entry[prefix]
	if !ok || e == nil {
		return nil, false
	}
	return e, true
}

// GetMPLS retrieves the MPLS label entry for key from the RIBHolder and reports whether it was found.
func (r *RIBHolder) GetMPLS(key MPLSLabelKey) (*aftpb.Afts_LabelEntryKey, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.r == nil {
		return nil, false
	}
	e, ok := r.r.LabelEntry[key]
	if !ok || e == nil {
		return nil, false
	}
	return e, true
}

// GetNextHopGroup retrieves the NextHopGroup with the specified ID from the RIBHolder and reports whether it was found.
func (r *RIBHolder) GetNextHopGroup(id uint64) (*aftpb.Afts_NextHopGroupKey, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.r == nil {
		return nil, false
	}
	n, ok := r.r.NextHopGroup[id]
	if !ok || n == nil {
		return nil, false
	}
	return n, true
}

// GetNextHop retrieves the NextHop with the specified index from the RIBHolder and reports whether it was found.
func (r *RIBHolder) GetNextHop(index uint64) (*aftpb.Afts_NextHopKey, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.r == nil {
		return nil, false
	}
	n, ok := r.r.NextHop[index]
	if !ok || n == nil {
		return nil, false
	}
	return n, true
}

// IPv4Count returns the number of installed IPv4 entries in the RIBHolder.
func (r *RIBHolder) IPv4Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.r == nil {
		return 0
	}
	return len(r.r.IPv4Entry)
}

// IPv6Count returns the number of installed IPv6 entries in the RIBHolder.
func (r *RIBHolder) IPv6Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.r == nil {
		return 0
	}
	return len(r.r.IPv6Entry)
}

// MPLSCount returns the number of installed MPLS label entries in the RIBHolder.
func (r *RIBHolder) MPLSCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.r == nil {
		return 0
	}
	return len(r.r.LabelEntry)
}

// NextHopGroupCount returns the number of installed NextHopGroup entries in the RIBHolder.
func (r *RIBHolder) NextHopGroupCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.r == nil {
		return 0
	}
	return len(r.r.NextHopGroup)
}

// NextHopCount returns the number of installed NextHop entries in the RIBHolder.
func (r *RIBHolder) NextHopCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.r == nil {
		return 0
	}
	return len(r.r.NextHop)
}

// AddIPv4 validates, clones, and installs the IPv4 entry e into the RIBHolder.
// It returns any replaced entry, a boolean indicating whether the entry was installed, and any fatal error.
func (r *RIBHolder) AddIPv4(e *aftpb.Afts_Ipv4EntryKey, explicitReplace bool) (*aftpb.Afts_Ipv4EntryKey, bool, error) {
	if r.r == nil {
		return nil, false, errors.New("invalid RIB structure, nil")
	}
	if e == nil {
		return nil, false, errors.New("nil IPv4 Entry provided")
	}

	entry, err := validateAndCloneIPv4(e)
	if err != nil {
		return nil, false, fmt.Errorf("invalid IPv4Entry, %v", err)
	}
	pfx := entry.GetPrefix()
	v4 := entry.GetIpv4Entry()

	if parent := r.defaultParentRIB(); r.checkFn == nil || parent != nil {
		var extRIB *RIBHolder
		if parent != nil {
			if v4.GetNextHopGroup().GetValue() == 0 {
				if explicitReplace && !r.ipv4Exists(pfx) {
					return nil, false, fmt.Errorf("cannot replace IPv4 Entry %s, does not exist", pfx)
				}
				return nil, false, fmt.Errorf("invalid zero-index NHG in IPv4Entry %s, NI %s", pfx, r.name)
			}
			if otherNI := v4.GetNextHopGroupNetworkInstance().GetValue(); otherNI != "" && otherNI != r.name {
				var ok bool
				extRIB, ok = parent.NetworkInstanceRIB(otherNI)
				if !ok {
					if explicitReplace && !r.ipv4Exists(pfx) {
						return nil, false, fmt.Errorf("cannot replace IPv4 Entry %s, does not exist", pfx)
					}
					return nil, false, fmt.Errorf("invalid unknown network-instance for entry, %s", otherNI)
				}
			}
		}

		r.mu.Lock()
		orig, exists := r.r.IPv4Entry[pfx]
		if explicitReplace && !exists {
			r.mu.Unlock()
			return nil, false, fmt.Errorf("cannot replace IPv4 Entry %s, does not exist", pfx)
		}
		if parent != nil {
			nhg := v4.GetNextHopGroup().GetValue()
			if extRIB != nil {
				if !extRIB.nhgExists(nhg) {
					r.mu.Unlock()
					return nil, false, nil
				}
			} else if _, ok := r.r.NextHopGroup[nhg]; !ok {
				r.mu.Unlock()
				return nil, false, nil
			}
		}
		if r.r.IPv4Entry == nil {
			r.r.IPv4Entry = make(map[string]*aftpb.Afts_Ipv4EntryKey, 1)
		}
		r.r.IPv4Entry[pfx] = entry
		hook := r.postChangeHook
		r.mu.Unlock()

		if hook != nil {
			hook(constants.Add, unixTS(), r.name, entry)
		}
		return orig, true, nil
	}

	nr := &AFTs{
		IPv4Entry: map[string]*aftpb.Afts_Ipv4EntryKey{pfx: entry},
	}

	if explicitReplace && !r.ipv4Exists(pfx) {
		return nil, false, fmt.Errorf("cannot replace IPv4 Entry %s, does not exist", pfx)
	}

	var orig *aftpb.Afts_Ipv4EntryKey
	if explicitReplace || r.ipv4Exists(pfx) {
		orig = r.retrieveIPv4(pfx)
	}

	if r.checkFn != nil {
		ok, err := r.checkFn(constants.Add, nr)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
	}

	r.doAddIPv4(pfx, entry)

	if r.postChangeHook != nil {
		r.postChangeHook(constants.Add, unixTS(), r.name, entry)
	}

	return orig, true, nil
}

// ipv4Exists reports whether prefix exists in the RIBHolder.
func (r *RIBHolder) ipv4Exists(prefix string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.r.IPv4Entry[prefix]
	return ok
}

// retrieveIPv4 returns the IPv4 entry for prefix under a read lock.
func (r *RIBHolder) retrieveIPv4(prefix string) *aftpb.Afts_Ipv4EntryKey {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.r.IPv4Entry[prefix]
}

// doAddIPv4 writes entry into the IPv4Entry map under a write lock.
func (r *RIBHolder) doAddIPv4(pfx string, entry *aftpb.Afts_Ipv4EntryKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.r.IPv4Entry == nil {
		r.r.IPv4Entry = make(map[string]*aftpb.Afts_Ipv4EntryKey, 1)
	}
	r.r.IPv4Entry[pfx] = entry
}

// DeleteIPv4 removes the IPv4 entry e from the RIBHolder.
// It returns the removed entry (if present), whether the delete succeeded, and any error.
func (r *RIBHolder) DeleteIPv4(e *aftpb.Afts_Ipv4EntryKey) (*aftpb.Afts_Ipv4EntryKey, bool, error) {
	if e == nil {
		return nil, false, errors.New("nil entry provided")
	}
	if r.r == nil {
		return nil, false, errors.New("invalid RIB structure, nil")
	}

	pfx := e.GetPrefix()
	if r.checkFn == nil || r.defaultParentRIB() != nil {
		r.mu.Lock()
		de := r.r.IPv4Entry[pfx]
		delete(r.r.IPv4Entry, pfx)
		hook := r.postChangeHook
		r.mu.Unlock()

		if hook != nil {
			hookEntry := de
			if hookEntry == nil {
				hookEntry = e
			}
			hook(constants.Delete, unixTS(), r.name, hookEntry)
		}
		return de, true, nil
	}

	de := r.retrieveIPv4(pfx)
	rr := &AFTs{
		IPv4Entry: map[string]*aftpb.Afts_Ipv4EntryKey{
			pfx: {Prefix: pfx, Ipv4Entry: &aftpb.Afts_Ipv4Entry{}},
		},
	}
	if r.checkFn != nil {
		ok, err := r.checkFn(constants.Delete, rr)
		switch {
		case err != nil:
			return nil, false, err
		case !ok:
			return nil, false, nil
		}
	}

	r.doDeleteIPv4(pfx)

	if r.postChangeHook != nil {
		hookEntry := de
		if hookEntry == nil {
			hookEntry = e
		}
		r.postChangeHook(constants.Delete, unixTS(), r.name, hookEntry)
	}

	return de, true, nil
}

// doDeleteIPv4 deletes pfx from the IPv4Entry map under a write lock.
func (r *RIBHolder) doDeleteIPv4(pfx string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.r.IPv4Entry, pfx)
}

// locklessDeleteIPv4 removes prefix from IPv4Entry when the caller already holds r.mu.
func (r *RIBHolder) locklessDeleteIPv4(prefix string) error {
	de := r.r.IPv4Entry[prefix]
	if de == nil {
		return fmt.Errorf("cannot find prefix %s", prefix)
	}
	delete(r.r.IPv4Entry, prefix)
	if r.postChangeHook != nil {
		r.postChangeHook(constants.Delete, unixTS(), r.name, de)
	}
	return nil
}

// AddIPv6 validates, clones, and installs the IPv6 entry e into the RIBHolder.
// It returns any replaced entry, a boolean indicating whether the entry was installed, and any fatal error.
func (r *RIBHolder) AddIPv6(e *aftpb.Afts_Ipv6EntryKey, explicitReplace bool) (*aftpb.Afts_Ipv6EntryKey, bool, error) {
	if r.r == nil {
		return nil, false, errors.New("invalid RIB structure, nil")
	}
	if e == nil {
		return nil, false, errors.New("nil IPv6 Entry provided")
	}

	entry, err := validateAndCloneIPv6(e)
	if err != nil {
		return nil, false, fmt.Errorf("invalid IPv6Entry, %v", err)
	}
	pfx := entry.GetPrefix()
	v6 := entry.GetIpv6Entry()

	if parent := r.defaultParentRIB(); r.checkFn == nil || parent != nil {
		var extRIB *RIBHolder
		if parent != nil {
			if v6.GetNextHopGroup().GetValue() == 0 {
				if explicitReplace && !r.ipv6Exists(pfx) {
					return nil, false, fmt.Errorf("cannot replace IPv6 Entry %s, does not exist", pfx)
				}
				return nil, false, fmt.Errorf("invalid zero-index NHG in IPv6Entry %s, NI %s", pfx, r.name)
			}
			if otherNI := v6.GetNextHopGroupNetworkInstance().GetValue(); otherNI != "" && otherNI != r.name {
				var ok bool
				extRIB, ok = parent.NetworkInstanceRIB(otherNI)
				if !ok {
					if explicitReplace && !r.ipv6Exists(pfx) {
						return nil, false, fmt.Errorf("cannot replace IPv6 Entry %s, does not exist", pfx)
					}
					return nil, false, fmt.Errorf("invalid unknown network-instance for entry, %s", otherNI)
				}
			}
		}

		r.mu.Lock()
		orig, exists := r.r.IPv6Entry[pfx]
		if explicitReplace && !exists {
			r.mu.Unlock()
			return nil, false, fmt.Errorf("cannot replace IPv6 Entry %s, does not exist", pfx)
		}
		if parent != nil {
			nhg := v6.GetNextHopGroup().GetValue()
			if extRIB != nil {
				if !extRIB.nhgExists(nhg) {
					r.mu.Unlock()
					return nil, false, nil
				}
			} else if _, ok := r.r.NextHopGroup[nhg]; !ok {
				r.mu.Unlock()
				return nil, false, nil
			}
		}
		if r.r.IPv6Entry == nil {
			r.r.IPv6Entry = make(map[string]*aftpb.Afts_Ipv6EntryKey, 1)
		}
		r.r.IPv6Entry[pfx] = entry
		hook := r.postChangeHook
		r.mu.Unlock()

		if hook != nil {
			hook(constants.Add, unixTS(), r.name, entry)
		}
		return orig, true, nil
	}

	nr := &AFTs{
		IPv6Entry: map[string]*aftpb.Afts_Ipv6EntryKey{pfx: entry},
	}

	if explicitReplace && !r.ipv6Exists(pfx) {
		return nil, false, fmt.Errorf("cannot replace IPv6 Entry %s, does not exist", pfx)
	}

	var orig *aftpb.Afts_Ipv6EntryKey
	if r.ipv6Exists(pfx) {
		orig = r.retrieveIPv6(pfx)
	}

	if r.checkFn != nil {
		ok, err := r.checkFn(constants.Add, nr)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
	}

	r.doAddIPv6(pfx, entry)

	if r.postChangeHook != nil {
		r.postChangeHook(constants.Add, unixTS(), r.name, entry)
	}

	return orig, true, nil
}

// ipv6Exists reports whether prefix exists in the RIBHolder.
func (r *RIBHolder) ipv6Exists(prefix string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.r.IPv6Entry[prefix]
	return ok
}

// retrieveIPv6 returns the IPv6 entry for prefix under a read lock.
func (r *RIBHolder) retrieveIPv6(prefix string) *aftpb.Afts_Ipv6EntryKey {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.r.IPv6Entry[prefix]
}

// doAddIPv6 writes entry into the IPv6Entry map under a write lock.
func (r *RIBHolder) doAddIPv6(pfx string, entry *aftpb.Afts_Ipv6EntryKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.r.IPv6Entry == nil {
		r.r.IPv6Entry = make(map[string]*aftpb.Afts_Ipv6EntryKey, 1)
	}
	r.r.IPv6Entry[pfx] = entry
}

// DeleteIPv6 removes the IPv6 entry e from the RIBHolder.
// It returns the removed entry (if present), whether the delete succeeded, and any error.
func (r *RIBHolder) DeleteIPv6(e *aftpb.Afts_Ipv6EntryKey) (*aftpb.Afts_Ipv6EntryKey, bool, error) {
	if e == nil {
		return nil, false, errors.New("nil entry provided")
	}
	if r.r == nil {
		return nil, false, errors.New("invalid RIB structure, nil")
	}

	pfx := e.GetPrefix()
	if r.checkFn == nil || r.defaultParentRIB() != nil {
		r.mu.Lock()
		de := r.r.IPv6Entry[pfx]
		delete(r.r.IPv6Entry, pfx)
		hook := r.postChangeHook
		r.mu.Unlock()

		if hook != nil {
			hookEntry := de
			if hookEntry == nil {
				hookEntry = e
			}
			hook(constants.Delete, unixTS(), r.name, hookEntry)
		}
		return de, true, nil
	}

	de := r.retrieveIPv6(pfx)
	rr := &AFTs{
		IPv6Entry: map[string]*aftpb.Afts_Ipv6EntryKey{
			pfx: {Prefix: pfx, Ipv6Entry: &aftpb.Afts_Ipv6Entry{}},
		},
	}
	if r.checkFn != nil {
		ok, err := r.checkFn(constants.Delete, rr)
		switch {
		case err != nil:
			return nil, false, err
		case !ok:
			return nil, false, nil
		}
	}

	r.doDeleteIPv6(pfx)

	if r.postChangeHook != nil {
		hookEntry := de
		if hookEntry == nil {
			hookEntry = e
		}
		r.postChangeHook(constants.Delete, unixTS(), r.name, hookEntry)
	}

	return de, true, nil
}

// doDeleteIPv6 deletes pfx from the IPv6Entry map under a write lock.
func (r *RIBHolder) doDeleteIPv6(pfx string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.r.IPv6Entry, pfx)
}

// locklessDeleteIPv6 removes prefix from IPv6Entry when the caller already holds r.mu.
func (r *RIBHolder) locklessDeleteIPv6(prefix string) error {
	de := r.r.IPv6Entry[prefix]
	if de == nil {
		return fmt.Errorf("cannot find prefix %s", prefix)
	}
	delete(r.r.IPv6Entry, prefix)
	if r.postChangeHook != nil {
		r.postChangeHook(constants.Delete, unixTS(), r.name, de)
	}
	return nil
}

// AddMPLS validates, clones, and installs the MPLS label entry e into the RIBHolder.
// It returns any replaced entry, a boolean indicating whether the entry was installed, and any fatal error.
func (r *RIBHolder) AddMPLS(e *aftpb.Afts_LabelEntryKey, explicitReplace bool) (*aftpb.Afts_LabelEntryKey, bool, error) {
	if r.r == nil {
		return nil, false, errors.New("invalid RIB structure, nil")
	}
	if e == nil {
		return nil, false, errors.New("nil MPLS Entry provided")
	}

	lk, entry, err := validateAndCloneMPLS(e)
	if err != nil {
		return nil, false, fmt.Errorf("invalid LabelEntry, %v", err)
	}
	le := entry.GetLabelEntry()

	if parent := r.defaultParentRIB(); r.checkFn == nil || parent != nil {
		var extRIB *RIBHolder
		if parent != nil {
			if le.GetNextHopGroup().GetValue() == 0 {
				if explicitReplace && !r.mplsExists(lk) {
					return nil, false, fmt.Errorf("cannot replace MPLS Entry %d, does not exist", e.GetLabelUint64())
				}
				return nil, false, fmt.Errorf("invalid zero index NHG in LabelEntry %v, NI %s", entry.GetLabel(), r.name)
			}
			if otherNI := le.GetNextHopGroupNetworkInstance().GetValue(); otherNI != "" && otherNI != r.name {
				var ok bool
				extRIB, ok = parent.NetworkInstanceRIB(otherNI)
				if !ok {
					if explicitReplace && !r.mplsExists(lk) {
						return nil, false, fmt.Errorf("cannot replace MPLS Entry %d, does not exist", e.GetLabelUint64())
					}
					return nil, false, fmt.Errorf("invalid unknown network-instance for entry, %s", otherNI)
				}
			}
		}

		r.mu.Lock()
		orig, exists := r.r.LabelEntry[lk]
		if explicitReplace && !exists {
			r.mu.Unlock()
			return nil, false, fmt.Errorf("cannot replace MPLS Entry %d, does not exist", e.GetLabelUint64())
		}
		if parent != nil {
			nhg := le.GetNextHopGroup().GetValue()
			if extRIB != nil {
				if !extRIB.nhgExists(nhg) {
					r.mu.Unlock()
					return nil, false, nil
				}
			} else if _, ok := r.r.NextHopGroup[nhg]; !ok {
				r.mu.Unlock()
				return nil, false, nil
			}
		}
		if r.r.LabelEntry == nil {
			r.r.LabelEntry = make(map[MPLSLabelKey]*aftpb.Afts_LabelEntryKey, 1)
		}
		r.r.LabelEntry[lk] = entry
		hook := r.postChangeHook
		r.mu.Unlock()

		if hook != nil {
			hook(constants.Add, unixTS(), r.name, entry)
		}
		return orig, true, nil
	}

	nr := &AFTs{
		LabelEntry: map[MPLSLabelKey]*aftpb.Afts_LabelEntryKey{lk: entry},
	}

	if explicitReplace && !r.mplsExists(lk) {
		return nil, false, fmt.Errorf("cannot replace MPLS Entry %d, does not exist", e.GetLabelUint64())
	}

	var orig *aftpb.Afts_LabelEntryKey
	if r.mplsExists(lk) {
		orig = r.retrieveMPLS(lk)
	}

	if r.checkFn != nil {
		ok, err := r.checkFn(constants.Add, nr)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
	}

	r.doAddMPLS(lk, entry)

	if r.postChangeHook != nil {
		r.postChangeHook(constants.Add, unixTS(), r.name, entry)
	}

	return orig, true, nil
}

// mplsExists reports whether lk exists in the RIBHolder.
func (r *RIBHolder) mplsExists(lk MPLSLabelKey) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.r.LabelEntry[lk]
	return ok
}

// retrieveMPLS returns the MPLS entry for lk under a read lock.
func (r *RIBHolder) retrieveMPLS(lk MPLSLabelKey) *aftpb.Afts_LabelEntryKey {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.r.LabelEntry[lk]
}

// doAddMPLS writes entry into the LabelEntry map under a write lock.
func (r *RIBHolder) doAddMPLS(lk MPLSLabelKey, entry *aftpb.Afts_LabelEntryKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.r.LabelEntry == nil {
		r.r.LabelEntry = make(map[MPLSLabelKey]*aftpb.Afts_LabelEntryKey, 1)
	}
	r.r.LabelEntry[lk] = entry
}

// DeleteMPLS removes the MPLS label entry e from the RIBHolder.
// It returns the removed entry (if present), whether the delete succeeded, and any error.
func (r *RIBHolder) DeleteMPLS(e *aftpb.Afts_LabelEntryKey) (*aftpb.Afts_LabelEntryKey, bool, error) {
	if e == nil {
		return nil, false, errors.New("nil Label entry provided")
	}
	if r.r == nil {
		return nil, false, errors.New("invalid RIB structure, nil")
	}

	var lk MPLSLabelKey
	switch t := e.GetLabel().(type) {
	case *aftpb.Afts_LabelEntryKey_LabelUint64:
		lk = MPLSLabelKey{Label: t.LabelUint64}
	case *aftpb.Afts_LabelEntryKey_LabelOpenconfigmplstypesmplslabelenum:
		lk = MPLSLabelKey{Enum: t.LabelOpenconfigmplstypesmplslabelenum}
	default:
		return nil, false, fmt.Errorf("unsupported label type %T, %v", e, e)
	}

	if r.checkFn == nil || r.defaultParentRIB() != nil {
		r.mu.Lock()
		de := r.r.LabelEntry[lk]
		delete(r.r.LabelEntry, lk)
		hook := r.postChangeHook
		r.mu.Unlock()

		if hook != nil {
			hookEntry := de
			if hookEntry == nil {
				hookEntry = e
			}
			hook(constants.Delete, unixTS(), r.name, hookEntry)
		}
		return de, true, nil
	}

	de := r.retrieveMPLS(lk)
	rr := &AFTs{
		LabelEntry: map[MPLSLabelKey]*aftpb.Afts_LabelEntryKey{
			lk: e,
		},
	}
	if r.checkFn != nil {
		ok, err := r.checkFn(constants.Delete, rr)
		switch {
		case err != nil:
			return nil, false, err
		case !ok:
			return nil, false, nil
		}
	}

	r.doDeleteMPLS(lk)

	if r.postChangeHook != nil {
		hookEntry := de
		if hookEntry == nil {
			hookEntry = e
		}
		r.postChangeHook(constants.Delete, unixTS(), r.name, hookEntry)
	}

	return de, true, nil
}

// doDeleteMPLS deletes lk from the LabelEntry map under a write lock.
func (r *RIBHolder) doDeleteMPLS(lk MPLSLabelKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.r.LabelEntry, lk)
}

// locklessDeleteMPLS removes lk from LabelEntry when the caller already holds r.mu.
func (r *RIBHolder) locklessDeleteMPLS(lk MPLSLabelKey) error {
	de := r.r.LabelEntry[lk]
	if de == nil {
		return fmt.Errorf("cannot find label %v", lk)
	}
	delete(r.r.LabelEntry, lk)
	if r.postChangeHook != nil {
		r.postChangeHook(constants.Delete, unixTS(), r.name, de)
	}
	return nil
}

// AddNextHopGroup validates, clones, and installs the NextHopGroup e into the RIBHolder.
// It returns any replaced entry, a boolean indicating whether the entry was installed, and any fatal error.
func (r *RIBHolder) AddNextHopGroup(e *aftpb.Afts_NextHopGroupKey, explicitReplace bool) (*aftpb.Afts_NextHopGroupKey, bool, error) {
	if r.r == nil {
		return nil, false, errors.New("invalid RIB structure, nil")
	}
	if e == nil {
		return nil, false, errors.New("nil NextHopGroup provided")
	}

	entry, err := validateAndCloneNHG(e)
	if err != nil {
		return nil, false, fmt.Errorf("invalid NextHopGroup, %v", err)
	}
	id := entry.GetId()
	nhg := entry.GetNextHopGroup()

	if parent := r.defaultParentRIB(); r.checkFn == nil || parent != nil {
		r.mu.Lock()
		orig, exists := r.r.NextHopGroup[id]
		if explicitReplace && !exists {
			r.mu.Unlock()
			return nil, false, fmt.Errorf("cannot replace NextHopGroup %d, does not exist", id)
		}
		if parent != nil {
			if id == 0 {
				r.mu.Unlock()
				return nil, false, fmt.Errorf("invalid zero-index NHG")
			}
			if len(nhg.GetNextHop()) == 0 {
				r.mu.Unlock()
				return nil, false, fmt.Errorf("empty next-hop-group")
			}
			for _, n := range nhg.GetNextHop() {
				if n.GetIndex() == 0 {
					r.mu.Unlock()
					return nil, false, fmt.Errorf("invalid zero index NH in NHG %d, NI %s", id, r.name)
				}
				if _, ok := r.r.NextHop[n.GetIndex()]; !ok {
					r.mu.Unlock()
					if log.V(2) {
						log.Infof("NextHopGroup %d added to pending queue - not installed", id)
					}
					return nil, false, nil
				}
			}
		}
		if r.r.NextHopGroup == nil {
			r.r.NextHopGroup = make(map[uint64]*aftpb.Afts_NextHopGroupKey, 1)
		}
		r.r.NextHopGroup[id] = entry
		hook := r.postChangeHook
		r.mu.Unlock()

		if hook != nil {
			hook(constants.Add, unixTS(), r.name, entry)
		}
		return orig, true, nil
	}

	nr := &AFTs{
		NextHopGroup: map[uint64]*aftpb.Afts_NextHopGroupKey{id: entry},
	}

	if explicitReplace && !r.nhgExists(id) {
		return nil, false, fmt.Errorf("cannot replace NextHopGroup %d, does not exist", id)
	}

	var orig *aftpb.Afts_NextHopGroupKey
	if r.nhgExists(id) {
		orig = r.retrieveNHG(id)
	}

	if r.checkFn != nil {
		ok, err := r.checkFn(constants.Add, nr)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			if log.V(2) {
				log.Infof("NextHopGroup %d added to pending queue - not installed", id)
			}
			return nil, false, nil
		}
	}

	r.doAddNHG(id, entry)

	if r.postChangeHook != nil {
		r.postChangeHook(constants.Add, unixTS(), r.name, entry)
	}

	return orig, true, nil
}

// nhgExists reports whether the NextHopGroup with ID id exists in the RIBHolder.
func (r *RIBHolder) nhgExists(id uint64) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.r.NextHopGroup[id]
	return ok
}

// retrieveNHG returns the NextHopGroup for id under a read lock.
func (r *RIBHolder) retrieveNHG(id uint64) *aftpb.Afts_NextHopGroupKey {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.r.NextHopGroup[id]
}

// doAddNHG writes entry into the NextHopGroup map under a write lock.
func (r *RIBHolder) doAddNHG(id uint64, entry *aftpb.Afts_NextHopGroupKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.r.NextHopGroup == nil {
		r.r.NextHopGroup = make(map[uint64]*aftpb.Afts_NextHopGroupKey, 1)
	}
	r.r.NextHopGroup[id] = entry
}

// DeleteNextHopGroup removes the NextHopGroup entry e from the RIBHolder.
// It returns the removed entry (if present), whether the delete succeeded, and any error.
func (r *RIBHolder) DeleteNextHopGroup(e *aftpb.Afts_NextHopGroupKey) (*aftpb.Afts_NextHopGroupKey, bool, error) {
	if e == nil {
		return nil, false, errors.New("nil entry provided")
	}
	if r.r == nil {
		return nil, false, errors.New("invalid RIB structure, nil")
	}

	id := e.GetId()
	if id == 0 {
		return nil, false, errors.New("invalid NHG ID 0")
	}

	if parent := r.defaultParentRIB(); r.checkFn == nil || parent != nil {
		r.mu.Lock()
		de, exists := r.r.NextHopGroup[id]
		if exists && parent != nil && r.nhgReferenced(id) {
			r.mu.Unlock()
			return nil, false, nil
		}
		delete(r.r.NextHopGroup, id)
		hook := r.postChangeHook
		r.mu.Unlock()

		if hook != nil {
			hookEntry := de
			if hookEntry == nil {
				hookEntry = e
			}
			hook(constants.Delete, unixTS(), r.name, hookEntry)
		}
		return de, true, nil
	}

	de := r.retrieveNHG(id)
	rr := &AFTs{
		NextHopGroup: map[uint64]*aftpb.Afts_NextHopGroupKey{
			id: {Id: id, NextHopGroup: &aftpb.Afts_NextHopGroup{}},
		},
	}
	if r.checkFn != nil {
		ok, err := r.checkFn(constants.Delete, rr)
		switch {
		case err != nil:
			return nil, false, err
		case !ok:
			return nil, false, nil
		}
	}

	r.doDeleteNHG(id)

	if r.postChangeHook != nil {
		hookEntry := de
		if hookEntry == nil {
			hookEntry = e
		}
		r.postChangeHook(constants.Delete, unixTS(), r.name, hookEntry)
	}

	return de, true, nil
}

// doDeleteNHG deletes idx from the NextHopGroup map under a write lock.
func (r *RIBHolder) doDeleteNHG(idx uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.r.NextHopGroup, idx)
}

// locklessDeleteNHG removes id from NextHopGroup when the caller already holds r.mu.
func (r *RIBHolder) locklessDeleteNHG(id uint64) error {
	de := r.r.NextHopGroup[id]
	if de == nil {
		return fmt.Errorf("cannot find NHG %d", id)
	}
	for _, nh := range de.GetNextHopGroup().GetNextHop() {
		r.decNHRefCount(nh.GetIndex())
	}
	delete(r.r.NextHopGroup, id)
	if r.postChangeHook != nil {
		r.postChangeHook(constants.Delete, unixTS(), r.name, de)
	}
	return nil
}

// incNHGRefCount increments the reference count for the specified NextHopGroup ID.
func (r *RIBHolder) incNHGRefCount(i uint64) {
	if r.refCounts == nil {
		return
	}
	r.refCounts.mu.Lock()
	defer r.refCounts.mu.Unlock()
	r.refCounts.NextHopGroup[i]++
}

// decNHGRefCount decrements the reference count for the specified NextHopGroup ID.
func (r *RIBHolder) decNHGRefCount(i uint64) {
	if r.refCounts == nil {
		return
	}
	r.refCounts.mu.Lock()
	defer r.refCounts.mu.Unlock()
	if r.refCounts.NextHopGroup[i] == 0 {
		return
	}
	r.refCounts.NextHopGroup[i]--
}

// nhgReferenced reports whether the NextHopGroup with ID i has a positive reference count.
func (r *RIBHolder) nhgReferenced(i uint64) bool {
	if r.refCounts == nil {
		return false
	}
	r.refCounts.mu.RLock()
	defer r.refCounts.mu.RUnlock()
	return r.refCounts.NextHopGroup[i] > 0
}

// AddNextHop validates, clones, and installs the NextHop e into the RIBHolder.
// It returns any replaced entry, a boolean indicating whether the entry was installed, and any fatal error.
func (r *RIBHolder) AddNextHop(e *aftpb.Afts_NextHopKey, explicitReplace bool) (*aftpb.Afts_NextHopKey, bool, error) {
	if r.r == nil {
		return nil, false, errors.New("invalid RIB structure, nil")
	}
	if e == nil {
		return nil, false, errors.New("nil NextHop provided")
	}

	entry, err := validateAndCloneNH(e)
	if err != nil {
		return nil, false, fmt.Errorf("invalid NextHop, %v", err)
	}
	idx := entry.GetIndex()

	if parent := r.defaultParentRIB(); r.checkFn == nil || parent != nil {
		r.mu.Lock()
		replaced, exists := r.r.NextHop[idx]
		if explicitReplace && !exists {
			r.mu.Unlock()
			return nil, false, fmt.Errorf("cannot replace NextHop %d, does not exist", idx)
		}
		if parent != nil && idx == 0 {
			r.mu.Unlock()
			return nil, false, fmt.Errorf("invalid index zero for next-hop in NI %s", r.name)
		}
		if r.r.NextHop == nil {
			r.r.NextHop = make(map[uint64]*aftpb.Afts_NextHopKey, 1)
		}
		r.r.NextHop[idx] = entry
		hook := r.postChangeHook
		r.mu.Unlock()

		if hook != nil {
			hook(constants.Add, unixTS(), r.name, entry)
		}
		return replaced, true, nil
	}

	nr := &AFTs{
		NextHop: map[uint64]*aftpb.Afts_NextHopKey{idx: entry},
	}

	if explicitReplace && !r.nhExists(idx) {
		return nil, false, fmt.Errorf("cannot replace NextHop %d, does not exist", idx)
	}

	var replaced *aftpb.Afts_NextHopKey
	if r.nhExists(idx) {
		replaced = r.retrieveNH(idx)
	}

	if r.checkFn != nil {
		ok, err := r.checkFn(constants.Add, nr)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
	}

	r.doAddNH(idx, entry)

	if r.postChangeHook != nil {
		r.postChangeHook(constants.Add, unixTS(), r.name, entry)
	}

	return replaced, true, nil
}

// nhExists reports whether the NextHop with index exists in the RIBHolder.
func (r *RIBHolder) nhExists(index uint64) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.r.NextHop[index]
	return ok
}

// retrieveNH returns the NextHop for index under a read lock.
func (r *RIBHolder) retrieveNH(index uint64) *aftpb.Afts_NextHopKey {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.r.NextHop[index]
}

// doAddNH writes entry into the NextHop map under a write lock.
func (r *RIBHolder) doAddNH(index uint64, entry *aftpb.Afts_NextHopKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.r.NextHop == nil {
		r.r.NextHop = make(map[uint64]*aftpb.Afts_NextHopKey, 1)
	}
	r.r.NextHop[index] = entry
}

// DeleteNextHop removes the NextHop entry e from the RIBHolder.
// It returns the removed entry (if present), whether the delete succeeded, and any error.
func (r *RIBHolder) DeleteNextHop(e *aftpb.Afts_NextHopKey) (*aftpb.Afts_NextHopKey, bool, error) {
	if e == nil {
		return nil, false, errors.New("nil entry provided")
	}
	if r.r == nil {
		return nil, false, errors.New("invalid RIB structure, nil")
	}

	idx := e.GetIndex()
	if idx == 0 {
		return nil, false, fmt.Errorf("invalid NH index 0")
	}

	if parent := r.defaultParentRIB(); r.checkFn == nil || parent != nil {
		r.mu.Lock()
		de, exists := r.r.NextHop[idx]
		if exists && parent != nil && r.nhReferenced(idx) {
			r.mu.Unlock()
			return nil, false, nil
		}
		delete(r.r.NextHop, idx)
		hook := r.postChangeHook
		r.mu.Unlock()

		if hook != nil {
			hookEntry := de
			if hookEntry == nil {
				hookEntry = e
			}
			hook(constants.Delete, unixTS(), r.name, hookEntry)
		}
		return de, true, nil
	}

	de := r.retrieveNH(idx)
	rr := &AFTs{
		NextHop: map[uint64]*aftpb.Afts_NextHopKey{
			idx: {Index: idx, NextHop: &aftpb.Afts_NextHop{}},
		},
	}
	if r.checkFn != nil {
		ok, err := r.checkFn(constants.Delete, rr)
		switch {
		case err != nil:
			return nil, false, err
		case !ok:
			return nil, false, nil
		}
	}

	r.doDeleteNH(idx)

	if r.postChangeHook != nil {
		hookEntry := de
		if hookEntry == nil {
			hookEntry = e
		}
		r.postChangeHook(constants.Delete, unixTS(), r.name, hookEntry)
	}

	return de, true, nil
}

// doDeleteNH deletes id from the NextHop map under a write lock.
func (r *RIBHolder) doDeleteNH(id uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.r.NextHop, id)
}

// locklessDeleteNH removes index from NextHop when the caller already holds r.mu.
func (r *RIBHolder) locklessDeleteNH(index uint64) error {
	de := r.r.NextHop[index]
	if de == nil {
		return fmt.Errorf("cannot find NH %d", index)
	}
	delete(r.r.NextHop, index)
	if r.postChangeHook != nil {
		r.postChangeHook(constants.Delete, unixTS(), r.name, de)
	}
	return nil
}

// incNHRefCount increments the reference count for the specified NextHop index.
func (r *RIBHolder) incNHRefCount(i uint64) {
	if r.refCounts == nil {
		return
	}
	r.refCounts.mu.Lock()
	defer r.refCounts.mu.Unlock()
	r.refCounts.NextHop[i]++
}

// decNHRefCount decrements the reference count for the specified NextHop index.
func (r *RIBHolder) decNHRefCount(i uint64) {
	if r.refCounts == nil {
		return
	}
	r.refCounts.mu.Lock()
	defer r.refCounts.mu.Unlock()
	if r.refCounts.NextHop[i] == 0 {
		return
	}
	r.refCounts.NextHop[i]--
}

// nhReferenced reports whether the NextHop with index i has a positive reference count.
func (r *RIBHolder) nhReferenced(i uint64) bool {
	if r.refCounts == nil {
		return false
	}
	r.refCounts.mu.RLock()
	defer r.refCounts.mu.RUnlock()
	return r.refCounts.NextHop[i] > 0
}

// GetRIB streams the contents of the AFTs matching filter as GetResponse messages to msgCh.
// Because stored entry protobufs are immutable, GetRIB reuses them directly without re-marshalling.
func (r *RIBHolder) GetRIB(filter map[spb.AFTType]bool, msgCh chan *spb.GetResponse, stopCh chan struct{}) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if filter[spb.AFTType_ALL] {
		filter = map[spb.AFTType]bool{
			spb.AFTType_IPV4:          true,
			spb.AFTType_MPLS:          true,
			spb.AFTType_NEXTHOP:       true,
			spb.AFTType_NEXTHOP_GROUP: true,
			spb.AFTType_IPV6:          true,
		}
	}

	if filter[spb.AFTType_IPV4] {
		for _, e := range r.r.IPv4Entry {
			select {
			case <-stopCh:
				return nil
			default:
				var resp struct {
					msg   spb.GetResponse
					entry spb.AFTEntry
					oneof spb.AFTEntry_Ipv4
					slice [1]*spb.AFTEntry
				}
				resp.oneof.Ipv4 = e
				resp.entry.NetworkInstance = r.name
				resp.entry.Entry = &resp.oneof
				resp.slice[0] = &resp.entry
				resp.msg.Entry = resp.slice[:]
				msgCh <- &resp.msg
			}
		}
	}

	if filter[spb.AFTType_IPV6] {
		for _, e := range r.r.IPv6Entry {
			select {
			case <-stopCh:
				return nil
			default:
				var resp struct {
					msg   spb.GetResponse
					entry spb.AFTEntry
					oneof spb.AFTEntry_Ipv6
					slice [1]*spb.AFTEntry
				}
				resp.oneof.Ipv6 = e
				resp.entry.NetworkInstance = r.name
				resp.entry.Entry = &resp.oneof
				resp.slice[0] = &resp.entry
				resp.msg.Entry = resp.slice[:]
				msgCh <- &resp.msg
			}
		}
	}

	if filter[spb.AFTType_MPLS] {
		for _, e := range r.r.LabelEntry {
			select {
			case <-stopCh:
				return nil
			default:
				var resp struct {
					msg   spb.GetResponse
					entry spb.AFTEntry
					oneof spb.AFTEntry_Mpls
					slice [1]*spb.AFTEntry
				}
				resp.oneof.Mpls = e
				resp.entry.NetworkInstance = r.name
				resp.entry.Entry = &resp.oneof
				resp.slice[0] = &resp.entry
				resp.msg.Entry = resp.slice[:]
				msgCh <- &resp.msg
			}
		}
	}

	if filter[spb.AFTType_NEXTHOP_GROUP] {
		for _, e := range r.r.NextHopGroup {
			select {
			case <-stopCh:
				return nil
			default:
				var resp struct {
					msg   spb.GetResponse
					entry spb.AFTEntry
					oneof spb.AFTEntry_NextHopGroup
					slice [1]*spb.AFTEntry
				}
				resp.oneof.NextHopGroup = e
				resp.entry.NetworkInstance = r.name
				resp.entry.Entry = &resp.oneof
				resp.slice[0] = &resp.entry
				resp.msg.Entry = resp.slice[:]
				msgCh <- &resp.msg
			}
		}
	}

	if filter[spb.AFTType_NEXTHOP] {
		for _, e := range r.r.NextHop {
			select {
			case <-stopCh:
				return nil
			default:
				var resp struct {
					msg   spb.GetResponse
					entry spb.AFTEntry
					oneof spb.AFTEntry_NextHop
					slice [1]*spb.AFTEntry
				}
				resp.oneof.NextHop = e
				resp.entry.NetworkInstance = r.name
				resp.entry.Entry = &resp.oneof
				resp.slice[0] = &resp.entry
				resp.msg.Entry = resp.slice[:]
				msgCh <- &resp.msg
			}
		}
	}

	return nil
}

// FlushErr aggregates errors encountered during a Flush operation.
type FlushErr struct {
	// Errs holds the slice of errors collected while flushing.
	Errs []error
}

// Error returns the concatenated string representation of all errors in FlushErr.
func (f *FlushErr) Error() string {
	b := &bytes.Buffer{}
	for _, err := range f.Errs {
		b.WriteString(fmt.Sprintf("%s\n", err))
	}
	return b.String()
}

// flushNetworkInstance removes all entries from a single network instance while holding its lock.
func (r *RIB) flushNetworkInstance(netInst string) []error {
	niR, ok := r.NetworkInstanceRIB(netInst)
	if !ok {
		log.Errorf("cannot find network instance RIB for %s", netInst)
		return nil
	}

	niR.mu.Lock()
	defer niR.mu.Unlock()

	var errs []error
	for p, entry := range niR.r.IPv4Entry {
		v4 := entry.GetIpv4Entry()
		referencedRIB, err := r.refdRIB(niR, v4.GetNextHopGroupNetworkInstance().GetValue())
		switch {
		case err != nil:
			log.Errorf("cannot find network instance RIB %s during Flush for IPv4 prefix %s", v4.GetNextHopGroupNetworkInstance().GetValue(), p)
		default:
			referencedRIB.decNHGRefCount(v4.GetNextHopGroup().GetValue())
		}
		if err := niR.locklessDeleteIPv4(p); err != nil {
			errs = append(errs, err)
		}
	}

	for p, entry := range niR.r.IPv6Entry {
		v6 := entry.GetIpv6Entry()
		referencedRIB, err := r.refdRIB(niR, v6.GetNextHopGroupNetworkInstance().GetValue())
		switch {
		case err != nil:
			log.Errorf("cannot find network instance RIB %s during Flush for IPv6 prefix %s", v6.GetNextHopGroupNetworkInstance().GetValue(), p)
		default:
			referencedRIB.decNHGRefCount(v6.GetNextHopGroup().GetValue())
		}
		if err := niR.locklessDeleteIPv6(p); err != nil {
			errs = append(errs, err)
		}
	}

	for label, entry := range niR.r.LabelEntry {
		le := entry.GetLabelEntry()
		referencedRIB, err := r.refdRIB(niR, le.GetNextHopGroupNetworkInstance().GetValue())
		switch {
		case err != nil:
			log.Errorf("cannot find network instance RIB %s during Flush for MPLS label %v", le.GetNextHopGroupNetworkInstance().GetValue(), label)
		default:
			referencedRIB.decNHGRefCount(le.GetNextHopGroup().GetValue())
		}
		if err := niR.locklessDeleteMPLS(label); err != nil {
			errs = append(errs, err)
		}
	}

	var backupNHGs []uint64
	for _, nhg := range niR.r.NextHopGroup {
		if b := nhg.GetNextHopGroup().GetBackupNextHopGroup(); b != nil {
			backupNHGs = append(backupNHGs, b.GetValue())
		}
	}

	delNHG := func(id uint64) {
		if err := niR.locklessDeleteNHG(id); err != nil {
			errs = append(errs, err)
		}
	}

	for _, id := range backupNHGs {
		if _, ok := niR.r.NextHopGroup[id]; ok {
			delNHG(id)
		}
	}

	for n := range niR.r.NextHopGroup {
		delNHG(n)
	}

	for n := range niR.r.NextHop {
		if err := niR.locklessDeleteNH(n); err != nil {
			errs = append(errs, err)
		}
	}

	return errs
}

// Flush removes all entries from the specified network instances in dependency order.
func (r *RIB) Flush(nis []string) error {
	var errs []error
	for _, netInst := range nis {
		errs = append(errs, r.flushNetworkInstance(netInst)...)
	}
	if len(errs) != 0 {
		return &FlushErr{Errs: errs}
	}
	return nil
}

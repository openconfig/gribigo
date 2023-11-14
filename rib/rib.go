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

// Package rib implements a basic RIB for a gRIBI server.
package rib

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"time"

	log "github.com/golang/glog"
	"github.com/kylelemons/godebug/pretty"
	"github.com/openconfig/gnmi/value"
	"github.com/openconfig/goyang/pkg/yang"
	"github.com/openconfig/gribigo/aft"
	"github.com/openconfig/gribigo/constants"
	wpb "github.com/openconfig/ygot/proto/ywrapper"
	"github.com/openconfig/ygot/protomap"
	"github.com/openconfig/ygot/ygot"
	"github.com/openconfig/ygot/ytypes"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	aftpb "github.com/openconfig/gribi/v1/proto/gribi_aft"
	spb "github.com/openconfig/gribi/v1/proto/service"
)

// unixTS is used to determine the current unix timestamp in nanoseconds since the
// epoch. It is defined such that it can be overloaded by unit tests.
var unixTS = time.Now().UnixNano

// RIBHookFn is a function that is used as a hook following a change. It takes:
//   - an OpType deterining whether an add, remove, or modify operation was sent.
//   - the timestamp in nanoseconds since the unix epoch that a function was performed.
//   - a string indicating the name of the network instance
//   - a ygot.ValidatedGoStruct containing the entry that has been changed.
type RIBHookFn func(constants.OpType, int64, string, ygot.ValidatedGoStruct)

// ResolvedEntryFn is a function that is called for all entries that can be fully
// resolved within the RIB. Fully resolved in this case is defined as an input
// packet match criteria set of next-hops.
//
// It takes arguments of:
//   - the set of RIBs that were stored in the RIB as a map keyed by the name of
//     a network instance, with a RIB represented as a ygot-generated AFT struct.
//   - the prefix that was impacted.
//   - the OpType that the entry was subject to (add/replace/delete).
//   - a string indicating the network instance that the operation was within
//   - an enumerated value indicating the AFT the operation was within.
//   - an any that indicates the impacted AFT entry's key. The function must cast
//     the any to the relevant type.
//   - a set of details that the handler function may utilise.
type ResolvedEntryFn func(ribs map[string]*aft.RIB, optype constants.OpType, netinst string, aft constants.AFT, key any, dets ...ResolvedDetails)

// ResolvedDetails is an interface implemented by any type that is returned as
// part of the AFT details.
type ResolvedDetails interface {
	isResolvedDetail()
}

// RIBHolderCheckFunc is a function that is used as a check to determine whether
// a RIB entry is eligible for a particular operation. It takes arguments of:
//
//   - the operation type that is being performed.
//
//   - the network instance within which the operation should be considered.
//
//   - the RIB that describes the candidate changes. In the case that the operation
//     is an ADD or REPLACE the candidate must contain the entry that would be added
//     or replaced. In the case that it is a DELETE, the candidate contains the entry
//     that is to be deleted.
//
//     The candidate contains a single entry.
//
// The check function must return:
//   - a bool indicating whether the RIB operation should go ahead (true = proceed).
//   - an error that is considered fatal for the entry (i.e., this entry should never
//     be tried again).
type RIBHolderCheckFunc func(constants.OpType, string, *aft.RIB) (bool, error)

// RIB is a struct that stores a representation of a RIB for a network device.
type RIB struct {
	// nrMu protects the niRIB map.
	nrMu sync.RWMutex
	// niRIB is a map of OpenConfig AFTs that are used to represent the RIBs of a network element.
	// The key of the map is the name of the network instance to which the RIBs belong.
	niRIB map[string]*RIBHolder

	// defaultName is the name assigned to the default network instance.
	defaultName string
	// ribCheck indicates whether this RIB is running the RIB check function.
	ribCheck bool

	// pendMu protects the pendingCandidates.
	pendMu sync.RWMutex
	// pendingEntries is the set of entries that have been requested by
	// the AddXXX methods that cannot yet be installed in the RIB because they do
	// not resolve. Resolve is defined as canResolve returning true - which means that:
	//  - entries (ipv4, ipv6 etc.) reference valid NHGs
	//  - NHGs reference valid NHs
	//  - NHs are accepted by default (since they can be resolved with other protocols)
	//
	// After every successful AddXXX operation the list of candidates is walked to
	// determine whether they are now resolvable.
	//
	// The candidates are stored as the operation that was submitted in order that the
	// same AddXXX methods can be used along with network instance the operation
	// referred to. The map is keyed by the operation ID.
	pendingEntries map[uint64]*pendingEntry

	// resolvedEntryHook is a function that is called for all entries that
	// can be fully resolved in the RIB. In the current implementation it
	// is called only for IPv4 entries.
	resolvedEntryHook ResolvedEntryFn
}

// RIBHolder is a container for a set of RIBs.
type RIBHolder struct {
	// name is the name that is used for this network instance by the system.
	name string

	// mu protects the aft.RIB datastructure. This is a coarse lock, but is
	// the simplest implementation -- we can create a more fine-grained lock
	// if performance requires it.
	mu sync.RWMutex
	// r is the RIB within the network instance as the OpenConfig AFT model.
	r *aft.RIB

	// TODO(robjs): flag as to whether we should run any semantic validations
	// as we add to the RIB. We probably want to allow invalid entries to be
	// implemented.

	// checkFn is a function that is called for all entries before they are
	// considered valid candidates to have the operation op performed for them.
	//  It can be used to check that an entry is resolvable, or whether an
	// entry is referenced before deleting it.
	// The argument handed to it is a candidate RIB as described by an aft.RIB
	// structure. It returns a boolean indicating
	// whether the entry should be installed, or an error indicating that the
	// entry is not valid for installation.
	//
	// When checkFn returns false, but no error is returned, it is expected
	// that the client of the RIB can retry to process this entry at a later
	// point in time. If an error is returned, the checkFn is asserting that
	// there is no way that this entry can ever be installed in the RIB,
	// regardless of whether there are state changes.
	checkFn func(op constants.OpType, a *aft.RIB) (bool, error)

	// postChangeHook is a function that is called after each of the operations
	// within the RIB completes, it takes arguments of the
	//   - name of the network instance
	// 	 - operation type (as an constants.OpType enumerated value)
	//	 - the changed entry as a ygot.ValidatedGoStruct.
	postChangeHook RIBHookFn

	// refCounts is used to store counters for the number of references to next-hop
	// groups and next-hops within the RIB. It is used to ensure that referenced NHs
	// and NHGs cnanot be removed from the RIB.
	refCounts *niRefCounter
}

// niRefCounter stores reference counters for a particular network instance.
type niRefCounter struct {
	// mu protects the contents of niRefCounter
	mu sync.RWMutex
	// NextHop is the reference counter for NextHops - and is keyed by the
	// index of the next-hop within the network instance.
	NextHop map[uint64]uint64
	// NextHopGroup is the referenced counter for NextHopGroups within a
	// network instance, and is keyed by the ID of the next-hop-group
	// within the network instance.
	NextHopGroup map[uint64]uint64
}

// String returns a string representation of the RIBHolder.
func (r *RIBHolder) String() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	js, err := ygot.Marshal7951(r.r, ygot.JSONIndent("  "))
	if err != nil {
		return "invalid RIB"
	}
	return string(js)
}

// RIBOpt is an interface that is implemented for options to the RIB.
type RIBOpt interface {
	isRIBOpt()
}

// DisableRIBCheckFn specifies that the consistency checking functions should
// be disabled for the RIB. It is useful for a testing RIB that does not need
// to have working references.
func DisableRIBCheckFn() *disableCheckFn { return &disableCheckFn{} }

// disableCheckFn is the internal implementation of DisableRIBCheckFn.
type disableCheckFn struct{}

// isRIBOpt implements the RIBOpt interface
func (*disableCheckFn) isRIBOpt() {}

// hasDisableCheckFn checks whether the RIBOpt slice supplied contains the
// disableCheckFn option.
func hasDisableCheckFn(opt []RIBOpt) bool {
	for _, o := range opt {
		if _, ok := o.(*disableCheckFn); ok {
			return true
		}
	}
	return false
}

// New returns a new RIB with the default network instance created with name dn.
func New(dn string, opt ...RIBOpt) *RIB {
	r := &RIB{
		niRIB:          map[string]*RIBHolder{},
		defaultName:    dn,
		pendingEntries: map[uint64]*pendingEntry{},
	}

	rhOpt := []ribHolderOpt{}
	checkRIB := !hasDisableCheckFn(opt)
	if checkRIB {
		rhOpt = append(rhOpt, RIBHolderCheckFn(r.checkFn))
	}
	r.ribCheck = checkRIB

	r.niRIB[dn] = NewRIBHolder(dn, rhOpt...)

	return r
}

// checkFn wraps canResolve and canDelete to implement a RIBHolderCheckFn
func (r *RIB) checkFn(t constants.OpType, ni string, candidate *aft.RIB) (bool, error) {
	switch t {
	case constants.Add:
		// Replace has exactly the same validation as Add, we always just get called
		// with Add (since Add can be an implicit replace anyway).
		return r.canResolve(ni, candidate)
	case constants.Delete:
		return r.canDelete(ni, candidate)
	}
	return false, fmt.Errorf("invalid unknown operation type, %s", t)
}

// pendingEntry describes an operation that is pending on the gRIBI server. Generally,
// this is due to RIB recursion lookup failures.
type pendingEntry struct {
	// ni is the network instance the operation is operating on.
	ni string
	// op is the AFTOperation that is being performed.
	op *spb.AFTOperation
}

// SetPostChangeHook assigns the supplied hook to all network instance RIBs within
// the RIB structure.
func (r *RIB) SetPostChangeHook(fn RIBHookFn) {
	for _, nir := range r.niRIB {
		nir.mu.Lock()
		nir.postChangeHook = fn
		nir.mu.Unlock()
	}
}

// SetResolvedEntryHook asssigns the supplied hook to all network instance RIBs within
// the RIB structure.
func (r *RIB) SetResolvedEntryHook(fn ResolvedEntryFn) {
	r.resolvedEntryHook = fn
}

// NetworkInstanceRIB returns the RIB for the network instance with name s.
func (r *RIB) NetworkInstanceRIB(s string) (*RIBHolder, bool) {
	r.nrMu.RLock()
	defer r.nrMu.RUnlock()
	rh, ok := r.niRIB[s]
	return rh, ok
}

// AddNetworkInstance adds a new network instance with the specified name
// to the RIB.
func (r *RIB) AddNetworkInstance(name string) error {
	r.nrMu.Lock()
	defer r.nrMu.Unlock()

	if r.niRIB[name] != nil {
		return fmt.Errorf("RIB %s already exists", name)
	}

	rhOpt := []ribHolderOpt{}
	if r.ribCheck {
		rhOpt = append(rhOpt, RIBHolderCheckFn(r.checkFn))
	}

	r.niRIB[name] = NewRIBHolder(name, rhOpt...)
	return nil
}

// KnownNetworkInstances returns the name of all known network instances
// within the RIB.
func (r *RIB) KnownNetworkInstances() []string {
	r.nrMu.RLock()
	defer r.nrMu.RUnlock()
	names := []string{}
	for n := range r.niRIB {
		names = append(names, n)
	}
	// return the RIB names in a stable order.
	sort.Strings(names)
	return names
}

// RIBContents returns the contents of the RIB in a manner that an external
// caller can interact with. It returns a map, keyed by network instance name,
// with a deep copy of the RIB contents. Since copying large RIBs may be expensive
// care should be taken with when it is used. A copy is used since the RIB continues
// to handle concurrent changes to the contents from multiple sources.
func (r *RIB) RIBContents() (map[string]*aft.RIB, error) {
	return r.copyRIBs()
}

// String returns a string representation of the RIB.
func (r *RIB) String() string {
	r.nrMu.RLock()
	defer r.nrMu.RUnlock()
	buf := &bytes.Buffer{}
	for ni, niR := range r.niRIB {
		buf.WriteString(fmt.Sprintf("%s:\n-----\n%s\n", ni, niR))
	}
	return buf.String()
}

// OpResult contains the result of an operation (Add, Modify, Delete).
type OpResult struct {
	// ID is the ID of the operation as specified in the input request.
	ID uint64
	// Op is the operation that was performed.
	Op *spb.AFTOperation
	// Error is an error string detailing any error that occurred.
	Error string
}

// String returns the OpResult as a human readable string.
func (o *OpResult) String() string {
	return fmt.Sprintf("ID: %d, Type: %s, Error: %v", o.ID, prototext.Format(o.Op), o.Error)
}

// AddEntry adds the entry described in op to the network instance with name ni. It returns
// two slices of OpResults:
//   - the first ("oks") describes the set of entries that were installed successfully based on
//     this operation.
//   - the second ("fails") describes the set of entries that were NOT installed, and encountered
//     fatal errors during the process of installing the entry.
//
// It returns an error if there is a fatal error encountered for the function during operation.
//
// If the input AFT operation is a REPLACE operation, AddEntry ensures that the entry exists within
// the RIB before replacing it.
//
// The oks slice may have length > 1 (i.e., not just be the input operation) in the case an entry
// becomes resolvable (per canResolve) *after* this operation has been installed. It will recursively
// call the internal implementation in order to install all entries that are now resolvable based
// on the operation provided.
func (r *RIB) AddEntry(ni string, op *spb.AFTOperation) ([]*OpResult, []*OpResult, error) {
	if ni == "" {
		return nil, nil, fmt.Errorf("invalid network instance, %s", ni)
	}

	oks, fails := []*OpResult{}, []*OpResult{}
	checked := map[uint64]bool{}
	if err := r.addEntryInternal(ni, op, &oks, &fails, checked); err != nil {
		return nil, nil, err
	}

	return oks, fails, nil
}

// addEntryInternal is the internal implementation of AddEntry. It takes arguments of:
//   - the name of the network instance being operated on (ni) by the operation op.
//   - a slice of installed results, which is appended to.
//   - a slice of failed results, which is appended to.
//   - a map, keyed by operation ID, describing the stack of calls that we have currently
//     done during this recursion so that we do not repeat an install operation.
func (r *RIB) addEntryInternal(ni string, op *spb.AFTOperation, oks, fails *[]*OpResult, installStack map[uint64]bool) error {
	if installStack[op.GetId()] {
		return nil
	}
	niR, ok := r.NetworkInstanceRIB(ni)
	if !ok || !niR.IsValid() {
		return fmt.Errorf("invalid network instance, %s", ni)
	}

	explicitReplace := false
	if op.GetOp() == spb.AFTOperation_REPLACE {
		explicitReplace = true
	}

	var (
		installed bool
		opErr     error
		// Used to store information about the transaction that was
		// completed in case it completes successfully.
		v4Prefix, v6Prefix string
		mplsLabel          uint64
	)

	switch t := op.Entry.(type) {
	case *spb.AFTOperation_Ipv4:
		log.V(2).Infof("[op %d] attempting to add IPv4 prefix %s", op.GetId(), t.Ipv4.GetPrefix())
		done, orig, err := niR.AddIPv4(t.Ipv4, explicitReplace)
		switch {
		case err != nil:
			opErr = err
		case done:
			installed = done
			v4Prefix = t.Ipv4.GetPrefix()
			handleReferences(r, niR, orig, t.Ipv4.GetIpv4Entry())
		}
	case *spb.AFTOperation_Ipv6:
		v6Prefix = t.Ipv6.GetPrefix()
		log.V(2).Info("[op %d] attempting to add IPv6 prefix %s", op.GetId(), t.Ipv6.GetPrefix())
		done, orig, err := niR.AddIPv6(t.Ipv6, explicitReplace)
		switch {
		case err != nil:
			opErr = err
		case done:
			installed = done
			handleReferences(r, niR, orig, t.Ipv6.GetIpv6Entry())
		}
	case *spb.AFTOperation_Mpls:
		mplsLabel = t.Mpls.GetLabelUint64()
		log.V(2).Infof("[op %d] attempting to add MPLS label entry %d", op.GetId(), mplsLabel)
		done, orig, err := niR.AddMPLS(t.Mpls, explicitReplace)
		switch {
		case err != nil:
			opErr = err
		case done:
			installed = done
			handleReferences(r, niR, orig, t.Mpls.GetLabelEntry())
		}
	case *spb.AFTOperation_NextHopGroup:
		log.V(2).Infof("[op %d] attempting to add NHG ID %d", op.GetId(), t.NextHopGroup.GetId())
		done, orig, err := niR.AddNextHopGroup(t.NextHopGroup, explicitReplace)
		switch {
		case err != nil:
			opErr = err
		case done:
			r.handleNHGReferences(niR, orig, t.NextHopGroup.GetNextHopGroup())
			installed = done
		}
	case *spb.AFTOperation_NextHop:
		log.V(2).Infof("[op %d] attempting to add NH Index %d", op.GetId(), t.NextHop.GetIndex())
		done, _, err := niR.AddNextHop(t.NextHop, explicitReplace)
		switch {
		case err != nil:
			opErr = err
		case done:
			installed = done
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
		// Mark that within this stack we have installed this entry successfully, so
		// we don't retry if it was somewhere further up the stack.
		installStack[op.GetId()] = true
		log.V(2).Infof("operation %d installed in RIB successfully", op.GetId())

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

		// we may now have made some other pending entry be possible to install,
		// so try them all out.
		for _, e := range r.getPending() {
			err := r.addEntryInternal(e.ni, e.op, oks, fails, installStack)
			if err != nil {
				return err
			}
		}
	default:
		r.addPending(op.GetId(), &pendingEntry{
			ni: ni,
			op: op,
		})
	}

	return nil
}

// topLevelEntryProto is an interface implemented by protobuf messages that represent
// an IPv4, MPLS, or IPv6 protobuf.
type topLevelEntryProto interface {
	GetNextHopGroupNetworkInstance() *wpb.StringValue
	GetNextHopGroup() *wpb.UintValue
}

// topLevelEntryStruct is an interface implemented by ygot Go structs tha represent
// an IPv4, IPv6 or MPLS YANG container.
type topLevelEntryStruct interface {
	GetNextHopGroupNetworkInstance() string
	GetNextHopGroup() uint64
}

// isNil safely allows a topLevelEntryStruct to be compared to nil.
func isNil[T any](t T) bool {
	v := reflect.ValueOf(t)
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Map, reflect.Chan, reflect.Func:
		return v.IsNil()
	default:
		return false
	}
}

// handleReferences handles the reference counts for the specified new entry "new" in the RIB r. The
// context niRIB is used as the default VRF RIB for lookup, and the original struct, "orig" is used
// to update any replaced entries. If original is nil, then references are only incremented, otherwise
// replaced references are not adjusted, new references are incremented, and deleted references
// are decremented.
func handleReferences[P topLevelEntryProto, S topLevelEntryStruct](r *RIB, niRIB *RIBHolder, original S, new P) {
	incRefCounts := true
	log.Infof("original was %+v isNil? %v", original, isNil(original))
	newNHGNI := new.GetNextHopGroupNetworkInstance().GetValue()
	newNHG := new.GetNextHopGroup().GetValue()
	if !isNil(original) {
		// This is an entry that was replaced, and hence we need to handle two sets of
		// reference counts, decrementing anything that was deleted, and incrementing
		// anything that was newly referenced.
		origNHGNI := original.GetNextHopGroupNetworkInstance()
		origNHG := original.GetNextHopGroup()

		switch {
		case newNHGNI == origNHGNI && newNHG == origNHG:
			// We are referencing the same entries, so this is a NOOP.
			incRefCounts = false
		case newNHGNI != origNHGNI || newNHG != origNHG:
			// We are no longer referencing the original NHG, so we need to decrement
			// the old references.
			rr, err := r.refdRIB(niRIB, origNHGNI)
			switch err {
			case nil:
				rr.decNHGRefCount(origNHG)
			default:
				log.Errorf("cannot find NHG network instance %s", origNHGNI)
			}
		default:
			// We are referencing new network instances.
		}
	}

	if incRefCounts {
		referencingRIB, err := r.refdRIB(niRIB, newNHGNI)
		switch err {
		case nil:
			log.Infof("incrementing reference for NHG ID %d in %s", newNHG, newNHGNI)
			referencingRIB.incNHGRefCount(newNHG)
		default:
			log.Errorf("cannot find network instance %s", newNHGNI)
		}
	}
}

func (r *RIB) handleNHGReferences(niRIB *RIBHolder, original *aft.Afts_NextHopGroup, new *aftpb.Afts_NextHopGroup) {
	// Increment all the new references.
	for _, nh := range new.NextHop {
		log.Infof("incrementing reference for NH ID %d in %s", nh.GetIndex(), niRIB.name)
		niRIB.incNHRefCount(nh.GetIndex())
	}

	// And decrement all the old references.
	if original != nil {
		for _, nh := range original.NextHop {
			log.Infof("decrementing reference for NH ID %d in %s", nh.GetIndex(), niRIB.name)
			niRIB.decNHRefCount(nh.GetIndex())
		}
	}
}

// callResolvedEntryHook calls the resolvedEntryHook supplying information about the triggering
// operation. Particularky:
//   - the operation is of type optype
//   - it corresponds to the network instance netinst
//   - it is within the aft AFT
//   - it affects the AFT table entry with key value key.
//
// It returns an error if the hook cannot be called. Any error from the hook must be handled externally.
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

// copyRIBs returns a map, keyed by network instance name, with the value of the ygot-generated
// AFT struct, of the set of RIBs stored by the instance r. A DeepCopy of the RIBs is returned,
// along with an error that indicates whether the entries could be copied.
func (r *RIB) copyRIBs() (map[string]*aft.RIB, error) {
	// TODO(robjs): Consider whether we need finer grained locking for each network
	// instance RIB rather than holding the lock whilst we clone the contents.
	r.nrMu.RLock()
	defer r.nrMu.RUnlock()

	rib := map[string]*aft.RIB{}
	for name, niR := range r.niRIB {
		// this is likely expensive on very large RIBs, but with today's implementatiom
		// it seems acceptable, since we then allow the caller not to have to figure out
		// any locking since they have their own RIB to work on.
		dupRIB, err := ygot.DeepCopy(niR.r)
		if err != nil {
			return nil, fmt.Errorf("cannot copy RIB for NI %s, %v", name, err)
		}
		rib[name] = dupRIB.(*aft.RIB)
	}
	return rib, nil
}

// refdRIB returns the RIB for the specified ref -- which may be the current RIB
// if ref is empty, otherwise it is a different RIB on the server. It returns an
// error if it does not exist.
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

// DeleteEntry removes the entry specified by op from the network instance ni.
func (r *RIB) DeleteEntry(ni string, op *spb.AFTOperation) ([]*OpResult, []*OpResult, error) {
	niR, ok := r.NetworkInstanceRIB(ni)
	if !ok || !niR.IsValid() {
		return nil, nil, fmt.Errorf("invalid network instance, %s", ni)
	}

	var (
		oks, fails   []*OpResult
		removed      bool
		err          error
		originalv4   *aft.Afts_Ipv4Entry
		originalv6   *aft.Afts_Ipv6Entry
		originalNHG  *aft.Afts_NextHopGroup
		originalMPLS *aft.Afts_LabelEntry
	)

	if op == nil || op.Entry == nil {
		return nil, nil, status.Newf(codes.InvalidArgument, "invalid nil AFT operation, %v", op).Err()
	}
	switch t := op.Entry.(type) {
	case *spb.AFTOperation_Ipv4:
		log.V(2).Infof("deleting IPv4 prefix %s", t.Ipv4.GetPrefix())
		removed, originalv4, err = niR.DeleteIPv4(t.Ipv4)
	case *spb.AFTOperation_Ipv6:
		log.V(2).Infof("deleting IPv6 prefix %s", t.Ipv6.GetPrefix())
		removed, originalv6, err = niR.DeleteIPv6(t.Ipv6)
	case *spb.AFTOperation_NextHop:
		log.V(2).Infof("deleting NH Index %d", t.NextHop.GetIndex())
		removed, _, err = niR.DeleteNextHop(t.NextHop)
	case *spb.AFTOperation_NextHopGroup:
		log.V(2).Infof("deleting NHG ID %d", t.NextHopGroup.GetId())
		removed, originalNHG, err = niR.DeleteNextHopGroup(t.NextHopGroup)
	case *spb.AFTOperation_Mpls:
		log.V(2).Infof("deleting MPLS entry %s", t.Mpls.GetLabel())
		removed, originalMPLS, err = niR.DeleteMPLS(t.Mpls)
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
		// Decrement the reference counts.
		switch {
		case originalv4 != nil:
			referencingRIB, err := r.refdRIB(niR, originalv4.GetNextHopGroupNetworkInstance())
			if err != nil {
				return nil, nil, err
			}
			log.Infof("decrementing NHG count in VRF %s", originalv4.GetNextHopGroupNetworkInstance())
			log.Infof("decrementing reference count from %+v", referencingRIB.refCounts)
			referencingRIB.decNHGRefCount(originalv4.GetNextHopGroup())
			log.Infof("decrementing reference count to %+v", referencingRIB.refCounts)
			callHook = true
			aft = constants.IPv4
			key = originalv4.GetPrefix()
		case originalv6 != nil:
			referencingRIB, err := r.refdRIB(niR, originalv6.GetNextHopGroupNetworkInstance())
			if err != nil {
				return nil, nil, err
			}
			referencingRIB.decNHGRefCount(originalv6.GetNextHopGroup())
			callHook = true
			aft = constants.IPv6
			key = originalv6.GetPrefix()
		case originalNHG != nil:
			for id := range originalNHG.NextHop {
				log.Infof("decrementing reference count from %+v", niR.refCounts)
				niR.decNHRefCount(id)
				log.Infof("decrementing reference count to %+v", niR.refCounts)
			}
		case originalMPLS != nil:
			referencingRIB, err := r.refdRIB(niR, originalMPLS.GetNextHopGroupNetworkInstance())
			if err != nil {
				return nil, nil, err
			}
			referencingRIB.decNHGRefCount(originalMPLS.GetNextHopGroup())
			callHook = true
			aft = constants.MPLS
			key = originalMPLS.GetLabel()
		}

		log.V(2).Infof("operation %d deleted from RIB successfully", op.GetId())
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

// getPending returns the current set of pending entry operations for the
// RIB receiver.
func (r *RIB) getPending() []*pendingEntry {
	r.pendMu.RLock()
	defer r.pendMu.RUnlock()
	p := []*pendingEntry{}
	for _, e := range r.pendingEntries {
		p = append(p, e)
	}
	return p
}

// addPending adds a pendingEntry with operation ID id to the pending entries
// within the RIB.
func (r *RIB) addPending(id uint64, e *pendingEntry) {
	r.pendMu.Lock()
	defer r.pendMu.Unlock()
	r.pendingEntries[id] = e
}

// rmPending removes the operation with ID id from the RIB's pendingEntries.
func (r *RIB) rmPending(id uint64) {
	r.pendMu.Lock()
	defer r.pendMu.Unlock()
	delete(r.pendingEntries, id)
}

// canResolve takes an input candidate RIB, which contains only the new entry
// being added and determines whether it can be resolved against the existing set
// of RIBs that are stored in r. The specified netInst string is used to
// determine the current network instance within which this entry is being
// considered, such that where the assumption is that a reference is resolved within
// the same network-instance this NI can be used.
//
// canResolve returns a boolean indicating whether the entry
// can be resolved or not.
//
// An entry is defined to be resolved if all its external references within the gRIBI
// RIB can be resolved - particularly (starting from the most specific):
//
//  1. for a next-hop
//     - always consider this valid, since all elements can be resolved outside of
//     gRIBI.
//  2. for a next-hop-group
//     - all the next-hops within the NHG can be resolved
//  3. for an ipv4-entry
//     - the next-hop-group can be resolved
//
// An error is returned if the candidate RIB contains more than one new type.
func (r *RIB) canResolve(netInst string, candidate *aft.RIB) (bool, error) {
	caft := candidate.GetAfts()
	if caft == nil {
		return false, errors.New("invalid nil candidate AFT")
	}

	if err := checkCandidate(caft); err != nil {
		return false, err
	}

	for _, n := range caft.NextHop {
		if n.GetIndex() == 0 {
			return false, fmt.Errorf("invalid index zero for next-hop in NI %s", netInst)
		}
		// we always resolve next-hop entries because they can be resolved outside of gRIBI.
		return true, nil
	}

	// resolve in the default NI if we didn't get asked for a specific NI.
	if netInst == "" {
		netInst = r.defaultName
	}
	niRIB, ok := r.NetworkInstanceRIB(netInst)
	if !ok {
		return false, fmt.Errorf("invalid network-instance %s", netInst)
	}

	for _, g := range caft.NextHopGroup {
		if g.GetId() == 0 {
			return false, fmt.Errorf("invalid zero-index NHG")
		}
		for _, n := range g.NextHop {
			// Zero is an invalid value for a next-hop index. GetIndex() will also return 0
			// if the NH index is nil, which is also invalid - so handle them together.
			if n.GetIndex() == 0 {
				return false, fmt.Errorf("invalid zero index NH in NHG %d, NI %s", g.GetId(), netInst)
			}
			// nexthops are resolved in the same NI as the next-hop-group
			if _, ok := niRIB.GetNextHop(n.GetIndex()); !ok {
				// this is not an error - it's just that we can't resolve this seemingly
				// valid looking NHG at this point.
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
			// again, not an error - we just can't resolve this IPv4 entry due to missing NHG right now.
			return false, nil
		}
		return true, nil
	}

	for _, i := range caft.Ipv4Entry {
		if i.GetNextHopGroup() == 0 {
			// handle zero index again.
			return false, fmt.Errorf("invalid zero-index NHG in IPv4Entry %s, NI %s", i.GetPrefix(), netInst)
		}
		return nhgResolvable(niRIB, i.GetNextHopGroupNetworkInstance(), i.GetNextHopGroup())

	}

	for _, i := range caft.Ipv6Entry {
		if i.GetNextHopGroup() == 0 {
			return false, fmt.Errorf("invalid zero-index NHG in IPv6Entry %s, NI %s", i.GetPrefix(), netInst)
		}
		return nhgResolvable(niRIB, i.GetNextHopGroupNetworkInstance(), i.GetNextHopGroup())
	}

	for _, i := range caft.LabelEntry {
		if i.GetNextHopGroup() == 0 {
			return false, fmt.Errorf("invalid zero index NHG in LabelEntry %v, NI %s", i.GetLabel(), netInst)
		}
		return nhgResolvable(niRIB, i.GetNextHopGroupNetworkInstance(), i.GetNextHopGroup())
	}

	// We should never reach here since we checked that at least one of the things that we are looping over has
	// length >1, but return here too.
	return false, errors.New("no entries in specified candidate")
}

// canDelete takes an input deletionCandidate RIB, which contains only the entry that
// is to be removed from the RIB and determines whether it is safe to remove
// it from the existing set of RIBs that are stored in r. The specified netInst string is
// used to determine the current network instance within which this entry is being
// considered.
//
// canDelete returns a boolean indicating whether the entry can be removed or not
// or an error if the candidate is found to be invalid.
func (r *RIB) canDelete(netInst string, deletionCandidate *aft.RIB) (bool, error) {
	caft := deletionCandidate.GetAfts()
	if caft == nil {
		return false, errors.New("invalid nil candidate AFT")
	}

	if err := checkCandidate(caft); err != nil {
		return false, err
	}

	// Throughout the following code, we know there is a single entry within the
	// candidate RIB, since checkCandidate performs this check.
	//
	// We always check references in the local network instance and resolve in the
	// default NI if we didn't get asked for a specific NI. We check for this before
	// doing the delete to make sure we're working in a valid NI.
	if netInst == "" {
		netInst = r.defaultName
	}
	niRIB, ok := r.NetworkInstanceRIB(netInst)
	if !ok {
		return false, fmt.Errorf("invalid network-instance %s", netInst)
	}

	// IPv4 entries can always be removed, since we allow recursion to happen
	// inside and outside of gRIBI - this is true for MPLS and IPv6.
	if len(caft.Ipv4Entry) != 0 || len(caft.LabelEntry) != 0 || len(caft.Ipv6Entry) != 0 {
		return true, nil
	}

	// Now, we need to check that nothing references a NHG. We could do this naîvely,
	// by walking all RIBs, but this is expensive, so rather we check the refCounter
	// within the RIB instance.
	for id := range caft.NextHopGroup {
		switch {
		case id == 0:
			return false, fmt.Errorf("bad NextHopGroup ID 0")
		case !niRIB.nhgExists(id):
			log.Infof("idempotent delete of NHG ID %d", id)
			return true, nil
		}
		log.Infof("NHG is referenced? %v", niRIB.nhgReferenced(id))
		// if the NHG is not referenced, then we can delete it.
		return !niRIB.nhgReferenced(id), nil
	}

	for idx := range caft.NextHop {
		switch {
		case idx == 0:
			return false, fmt.Errorf("bad NextHop ID 0")
		case !niRIB.nhExists(idx):
			log.Infof("idempotent delete of NH index %d", idx)
			return true, nil
		}
		log.Infof("NH is referenced? %v", niRIB.nhReferenced(idx))
		// again if the NH is not referenced, then we can delete it.
		return !niRIB.nhReferenced(idx), nil
	}

	// We checked that there was 1 entry in the RIB, so we should never reach here,
	// but return an error and keep the compiler happy.
	return false, errors.New("no entries in specified candidate")

}

// checkCandidate checks whether the candidate RIB 'caft' can be processed
// by the RIB implementation. It returns an error if it cannot.
func checkCandidate(caft *aft.Afts) error {
	switch {
	case len(caft.MacEntry) != 0:
		return fmt.Errorf("ethernet MAC entries are unsupported, got: %v", caft.MacEntry)
	case len(caft.PolicyForwardingEntry) != 0:
		return fmt.Errorf("PBR entries are unsupported, got: %v", caft.PolicyForwardingEntry)
	case (len(caft.Ipv6Entry) + len(caft.LabelEntry) + len(caft.Ipv4Entry) + len(caft.NextHopGroup) + len(caft.NextHop)) == 0:
		return errors.New("no entries in specified candidate")
	case (len(caft.Ipv6Entry) + len(caft.LabelEntry) + len(caft.Ipv4Entry) + len(caft.NextHopGroup) + len(caft.NextHop)) > 1:
		return fmt.Errorf("multiple entries are unsupported, got mpls: %v, ipv4: %v, next-hop-group: %v, next-hop: %v", caft.LabelEntry, caft.Ipv4Entry, caft.NextHopGroup, caft.NextHop)
	}
	return nil
}

// ribHolderOpt is an interface implemented by all options that can be provided to the RIBHolder's NewRIBHolder
// function.
type ribHolderOpt interface {
	isRHOpt()
}

// ribHolderCheckFn is a ribHolderOpt that provides a function that can be run for each operation to
// determine whether it should be installed in the RIB.
type ribHolderCheckFn struct {
	fn RIBHolderCheckFunc
}

// isRHOpt implements the ribHolderOpt function
func (r *ribHolderCheckFn) isRHOpt() {}

// RIBHolderCheckFn is an option that provides a function f to be run for each RIB
// change.
func RIBHolderCheckFn(f RIBHolderCheckFunc) *ribHolderCheckFn {
	return &ribHolderCheckFn{fn: f}
}

// hasCheckFn checks whether there is a ribHolderCheckFn option within the supplied
// options.
func hasCheckFn(opts []ribHolderOpt) *ribHolderCheckFn {
	for _, o := range opts {
		if f, ok := o.(*ribHolderCheckFn); ok {
			return f
		}
	}
	return nil
}

// NewRIBHolder returns a new RIB holder for a single network instance.
func NewRIBHolder(name string, opts ...ribHolderOpt) *RIBHolder {
	r := &RIBHolder{
		name: name,
		r: &aft.RIB{
			Afts: &aft.Afts{},
		},
		refCounts: &niRefCounter{
			NextHop:      map[uint64]uint64{},
			NextHopGroup: map[uint64]uint64{},
		},
	}

	fn := hasCheckFn(opts)
	// If there is a check function - regenerate it so that it
	// always operates on the local name.
	if fn != nil {
		checkFn := func(op constants.OpType, r *aft.RIB) (bool, error) {
			return fn.fn(op, name, r)
		}
		r.checkFn = checkFn
	}
	return r
}

// IsValid determines whether the specified RIBHolder is valid to be
// programmed.
func (r *RIBHolder) IsValid() bool {
	// This shows why we need to make the locking on the RIB more granular,
	// since now we're taking a lock just to check whether things are not nil.
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.name == "" || r.r == nil || r.r.Afts == nil {
		return false
	}
	return true
}

// GetNextHop gets the next-hop with the specified index from the RIB
// and returns it. It returns a bool indicating whether the value was
// found.
func (r *RIBHolder) GetNextHop(index uint64) (*aft.Afts_NextHop, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n := r.r.GetAfts().GetNextHop(index)
	if n == nil {
		return nil, false
	}
	return n, true
}

// GetNextHopGroup gets the next-hop-group with the specified ID from the RIB
// and returns it. It returns a bool indicating whether the value was found.
func (r *RIBHolder) GetNextHopGroup(id uint64) (*aft.Afts_NextHopGroup, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n := r.r.GetAfts().GetNextHopGroup(id)
	if n == nil {
		return nil, false
	}
	return n, true
}

// rootSchema returns the schema of the root of the AFT YANG tree.
func rootSchema() (*yang.Entry, error) {
	s, err := aft.Schema()
	if err != nil {
		return nil, fmt.Errorf("cannot get schema, %v", err)
	}
	return s.RootSchema(), nil
}

// candidateRIB takes the input set of Afts and returns them as a aft.RIB pointer
// that can be merged into an existing RIB.
func candidateRIB(a *aftpb.Afts) (*aft.RIB, error) {
	paths, err := protomap.PathsFromProto(a)
	if err != nil {
		return nil, err
	}

	nr := &aft.RIB{}
	rs, err := rootSchema()
	if err != nil {
		return nil, err
	}

	for p, v := range paths {
		sv, err := value.FromScalar(v)

		if err != nil {
			ps := p.String()
			if yps, err := ygot.PathToString(p); err == nil {
				ps = yps
			}
			return nil, fmt.Errorf("cannot convert field %s to scalar, %v", ps, sv)
		}
		if err := ytypes.SetNode(rs, nr, p, sv, &ytypes.InitMissingElements{}); err != nil {
			return nil, fmt.Errorf("invalid RIB %s, %v", a, err)
		}
	}

	// We validate against the schema, but not semantically within gRIBI.
	if err := nr.Afts.Validate(&ytypes.LeafrefOptions{
		IgnoreMissingData: true,
		Log:               false,
	}); err != nil {
		return nil, fmt.Errorf("invalid entry provided, %v", err)
	}

	return nr, nil
}

// AddIPv4 adds the IPv4 entry described by e to the RIB. If the explicitReplace
// argument is set to true, the entry is checked for existence before it is replaced
// otherwise, replaces are implicit. It returns a bool that indicates whether the
// entry was installed, a IPv4 entry that represents the replaced entry, and an
// error which can be considered fatal (i.e., there is no future possibility of
// this entry becoming valid).)
func (r *RIBHolder) AddIPv4(e *aftpb.Afts_Ipv4EntryKey, explicitReplace bool) (bool, *aft.Afts_Ipv4Entry, error) {
	if r.r == nil {
		return false, nil, errors.New("invalid RIB structure, nil")
	}

	if e == nil {
		return false, nil, errors.New("nil IPv4 Entry provided")
	}

	// This is a hack, since ygot does not know that the field that we
	// have provided is a list entry, then it doesn't do the right thing. So
	// we just give it the root so that it knows.
	nr, err := candidateRIB(&aftpb.Afts{
		Ipv4Entry: []*aftpb.Afts_Ipv4EntryKey{e},
	})
	if err != nil {
		return false, nil, fmt.Errorf("invalid IPv4Entry, %v", err)
	}

	if explicitReplace && !r.ipv4Exists(e.GetPrefix()) {
		return false, nil, fmt.Errorf("cannot replace IPv4 Entry %s, does not exist", e.GetPrefix())
	}

	var orig *aft.Afts_Ipv4Entry
	// If we are replacing this entry, return the original to allow the caller to handle any
	// refcounting that is required.
	if explicitReplace || r.ipv4Exists(e.GetPrefix()) {
		orig = r.retrieveIPv4(e.GetPrefix())
	}

	if r.checkFn != nil {
		ok, err := r.checkFn(constants.Add, nr)
		if err != nil {
			// This entry can never be installed, so return the error
			// to the caller directly -- signalling to them not to retry.
			return false, nil, err
		}
		if !ok {
			// The checkFn validated the entry and found it to be OK, but
			// indicated that we should not merge it into the RIB because
			// some prerequisite was not satisifed. Based on this, we
			// return false (we didn't install it), but indicate with err == nil
			// that the caller can retry this entry at some later point, and we'll
			// run the checkFn again to see whether it can now be installed.
			return false, nil, nil
		}
	}

	if _, err := r.doAddIPv4(e.GetPrefix(), nr); err != nil {
		return false, nil, err
	}

	// We expect that there is just a single entry here since we are
	// being called based on a single entry, but we loop since we don't
	// know the key.
	if r.postChangeHook != nil {
		for _, ip4 := range nr.Afts.Ipv4Entry {
			r.postChangeHook(constants.Add, unixTS(), r.name, ip4)
		}
	}

	return true, orig, nil
}

// ipv4Exists returns true if the IPv4 prefix exists within the RIBHolder.
func (r *RIBHolder) ipv4Exists(prefix string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.r.GetAfts().Ipv4Entry[prefix]
	return ok
}

// doAddIPv4 adds an IPv4Entry holding the shortest possible lock on the RIB.
// It returns a bool indicating whether this was an implicit replace.
func (r *RIBHolder) doAddIPv4(pfx string, newRIB *aft.RIB) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Sanity check.
	if nhg, nh := len(newRIB.Afts.NextHopGroup), len(newRIB.Afts.NextHop); nhg != 0 || nh != 0 {
		return false, fmt.Errorf("candidate RIB specifies entries other than NextHopGroups, got: %d nhg, %d nh", nhg, nh)
	}

	// Check whether this is an implicit replace.
	_, implicit := r.r.GetAfts().Ipv4Entry[pfx]

	// MergeStructInto doesn't completely replace a list entry if it finds a missing key,
	// so will append the two entries together.
	// We don't use Delete itself because it will deadlock (we already hold the lock).
	delete(r.r.GetAfts().Ipv4Entry, pfx)

	// TODO(robjs): consider what happens if this fails -- we may leave the RIB in
	// an inconsistent state.
	if err := ygot.MergeStructInto(r.r, newRIB); err != nil {
		return false, fmt.Errorf("cannot merge candidate RIB into existing RIB, %v", err)
	}
	return implicit, nil
}

// DeleteIPv4 removes the IPv4 entry e from the RIB. It returns a boolean
// indicating whether the entry has been removed, a copy of the entry that was
// removed  and an error if the message cannot be parsed. Per the gRIBI specification,
// the payload of the entry is not compared.
func (r *RIBHolder) DeleteIPv4(e *aftpb.Afts_Ipv4EntryKey) (bool, *aft.Afts_Ipv4Entry, error) {
	if e == nil {
		return false, nil, errors.New("nil entry provided")
	}

	if r.r == nil {
		return false, nil, errors.New("invalid RIB structure, nil")
	}

	de := r.retrieveIPv4(e.GetPrefix())

	rr := &aft.RIB{}
	rr.GetOrCreateAfts().GetOrCreateIpv4Entry(e.GetPrefix())
	if r.checkFn != nil {
		ok, err := r.checkFn(constants.Delete, rr)
		switch {
		case err != nil:
			// the check told us this was fatal for this entry -> we should return.
			return false, nil, err
		case !ok:
			// otherwise, we just didn't do this operation.
			return false, nil, nil
		}
	}

	r.doDeleteIPv4(e.GetPrefix())

	if r.postChangeHook != nil {
		r.postChangeHook(constants.Delete, unixTS(), r.name, de)
	}

	return true, de, nil
}

// retrieveIPv4 returns the specified IPv4Entry, holding a lock
// on the RIBHolder as it does so.
func (r *RIBHolder) retrieveIPv4(prefix string) *aft.Afts_Ipv4Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.r.Afts.Ipv4Entry[prefix]
}

// doDeleteIPv4 deletes pfx from the IPv4Entry RIB holding the shortest possible lock.
func (r *RIBHolder) doDeleteIPv4(pfx string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.r.Afts.Ipv4Entry, pfx)
}

// locklessDeleteIPv4 removes the next-hop with the specified prefix without
// holding a lock on the RIB. The caller MUST hold the relevant lock. It returns
// an error if the entry cannot be found.
func (r *RIBHolder) locklessDeleteIPv4(prefix string) error {
	de := r.r.Afts.Ipv4Entry[prefix]
	if de == nil {
		return fmt.Errorf("cannot find prefix %s", prefix)
	}

	delete(r.r.Afts.Ipv4Entry, prefix)
	if r.postChangeHook != nil {
		r.postChangeHook(constants.Delete, unixTS(), r.name, de)
	}
	return nil
}

// AddIPv6 adds the IPv6 entry specified by e to the RIB, explicitReplace indicates whether
// the "add" operation that is being performed is actually an explicit replace of a specific
// prefix such that an error can be returned.
func (r *RIBHolder) AddIPv6(e *aftpb.Afts_Ipv6EntryKey, explicitReplace bool) (bool, *aft.Afts_Ipv6Entry, error) {
	if r.r == nil {
		return false, nil, errors.New("invalid RIB structure, nil")
	}

	if e == nil {
		return false, nil, errors.New("nil IPv6 Entry provided")
	}

	nr, err := candidateRIB(&aftpb.Afts{
		Ipv6Entry: []*aftpb.Afts_Ipv6EntryKey{e},
	})
	if err != nil {
		return false, nil, fmt.Errorf("invalid IPv6Entry, %v", err)
	}

	if explicitReplace && !r.ipv6Exists(e.GetPrefix()) {
		return false, nil, fmt.Errorf("cannot replace IPv6 Entry %s, does not exist", e.GetPrefix())
	}

	var orig *aft.Afts_Ipv6Entry
	if r.ipv6Exists(e.GetPrefix()) {
		orig = r.retrieveIPv6(e.GetPrefix())
	}

	if r.checkFn != nil {
		ok, err := r.checkFn(constants.Add, nr)
		if err != nil {
			return false, nil, err
		}
		if !ok {
			return false, nil, nil
		}
	}

	if _, err := r.doAddIPv6(e.GetPrefix(), nr); err != nil {
		return false, nil, err
	}

	if r.postChangeHook != nil {
		for _, ip4 := range nr.Afts.Ipv6Entry {
			r.postChangeHook(constants.Add, unixTS(), r.name, ip4)
		}
	}

	return true, orig, nil
}

// ipv6Exists determines whether the specified prefix exists within the RIB.
func (r *RIBHolder) ipv6Exists(prefix string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.r.GetAfts().Ipv6Entry[prefix]
	return ok
}

// doAddIPv6 implements the addition of the prefix pfx to the RIB using the supplied
// newRIB as the the entries that should be merged into this RIB.
func (r *RIBHolder) doAddIPv6(pfx string, newRIB *aft.RIB) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if nhg, nh := len(newRIB.Afts.NextHopGroup), len(newRIB.Afts.NextHop); nhg != 0 || nh != 0 {
		return false, fmt.Errorf("candidate RIB specifies entries other than NextHopGroups, got: %d nhg, %d nh", nhg, nh)
	}

	_, implicit := r.r.GetAfts().Ipv6Entry[pfx]

	delete(r.r.GetAfts().Ipv6Entry, pfx)

	// TODO(robjs): consider what happens if this fails -- we may leave the RIB in
	// an inconsistent state.
	if err := ygot.MergeStructInto(r.r, newRIB); err != nil {
		return false, fmt.Errorf("cannot merge candidate RIB into existing RIB, %v", err)
	}
	return implicit, nil
}

// DeleteIPv6 deletes the entry specified by e from the RIB, returning the entry that was removed.
func (r *RIBHolder) DeleteIPv6(e *aftpb.Afts_Ipv6EntryKey) (bool, *aft.Afts_Ipv6Entry, error) {
	if e == nil {
		return false, nil, errors.New("nil entry provided")
	}

	if r.r == nil {
		return false, nil, errors.New("invalid RIB structure, nil")
	}

	de := r.retrieveIPv6(e.GetPrefix())

	rr := &aft.RIB{}
	rr.GetOrCreateAfts().GetOrCreateIpv6Entry(e.GetPrefix())
	if r.checkFn != nil {
		ok, err := r.checkFn(constants.Delete, rr)
		switch {
		case err != nil:
			// the check told us this was fatal for this entry -> we should return.
			return false, nil, err
		case !ok:
			// otherwise, we just didn't do this operation.
			return false, nil, nil
		}
	}

	r.doDeleteIPv6(e.GetPrefix())

	if r.postChangeHook != nil {
		r.postChangeHook(constants.Delete, unixTS(), r.name, de)
	}

	return true, de, nil
}

// retrieveIPv6 retrieves the contents of the entry for prefix from the RIB, holding
// the shortest possible lock.
func (r *RIBHolder) retrieveIPv6(prefix string) *aft.Afts_Ipv6Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.r.Afts.Ipv6Entry[prefix]
}

// doDeleteIPv6 deletes the prefix pfx from the RIB, holding the shortest possible lock.
func (r *RIBHolder) doDeleteIPv6(pfx string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.r.Afts.Ipv6Entry, pfx)
}

// locklessDeleteIPv6 deletes the entry for prefix from the RIB, without holding the lock
// caution must be exercised and the lock MUST be held to call this function.
func (r *RIBHolder) locklessDeleteIPv6(prefix string) error {
	de := r.r.Afts.Ipv6Entry[prefix]
	if de == nil {
		return fmt.Errorf("cannot find prefix %s", prefix)
	}

	delete(r.r.Afts.Ipv6Entry, prefix)
	if r.postChangeHook != nil {
		r.postChangeHook(constants.Delete, unixTS(), r.name, de)
	}
	return nil
}

// AddMPLS adds the specified label entry described by e to the RIB. If the
// explicitReplace argument is set to true, it checks whether the entry exists
// before it is replaced, otherwise replaces are implicit. It returns a bool
// which indicates whether the entry was added, a second bool that indicates
// whether the programming was an implicit replace and an error that should be
// considered fatal by the caller (i.e., there is no possibility that this
// entry can become valid and be installed in the future).
func (r *RIBHolder) AddMPLS(e *aftpb.Afts_LabelEntryKey, explicitReplace bool) (bool, *aft.Afts_LabelEntry, error) {
	if r.r == nil {
		return false, nil, errors.New("invalid RIB structure, nil")
	}

	if e == nil {
		return false, nil, errors.New("nil MPLS Entry provided")
	}

	nr, err := candidateRIB(&aftpb.Afts{
		LabelEntry: []*aftpb.Afts_LabelEntryKey{e},
	})
	if err != nil {
		return false, nil, fmt.Errorf("invalid LabelEntry, %v", err)
	}

	if explicitReplace && !r.mplsExists(uint32(e.GetLabelUint64())) {
		return false, nil, fmt.Errorf("cannot replace MPLS Entry %d, does not exist", e.GetLabelUint64())
	}

	var orig *aft.Afts_LabelEntry
	if r.mplsExists(uint32(e.GetLabelUint64())) {
		orig = r.retrieveMPLS(uint32(e.GetLabelUint64()))
	}

	if r.checkFn != nil {
		ok, err := r.checkFn(constants.Add, nr)
		if err != nil {
			// This entry can never be installed, so return the error
			// to the caller directly -- signalling to them not to retry.
			return false, nil, err
		}
		if !ok {
			// The checkFn validated the entry and found it to be OK, but
			// indicated that we should not merge it into the RIB because
			// some prerequisite was not satisifed. Based on this, we
			// return false (we didn't install it), but indicate with err == nil
			// that the caller can retry this entry at some later point, and we'll
			// run the checkFn again to see whether it can now be installed.
			return false, nil, nil
		}
	}

	if _, err := r.doAddMPLS(uint32(e.GetLabelUint64()), nr); err != nil {
		return false, nil, err
	}

	// We expect that there is just a single entry here since we are
	// being called based on a single entry, but we loop since we don't
	// know the key.
	if r.postChangeHook != nil {
		for _, mpls := range nr.Afts.LabelEntry {
			r.postChangeHook(constants.Add, unixTS(), r.name, mpls)
		}
	}

	return true, orig, nil
}

// mplsExists validates whether an entry exists in r for the specified MPLS
// label entry. It returns true if such an entry exists.
func (r *RIBHolder) mplsExists(label uint32) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.r.GetAfts().LabelEntry[aft.UnionUint32(label)]
	return ok
}

// doAddMPLS implements the addition of the label entry specified by label
// with the contents of the newRIB specified. It holds the shortest possible
// lock on the RIB. doMPLS returns a bool indicating whether the update was
// an implicit replace.
func (r *RIBHolder) doAddMPLS(label uint32, newRIB *aft.RIB) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Sanity check.
	if nhg, nh := len(newRIB.Afts.NextHopGroup), len(newRIB.Afts.NextHop); nhg != 0 || nh != 0 {
		return false, fmt.Errorf("candidate RIB specifies entries other than NextHopGroups, got: %d nhg, %d nh", nhg, nh)
	}

	// Check whether this is an implicit replace.
	_, implicit := r.r.GetAfts().LabelEntry[aft.UnionUint32(label)]

	// MergeStructInto doesn't completely replace a list entry if it finds a missing key,
	// so will append the two entries together.
	// We don't use Delete itself because it will deadlock (we already hold the lock).
	delete(r.r.GetAfts().LabelEntry, aft.UnionUint32(label))

	// TODO(robjs): consider what happens if this fails -- we may leave the RIB in
	// an inconsistent state.
	if err := ygot.MergeStructInto(r.r, newRIB); err != nil {
		return false, fmt.Errorf("cannot merge candidate RIB into existing RIB, %v", err)
	}
	return implicit, nil
}

// DeleteMPLS removes the MPLS label entry e from the RIB. It returns a
// boolean indicating whether the entry has been removed, a copy of the entry
// that was removed, and an error if the message cannot be parsed. Per the gRIBI
// specification the payload of the entry is not compared the existing entry
// before deleting it.
func (r *RIBHolder) DeleteMPLS(e *aftpb.Afts_LabelEntryKey) (bool, *aft.Afts_LabelEntry, error) {
	if e == nil {
		return false, nil, errors.New("nil Label entry provided")
	}

	if r.r == nil {
		return false, nil, errors.New("invalid RIB structure, nil")
	}

	if _, ok := e.GetLabel().(*aftpb.Afts_LabelEntryKey_LabelUint64); !ok {
		return false, nil, fmt.Errorf("unsupported label type %T, only uint64 labels are supported, %v", e, e)
	}

	lbl := uint32(e.GetLabelUint64())

	de := r.retrieveMPLS(lbl)

	rr := &aft.RIB{}
	rr.GetOrCreateAfts().GetOrCreateLabelEntry(aft.UnionUint32(lbl))

	if r.checkFn != nil {
		ok, err := r.checkFn(constants.Delete, rr)
		switch {
		case err != nil:
			// the check told us this was a fatal error that cannot be
			// recovered from.
			return false, nil, err
		case !ok:
			// we did not complete this operation, but it can be retried.
			return false, nil, nil
		}
	}

	r.doDeleteMPLS(lbl)

	if r.postChangeHook != nil {
		r.postChangeHook(constants.Delete, unixTS(), r.name, de)
	}

	return true, de, nil
}

// retrieveMPLS returns the MPLS entry specified by label, holding a lock
// on the RIBHolder as it does so.
func (r *RIBHolder) retrieveMPLS(label uint32) *aft.Afts_LabelEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.r.Afts.LabelEntry[aft.UnionUint32(label)]
}

// doDeleteMPLS deletes label from the LabelEntry RIB holding the shortest
// possible lock.
func (r *RIBHolder) doDeleteMPLS(label uint32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.r.Afts.LabelEntry, aft.UnionUint32(label))
}

// locklessDeleteMPLS removes the label forwarding entry with the specified label
// from the RIB, without holding the lock on the AFT. The calling routine
// MUST ensure that it holds the lock to ensure thread-safe operation.
func (r *RIBHolder) locklessDeleteMPLS(label aft.Afts_LabelEntry_Label_Union) error {
	de := r.r.Afts.LabelEntry[label]
	if de == nil {
		return fmt.Errorf("cannot find label %d", label)
	}

	delete(r.r.Afts.LabelEntry, label)
	if r.postChangeHook != nil {
		r.postChangeHook(constants.Delete, unixTS(), r.name, de)
	}
	return nil
}

// DeleteNextHopGroup removes the NextHopGroup entry e from the RIB. It returns a boolean
// indicating whether the entry has been removed, a copy of the next-hop-group that was
// removed and an error if the message cannot be parsed. Per the gRIBI specification, the
// payload of the entry is not compared.
func (r *RIBHolder) DeleteNextHopGroup(e *aftpb.Afts_NextHopGroupKey) (bool, *aft.Afts_NextHopGroup, error) {
	if e == nil {
		return false, nil, errors.New("nil entry provided")
	}

	if r.r == nil {
		return false, nil, errors.New("invalid RIB structure, nil")
	}

	if e.GetId() == 0 {
		return false, nil, errors.New("invalid NHG ID 0")
	}

	de := r.retrieveNHG(e.GetId())

	rr := &aft.RIB{}
	rr.GetOrCreateAfts().GetOrCreateNextHopGroup(e.GetId())
	if r.checkFn != nil {
		ok, err := r.checkFn(constants.Delete, rr)
		switch {
		case err != nil:
			// the check told us this was fatal for this entry -> we should return.
			return false, nil, err
		case !ok:
			// otherwise, we just didn't do this operation.
			return false, nil, nil
		}
	}

	r.doDeleteNHG(e.GetId())

	if r.postChangeHook != nil {
		r.postChangeHook(constants.Delete, unixTS(), r.name, de)
	}

	return true, de, nil
}

// retrieveNHG returns the specified NextHopGroup, holding a lock
// on the RIBHolder as it does so.
func (r *RIBHolder) retrieveNHG(id uint64) *aft.Afts_NextHopGroup {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.r.Afts.NextHopGroup[id]
}

// locklessDeleteNHG removes the next-hop-group with the specified ID without
// holding a lock on the RIB. The caller MUST hold the relevant lock. It returns
// an error if the entry cannot be found.
func (r *RIBHolder) locklessDeleteNHG(id uint64) error {
	de := r.r.Afts.NextHopGroup[id]
	if de == nil {
		return fmt.Errorf("cannot find NHG %d", id)
	}

	// NextHops must be in the same network instance and NHGs.
	for idx := range de.NextHop {
		log.Infof("decrementing reference count for NH %d", idx)
		log.Infof("was %d", r.refCounts.NextHop[idx])
		r.decNHRefCount(idx)
		log.Infof("now %d", r.refCounts.NextHop[idx])
	}

	delete(r.r.Afts.NextHopGroup, id)
	if r.postChangeHook != nil {
		r.postChangeHook(constants.Delete, unixTS(), r.name, de)
	}
	return nil
}

// DeleteNextHop removes the NextHop entry e from the RIB. It returns a boolean
// indicating whether the entry has been removed, a copy of the group that was
// removed and an error if the message cannot be parsed. Per the gRIBI specification,
// the payload of the entry is not compared.
func (r *RIBHolder) DeleteNextHop(e *aftpb.Afts_NextHopKey) (bool, *aft.Afts_NextHop, error) {
	if e == nil {
		return false, nil, errors.New("nil entry provided")
	}

	if r.r == nil {
		return false, nil, errors.New("invalid RIB structure, nil")
	}

	if e.GetIndex() == 0 {
		return false, nil, fmt.Errorf("invalid NH index 0")
	}

	de := r.retrieveNH(e.GetIndex())

	rr := &aft.RIB{}
	rr.GetOrCreateAfts().GetOrCreateNextHop(e.GetIndex())
	if r.checkFn != nil {
		ok, err := r.checkFn(constants.Delete, rr)
		switch {
		case err != nil:
			// the check told us this was fatal for this entry -> we should return.
			return false, nil, err
		case !ok:
			// otherwise, we just didn't do this operation.
			return false, nil, nil
		}
	}
	r.doDeleteNH(e.GetIndex())

	if r.postChangeHook != nil {
		r.postChangeHook(constants.Delete, unixTS(), r.name, de)
	}

	return true, de, nil
}

// retrieveNH returns the specified NextHop, holding a lock
// on the RIBHolder as it does so.
func (r *RIBHolder) retrieveNH(index uint64) *aft.Afts_NextHop {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.r.Afts.NextHop[index]
}

// locklessDeleteNH removes the next-hop with the specified index without
// holding a lock on the RIB. The caller MUST hold the relevant lock. It returns
// an error if the entry cannot be found.
func (r *RIBHolder) locklessDeleteNH(index uint64) error {
	de := r.r.Afts.NextHop[index]
	if de == nil {
		return fmt.Errorf("cannot find NH %d", index)
	}

	delete(r.r.Afts.NextHop, index)
	if r.postChangeHook != nil {
		r.postChangeHook(constants.Delete, unixTS(), r.name, de)
	}
	return nil
}

// doDeleteNHG deletes the NHG with index idx from the NHG AFTm holding the shortest
// possible lock.
func (r *RIBHolder) doDeleteNHG(idx uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.r.Afts.NextHopGroup, idx)
}

// doDeleteNH deletes the NH with ID id from the NH AFT, holding the shortest possible
// lock.
func (r *RIBHolder) doDeleteNH(id uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.r.Afts.NextHop, id)
}

// AddNextHopGroup adds a NextHopGroup e to the RIBHolder receiver. The explicitReplace argument
// determines whether the operation was an explicit replace, in which case an error is returned
// if the entry does not exist. It returns a boolean
// indicating whether the NHG was installed, a second bool indicating whether this was
// a replace. If encounted it returns an error if the group is invalid.
func (r *RIBHolder) AddNextHopGroup(e *aftpb.Afts_NextHopGroupKey, explicitReplace bool) (bool, *aft.Afts_NextHopGroup, error) {
	if r.r == nil {
		return false, nil, errors.New("invalid RIB structure, nil")
	}

	if e == nil {
		return false, nil, errors.New("nil NextHopGroup provided")
	}

	nr, err := candidateRIB(&aftpb.Afts{
		NextHopGroup: []*aftpb.Afts_NextHopGroupKey{e},
	})
	if err != nil {
		return false, nil, fmt.Errorf("invalid NextHopGroup, %v", err)
	}

	if explicitReplace && !r.nhgExists(e.GetId()) {
		return false, nil, fmt.Errorf("cannot replace NextHopGroup %d, does not exist", e.GetId())
	}

	var orig *aft.Afts_NextHopGroup
	if r.nhgExists(e.GetId()) {
		orig = r.retrieveNHG(e.GetId())
	}

	if r.checkFn != nil {
		ok, err := r.checkFn(constants.Add, nr)
		if err != nil {
			// Entry can never be installed (see the documentation in
			// the AddIPv4 function for additional details).
			return false, nil, err
		}
		if !ok {
			log.Infof("NextHopGroup %d added to pending queue - not installed", e.GetId())
			// Entry is not valid for installation right now.
			return false, nil, nil
		}
	}

	if _, err := r.doAddNHG(e.GetId(), nr); err != nil {
		return false, nil, err
	}

	if r.postChangeHook != nil {
		for _, nhg := range nr.Afts.NextHopGroup {
			r.postChangeHook(constants.Add, unixTS(), r.name, nhg)
		}
	}

	return true, orig, nil
}

// nhgExists returns true if the NHG with ID id exists in the RIBHolder.
func (r *RIBHolder) nhgExists(id uint64) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.r.GetAfts().NextHopGroup[id]
	return ok
}

// doAddNHG adds a NHG holding the shortest possible lock on the RIB to avoid
// deadlocking. It returns a boolean indicating whether this was a replace.
func (r *RIBHolder) doAddNHG(ID uint64, newRIB *aft.RIB) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Sanity check.
	if ip4, nh := len(newRIB.Afts.Ipv4Entry), len(newRIB.Afts.NextHop); ip4 != 0 || nh != 0 {
		return false, fmt.Errorf("candidate RIB specifies entries other than NextHopGroups, got: %d ipv4, %d nh", ip4, nh)
	}

	_, wasReplace := r.r.GetAfts().NextHopGroup[ID]

	// Handle implicit replace.
	delete(r.r.GetAfts().NextHopGroup, ID)

	if err := ygot.MergeStructInto(r.r, newRIB); err != nil {
		return false, fmt.Errorf("cannot merge candidate RIB into existing RIB, %v", err)
	}
	return wasReplace, nil
}

// incNHGRefCount increments the reference count for the specified next-hop-group.
func (r *RIBHolder) incNHGRefCount(i uint64) {
	r.refCounts.mu.Lock()
	defer r.refCounts.mu.Unlock()
	r.refCounts.NextHopGroup[i]++
}

// decNHGRefCount decrements the reference count for the specified next-hop-group.
func (r *RIBHolder) decNHGRefCount(i uint64) {
	r.refCounts.mu.Lock()
	defer r.refCounts.mu.Unlock()
	if r.refCounts.NextHopGroup[i] == 0 {
		// prevent the refcount from rolling back - this is an error, since it
		// means the implementation did not add references correctly.
		return
	}
	r.refCounts.NextHopGroup[i]--
}

// nhgReferenced indicates whether the next-hop-group has a refCount > 0.
func (r *RIBHolder) nhgReferenced(i uint64) bool {
	r.refCounts.mu.RLock()
	defer r.refCounts.mu.RUnlock()
	return r.refCounts.NextHopGroup[i] > 0
}

// AddNextHop adds a new NextHop e to the RIBHolder receiver. If the explicitReplace
// argument is set to true, AddNextHop verifies that the entry exists within the
// RIB before replacing it, otherwise replaces are implicit. It returns a boolean
// indicating whether the NextHop was installed, along with a second boolean that
// indicates whether this was an implicit replace. If encountered, it returns an error
// if the group is invalid.
func (r *RIBHolder) AddNextHop(e *aftpb.Afts_NextHopKey, explicitReplace bool) (bool, *aft.Afts_NextHop, error) {
	if r.r == nil {
		return false, nil, errors.New("invalid RIB structure, nil")
	}

	if e == nil {
		return false, nil, errors.New("nil NextHop provided")
	}

	nr, err := candidateRIB(&aftpb.Afts{
		NextHop: []*aftpb.Afts_NextHopKey{e},
	})
	if err != nil {
		return false, nil, fmt.Errorf("invalid NextHopGroup, %v", err)
	}

	if explicitReplace && !r.nhExists(e.GetIndex()) {
		return false, nil, fmt.Errorf("cannot replace NextHop %d, does not exist", e.GetIndex())
	}

	var replaced *aft.Afts_NextHop
	if r.nhExists(e.GetIndex()) {
		replaced = r.retrieveNH(e.GetIndex())
	}

	if r.checkFn != nil {
		ok, err := r.checkFn(constants.Add, nr)
		if err != nil {
			// Entry can never be installed (see the documentation in
			// the AddIPv4 function for additional details).
			return false, nil, err
		}
		if !ok {
			// Entry is not valid for installation right now.
			return false, nil, nil
		}
	}

	if _, err := r.doAddNH(e.GetIndex(), nr); err != nil {
		return false, nil, err
	}

	if r.postChangeHook != nil {
		for _, nh := range nr.Afts.NextHop {
			r.postChangeHook(constants.Add, unixTS(), r.name, nh)
		}
	}

	return true, replaced, nil
}

// nhExists returns true if the next-hop with index index exists within the RIBHolder.
func (r *RIBHolder) nhExists(index uint64) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.r.GetAfts().NextHop[index]
	return ok
}

// doAddNH adds a NH holding the shortest possible lock on the RIB to avoid
// deadlocking. It returns a boolean indicating whether the add was an implicit
// replace.
func (r *RIBHolder) doAddNH(index uint64, newRIB *aft.RIB) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Sanity check.
	if ip4, nhg := len(newRIB.Afts.Ipv4Entry), len(newRIB.Afts.NextHopGroup); ip4 != 0 || nhg != 0 {
		return false, fmt.Errorf("candidate RIB specifies entries other than NextHopGroups, got: %d ipv4, %d nhg", ip4, nhg)
	}

	_, implicit := r.r.GetAfts().NextHop[index]

	// Handle implicit replace.
	delete(r.r.GetAfts().NextHop, index)

	if err := ygot.MergeStructInto(r.r, newRIB); err != nil {
		return false, fmt.Errorf("cannot merge candidate RIB into existing RIB, %v", err)
	}
	return implicit, nil
}

// incNHRefCount increments the reference count for the specified next-hop-group.
func (r *RIBHolder) incNHRefCount(i uint64) {
	r.refCounts.mu.Lock()
	defer r.refCounts.mu.Unlock()
	r.refCounts.NextHop[i]++
}

// decNHRefCount decrements the reference count for the specified next-hop-group.
func (r *RIBHolder) decNHRefCount(i uint64) {
	r.refCounts.mu.Lock()
	defer r.refCounts.mu.Unlock()
	if r.refCounts.NextHop[i] == 0 {
		// prevent the refcount from rolling back - this is an error, since it
		// means the implementation did not add references correctly.
		return
	}
	r.refCounts.NextHop[i]--
}

// nhReferenced indicates whether the next-hop-group has a refCount > 0.
func (r *RIBHolder) nhReferenced(i uint64) bool {
	r.refCounts.mu.RLock()
	defer r.refCounts.mu.RUnlock()
	return r.refCounts.NextHop[i] > 0
}

// ConcreteIPv4Proto takes the input Ipv4Entry GoStruct and returns it as a gRIBI
// Ipv4EntryKey protobuf. It returns an error if the protobuf cannot be marshalled.
func ConcreteIPv4Proto(e *aft.Afts_Ipv4Entry) (*aftpb.Afts_Ipv4EntryKey, error) {
	ip4proto := &aftpb.Afts_Ipv4Entry{}
	if err := protoFromGoStruct(e, &gpb.Path{
		Elem: []*gpb.PathElem{{
			Name: "afts",
		}, {
			Name: "ipv4-unicast",
		}, {
			Name: "ipv4-entry",
		}},
	}, ip4proto); err != nil {
		return nil, fmt.Errorf("cannot marshal IPv4 prefix %s, %v", e.GetPrefix(), err)
	}
	return &aftpb.Afts_Ipv4EntryKey{
		Prefix:    *e.Prefix,
		Ipv4Entry: ip4proto,
	}, nil
}

// ConcreteIPv6Proto takes the input Ipv6Entry GoStruct and returns it as a gRIBI
// Ipv6EntryKey protobuf. It returns an error if the protobuf cannot be marshalled.
func ConcreteIPv6Proto(e *aft.Afts_Ipv6Entry) (*aftpb.Afts_Ipv6EntryKey, error) {
	ip6proto := &aftpb.Afts_Ipv6Entry{}
	if err := protoFromGoStruct(e, &gpb.Path{
		Elem: []*gpb.PathElem{{
			Name: "afts",
		}, {
			Name: "ipv6-unicast",
		}, {
			Name: "ipv6-entry",
		}},
	}, ip6proto); err != nil {
		return nil, fmt.Errorf("cannot marshal IPv6 prefix %s, %v", e.GetPrefix(), err)
	}
	return &aftpb.Afts_Ipv6EntryKey{
		Prefix:    *e.Prefix,
		Ipv6Entry: ip6proto,
	}, nil
}

// ConcreteMPLSProto takes the input LabelEntry GoStruct and returns it as a gRIBI
// LabelEntryKey protobuf. It returns an error if the protobuf cannot be marshalled.
func ConcreteMPLSProto(e *aft.Afts_LabelEntry) (*aftpb.Afts_LabelEntryKey, error) {
	mplsProto := &aftpb.Afts_LabelEntry{}
	if err := protoFromGoStruct(e, &gpb.Path{
		Elem: []*gpb.PathElem{{
			Name: "afts",
		}, {
			Name: "mpls",
		}, {
			Name: "label-entry",
		}},
	}, mplsProto); err != nil {
		return nil, fmt.Errorf("cannot marshal MPLS label %v, %v", e.GetLabel(), err)
	}

	l, ok := e.GetLabel().(aft.UnionUint32)
	if !ok {
		return nil, fmt.Errorf("cannot marshal MPLS label %v, incorrect type %T", e.GetLabel(), e.GetLabel())
	}

	return &aftpb.Afts_LabelEntryKey{
		Label: &aftpb.Afts_LabelEntryKey_LabelUint64{
			LabelUint64: uint64(l),
		},
		LabelEntry: mplsProto,
	}, nil
}

// ConcreteNextHopProto takes the input NextHop GoStruct and returns it as a gRIBI
// NextHopEntryKey protobuf. It returns an error if the protobuf cannot be marshalled.
func ConcreteNextHopProto(e *aft.Afts_NextHop) (*aftpb.Afts_NextHopKey, error) {
	nhproto := &aftpb.Afts_NextHop{}
	if err := protoFromGoStruct(e, &gpb.Path{
		Elem: []*gpb.PathElem{{
			Name: "afts",
		}, {
			Name: "next-hops",
		}, {
			Name: "next-hop",
		}},
	}, nhproto); err != nil {
		return nil, fmt.Errorf("cannot marshal next-hop index %d, %v", e.GetIndex(), err)
	}
	return &aftpb.Afts_NextHopKey{
		Index:   *e.Index,
		NextHop: nhproto,
	}, nil
}

// ConcreteNextHopGroupProto takes the input NextHopGroup GoStruct and returns it as a gRIBI
// NextHopGroupEntryKey protobuf. It returns an error if the protobuf cannot be marshalled.
func ConcreteNextHopGroupProto(e *aft.Afts_NextHopGroup) (*aftpb.Afts_NextHopGroupKey, error) {
	nhgproto := &aftpb.Afts_NextHopGroup{}
	if err := protoFromGoStruct(e, &gpb.Path{
		Elem: []*gpb.PathElem{{
			Name: "afts",
		}, {
			Name: "next-hop-groups",
		}, {
			Name: "next-hop-group",
		}},
	}, nhgproto); err != nil {
		return nil, fmt.Errorf("cannot marshal next-hop index %d, %v", e.GetId(), err)
	}
	return &aftpb.Afts_NextHopGroupKey{
		Id:           *e.Id,
		NextHopGroup: nhgproto,
	}, nil
}

// protoFromGoStruct takes the input GoStruct and marshals into the supplied pb
// protobuf message, trimming the prefix specified from the annotated paths within
// the protobuf.
func protoFromGoStruct(s ygot.ValidatedGoStruct, prefix *gpb.Path, pb proto.Message) error {
	ns, err := ygot.TogNMINotifications(s, 0, ygot.GNMINotificationsConfig{
		UsePathElem: true,
	})
	if err != nil {
		return fmt.Errorf("cannot marshal existing entry key %s, %v", s, err)
	}

	vals := map[*gpb.Path]interface{}{}
	for _, n := range ns {
		for _, u := range n.GetUpdate() {
			vals[u.Path] = u.Val
		}
	}

	if err := protomap.ProtoFromPaths(pb, vals,
		protomap.ProtobufMessagePrefix(prefix),
		protomap.ValuePathPrefix(prefix),
		protomap.IgnoreExtraPaths(),
	); err != nil {
		return fmt.Errorf("cannot unmarshal gNMI paths, %v", err)
	}

	return nil
}

// GetRIB writes the contents of the RIBs specified in the filter to msgCh. filter is a map,
// keyed by the gRIBI AFTType enumeration, if the value is set to true, the AFT is written
// to msgCh, otherwise it is skipped. The contents of the RIB are returned as gRIBI
// GetResponse messages which are written to the supplied msgCh. stopCh is a channel that
// indicates that the GetRIB method should stop its work and return immediately.
//
// An error is returned if the RIB cannot be returned.
func (r *RIBHolder) GetRIB(filter map[spb.AFTType]bool, msgCh chan *spb.GetResponse, stopCh chan struct{}) error {
	// TODO(robjs): since we are wanting to ensure that we tell the client
	// exactly what is installed, this leads to a decision to make about locking
	// of the RIB -- either we can go and lock the entire network instance RIB,
	// or be more granular than that.
	//
	//  * we take the NI-level lock: in the incoming master case, the client can
	//    ensure that they wait for the Get to complete before writing ==> there
	//    is no convergence impact. In the multi-master case (or even a consistency)
	//    check case, we impact convergence.
	//  * we take a more granular lock, in this case we do not impact convergence
	//    for any other entity than that individual entry.
	//
	// The latter is a better choice for a high-performance implementation, but
	// its not clear that we need to worry about this for this implementation *yet*.
	// In the future we should consider a fine-grained per-entry lock.
	r.mu.RLock()
	defer r.mu.RUnlock()

	// rewrite ALL to the values that we support.
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
		for pfx, e := range r.r.Afts.Ipv4Entry {
			select {
			case <-stopCh:
				return nil
			default:
				p, err := ConcreteIPv4Proto(e)
				if err != nil {
					return status.Errorf(codes.Internal, "cannot marshal IPv4Entry for %s into GetResponse, %v", pfx, err)
				}
				msgCh <- &spb.GetResponse{
					Entry: []*spb.AFTEntry{{
						NetworkInstance: r.name,
						Entry: &spb.AFTEntry_Ipv4{
							Ipv4: p,
						},
					}},
				}
			}
		}
	}

	if filter[spb.AFTType_IPV6] {
		for pfx, e := range r.r.Afts.Ipv6Entry {
			select {
			case <-stopCh:
				return nil
			default:
				p, err := ConcreteIPv6Proto(e)
				if err != nil {
					return status.Errorf(codes.Internal, "cannot marshal IPv6Entry for %s into GetResponse, %v", pfx, err)
				}
				msgCh <- &spb.GetResponse{
					Entry: []*spb.AFTEntry{{
						NetworkInstance: r.name,
						Entry: &spb.AFTEntry_Ipv6{
							Ipv6: p,
						},
					}},
				}
			}
		}
	}

	if filter[spb.AFTType_MPLS] {
		for lbl, e := range r.r.Afts.LabelEntry {
			select {
			case <-stopCh:
				return nil
			default:
				p, err := ConcreteMPLSProto(e)
				if err != nil {
					return status.Errorf(codes.Internal, "cannot marshal MPLS entry for label %d into GetResponse, %v", lbl, err)
				}
				msgCh <- &spb.GetResponse{
					Entry: []*spb.AFTEntry{{
						NetworkInstance: r.name,
						Entry: &spb.AFTEntry_Mpls{
							Mpls: p,
						},
					}},
				}
			}
		}
	}

	if filter[spb.AFTType_NEXTHOP_GROUP] {
		for index, e := range r.r.Afts.NextHopGroup {
			select {
			case <-stopCh:
				return nil
			default:
				p, err := ConcreteNextHopGroupProto(e)
				if err != nil {
					return status.Errorf(codes.Internal, "cannot marshal NextHopGroupEntry for index %d into GetResponse, %v", index, err)
				}
				msgCh <- &spb.GetResponse{
					Entry: []*spb.AFTEntry{{
						NetworkInstance: r.name,
						Entry: &spb.AFTEntry_NextHopGroup{
							NextHopGroup: p,
						},
					}},
				}
			}
		}
	}

	if filter[spb.AFTType_NEXTHOP] {
		for id, e := range r.r.Afts.NextHop {
			select {
			case <-stopCh:
				return nil
			default:
				p, err := ConcreteNextHopProto(e)
				if err != nil {
					return status.Errorf(codes.Internal, "cannot marshal NextHopEntry for ID %d into GetResponse, %v", id, err)
				}
				msgCh <- &spb.GetResponse{
					Entry: []*spb.AFTEntry{{
						NetworkInstance: r.name,
						Entry: &spb.AFTEntry_NextHop{
							NextHop: p,
						},
					}},
				}
			}
		}
	}

	return nil
}

type FlushErr struct {
	Errs []error
}

func (f *FlushErr) Error() string {
	b := &bytes.Buffer{}
	for _, err := range f.Errs {
		b.WriteString(fmt.Sprintf("%s\n", err))
	}
	return b.String()
}

// Flush cleanly removes all entries from the specified RIB. A lock on the RIB
// is held throughout the flush so that no entries can be added during this
// time.
//
// The order of operations for deletes considers the dependency tree:
//   - we remove IPv4 entries first, since these are never referenced counted,
//     and the order of removing them never matters.
//   - we check for any backup NHGs, and remove these first - since otherwise we
//     may end up with referenced NHGs. note, we need to consider circular references
//     of backup NHGs, which we may allow today. we remove the backup NHGs.
//   - we remove the remaining NHGs.
//   - we remove the NHs.
//
// Flush handles updating the reference counts within the RIB.
func (r *RIB) Flush(networkInstances []string) error {
	errs := []error{}

	for _, netInst := range networkInstances {
		niR, ok := r.NetworkInstanceRIB(netInst)
		if !ok {
			log.Errorf("cannot find network instance RIB for %s", netInst)
		}

		// We hold a long lock during the Flush operation since we need to ensure that
		// no entries are added to it whilst we remove all entries. This also means
		// that we use the locklessDeleteXXX functions below to avoid deadlocking.
		niR.mu.Lock()
		defer niR.mu.Unlock()

		for p, entry := range niR.r.Afts.Ipv4Entry {
			referencedRIB, err := r.refdRIB(niR, entry.GetNextHopGroupNetworkInstance())
			switch {
			case err != nil:
				log.Errorf("cannot find network instance RIB %s during Flush for IPv4 prefix %s", entry.GetNextHopGroupNetworkInstance(), p)
			default:
				log.Infof("decrementing reference in VRF %s", entry.GetNextHopGroupNetworkInstance())
				referencedRIB.decNHGRefCount(entry.GetNextHopGroup())
			}
			if err := niR.locklessDeleteIPv4(p); err != nil {
				errs = append(errs, err)
			}
		}

		for p, entry := range niR.r.Afts.Ipv6Entry {
			referencedRIB, err := r.refdRIB(niR, entry.GetNextHopGroupNetworkInstance())
			switch {
			case err != nil:
				log.Errorf("cannot find network instance RIB %s during Flush for IPv6 prefix %s", entry.GetNextHopGroupNetworkInstance(), p)
			default:
				referencedRIB.decNHGRefCount(entry.GetNextHopGroup())
			}
			if err := niR.locklessDeleteIPv6(p); err != nil {
				errs = append(errs, err)
			}
		}

		for label, entry := range niR.r.Afts.LabelEntry {
			referencedRIB, err := r.refdRIB(niR, entry.GetNextHopGroupNetworkInstance())
			switch {
			case err != nil:
				log.Errorf("cannot find network instance RIB %s during Flush for MPLS label %d", entry.GetNextHopGroupNetworkInstance(), label)
			default:
				referencedRIB.decNHGRefCount(entry.GetNextHopGroup())
			}
			if err := niR.locklessDeleteMPLS(label); err != nil {
				errs = append(errs, err)
			}
		}

		backupNHGs := []uint64{}
		for _, nhg := range niR.r.Afts.NextHopGroup {
			if nhg.BackupNextHopGroup != nil {
				backupNHGs = append(backupNHGs, *nhg.BackupNextHopGroup)
			}
		}

		delNHG := func(id uint64) {
			if err := niR.locklessDeleteNHG(id); err != nil {
				errs = append(errs, err)
			}
		}

		for _, id := range backupNHGs {
			delNHG(id)
		}

		for n := range niR.r.Afts.NextHopGroup {
			delNHG(n)
		}

		for n := range niR.r.Afts.NextHop {
			if err := niR.locklessDeleteNH(n); err != nil {
				errs = append(errs, err)
			}
		}

	}

	for niN, ni := range r.niRIB {
		log.Infof("after flush, refCounts in %s are %v", niN, pretty.Sprint(ni.refCounts))
	}

	if len(errs) != 0 {
		return &FlushErr{Errs: errs}
	}

	return nil
}

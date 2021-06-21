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

// Package RIB implements a basic RIB for a gRIBI server.
package rib

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	log "github.com/golang/glog"
	"github.com/openconfig/gnmi/value"
	"github.com/openconfig/goyang/pkg/yang"
	"github.com/openconfig/gribigo/aft"
	"github.com/openconfig/gribigo/constants"
	"github.com/openconfig/ygot/protomap"
	"github.com/openconfig/ygot/ygot"
	"github.com/openconfig/ygot/ytypes"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	aftpb "github.com/openconfig/gribi/v1/proto/gribi_aft"
	spb "github.com/openconfig/gribi/v1/proto/service"
)

// unixTS is used to determine the current unix timestamp in nanoseconds since the
// epoch. It is defined such that it can be overloaded by unit tests.
var unixTS = time.Now().UnixNano

// RIBHookFn is a function that is used as a hook following a change. It takes:
//  - an OpType deterining whether an add, remove, or modify operation was sent.
//  - the timestamp in nanoseconds since the unix epoch that a function was performed.
//  - a string indicating the name of the network instance
//  - a ygot.GoStruct containing the entry that has been changed.
type RIBHookFn func(constants.OpType, int64, string, ygot.GoStruct)

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

	// TODO(robjs): reference count NHGs and NHs across all AFTs to ensure that we
	// don't allow entries to be deleted that are in use.

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
	// considered valid candidates to be merged. It can be used to check that
	// an entry is resolvable. The argument handed to it is a candidate RIB
	// as described by an aft.RIB structure. It returns a boolean indicating
	// whether the entry should be installed, or an error indicating that the
	// entry is not valid for installation.
	//
	// When checkFn returns false, but no error is returned, it is expected
	// that the client of the RIB can retry to install this entry at a later
	// point in time. If an error is returned, the checkFn is asserting that
	// there is no way that this entry can ever be installed in the RIB,
	// regardless of whether there are state changes.
	checkFn func(a *aft.RIB) (bool, error)

	// postChangeHook is a function that is called after each of the operations
	// within the RIB completes, it takes arguments of the
	//   - name of the network instance
	// 	 - operation type (as an constants.OpType enumerated value)
	//	 - the changed entry as a ygot.GoStruct.
	postChangeHook RIBHookFn
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
		rhOpt = append(rhOpt, RIBHolderCheckFn(r.canResolve))
	}
	r.ribCheck = checkRIB

	r.niRIB[dn] = NewRIBHolder(dn, rhOpt...)

	return r
}

// pendingEntry describes an operation that is pending on the gRIBI server. Generally,
// this is due to RIB recursion lookup failures.
type pendingEntry struct {
	// ni is the network instance the operation is operating on.
	ni string
	// op is the AFTOperation that is being performed.
	op *spb.AFTOperation
}

// SetHook assigns the supplied hook to all network instance RIBs within
// the RIB structure.
func (r *RIB) SetHook(fn RIBHookFn) {
	for _, nir := range r.niRIB {
		nir.postChangeHook = fn
	}
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
		rhOpt = append(rhOpt, RIBHolderCheckFn(r.canResolve))
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

// AddEntry adds the entry described in op to the network instance with name ni. It returns
// two slices of OpResults:
//  - the first ("oks") describes the set of entries that were installed successfully based on
//    this operation.
//  - the second ("fails") describes the set of entries that were NOT installed, and encountered
//    fatal errors during the process of installing the entry.
//
// It returns an error if there is a fatal error encountered for the function during operation.
//
// The oks slice may have length > 1 (i.e., not just be the input operation) in the case an entry
// becomes resolvable (per canResolve) *after* this operation has been installed. It will recursively
// call the internal implementation in order to install all entries that are now resolvable based
// on the operation provided.
func (r *RIB) AddEntry(ni string, op *spb.AFTOperation) ([]*OpResult, []*OpResult, error) {
	oks, fails := []*OpResult{}, []*OpResult{}
	checked := map[uint64]bool{}
	if err := r.addEntryInternal(ni, op, &oks, &fails, checked); err != nil {
		return nil, nil, err
	}
	return oks, fails, nil
}

// addEntryInternal is the internal implementation of AddEntry. It takes arguments of:
//  - the name of the network instance being operated on (ni) by the operation op.
//  - a slice of installed results, which is appended to.
//  - a slice of failed results, which is appended to.
//  - a map, keyed by operation ID, describing the stack of calls that we have currently
//    done during this recursion so that we do not repeat an install operation.
func (r *RIB) addEntryInternal(ni string, op *spb.AFTOperation, oks, fails *[]*OpResult, installStack map[uint64]bool) error {
	if installStack[op.GetId()] {
		return nil
	}
	niR, ok := r.NetworkInstanceRIB(ni)
	if !ok || !niR.IsValid() {
		return fmt.Errorf("invalid network instance, %s", ni)
	}

	var installed bool
	var err error
	switch t := op.Entry.(type) {
	case *spb.AFTOperation_Ipv4:
		log.V(2).Infof("adding IPv4 prefix %s", t.Ipv4.GetPrefix())
		installed, err = niR.AddIPv4(t.Ipv4)
	case *spb.AFTOperation_NextHop:
		log.V(2).Infof("adding NH Index %d", t.NextHop.GetIndex())
		installed, err = niR.AddNextHop(t.NextHop)
	case *spb.AFTOperation_NextHopGroup:
		log.V(2).Infof("adding NHG ID %d", t.NextHopGroup.GetId())
		installed, err = niR.AddNextHopGroup(t.NextHopGroup)
	default:
		return status.Newf(codes.Unimplemented, "unsupported AFT operation type %T", t).Err()
	}

	switch {
	case err != nil:
		*fails = append(*fails, &OpResult{
			ID:    op.GetId(),
			Op:    op,
			Error: err.Error(),
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
		// we may now have made some other pending entry be possible to install,
		// so try them all out!
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
//   * for a next-hop
//       - always consider this valid, since all elements can be resolved outside of
//         gRIBI.
//   * for a next-hop-group
//       - all the next-hops within the NHG can be resolved
//   * for an ipv4-entry
//       - the next-hop-group can be resolved
//
// An error is returned if the candidate RIB contains more than one new type.
func (r *RIB) canResolve(netInst string, candidate *aft.RIB) (bool, error) {
	caft := candidate.GetAfts()
	if caft == nil {
		return false, errors.New("invalid nil candidate AFT")
	}
	switch {
	case len(caft.Ipv6Entry) != 0:
		return false, fmt.Errorf("IPv6 entries are unsupported, got: %v", caft.Ipv6Entry)
	case len(caft.LabelEntry) != 0:
		return false, fmt.Errorf("MPLS label entries are unsupported, got: %v", caft.LabelEntry)
	case len(caft.MacEntry) != 0:
		return false, fmt.Errorf("ethernet MAC entries are unsupported, got: %v", caft.MacEntry)
	case len(caft.PolicyForwardingEntry) != 0:
		return false, fmt.Errorf("PBR entries are unsupported, got: %v", caft.PolicyForwardingEntry)
	case (len(caft.Ipv4Entry) + len(caft.NextHopGroup) + len(caft.NextHop)) == 0:
		return false, errors.New("no entries in specified candidate")
	case (len(caft.Ipv4Entry) + len(caft.NextHopGroup) + len(caft.NextHop)) > 1:
		return false, fmt.Errorf("multiple entries are unsupported, got ipv4: %v, next-hop-group: %v, next-hop: %v", caft.Ipv4Entry, caft.NextHopGroup, caft.NextHop)
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

	for _, i := range caft.Ipv4Entry {
		if i.GetNextHopGroup() == 0 {
			// handle zero index again.
			return false, fmt.Errorf("invalid zero-index NHG in IPv4Entry %s, NI %s", i.GetPrefix(), netInst)
		}
		resolveRIB := niRIB
		if otherNI := i.GetNextHopGroupNetworkInstance(); otherNI != "" {
			resolveRIB, ok = r.NetworkInstanceRIB(otherNI)
			if !ok {
				return false, fmt.Errorf("invalid unknown network-instance for IPv4Entry, %s", otherNI)
			}
		}
		if _, ok := resolveRIB.GetNextHopGroup(i.GetNextHopGroup()); !ok {
			// again, not an error - we just can't resolve this IPv4 entry due to missing NHG right now.
			return false, nil
		}
		return true, nil
	}

	// We should never reach here since we checked that at least one of the things that we are looping over has
	// length >1, but return here too.
	return false, errors.New("no entries in specified candidate")
}

// ribHolderOpt is an interface implemented by all options that can be provided to the RIBHolder's NewRIBHolder
// function.
type ribHolderOpt interface {
	isRHOpt()
}

// ribHolderCheckFn is a ribHolderOpt that provides a function that can be run for each operation to
// determine whether it should be installed in the RIB.
type ribHolderCheckFn struct {
	fn func(string, *aft.RIB) (bool, error)
}

// isRHOpt implements the ribHolderOpt function
func (r *ribHolderCheckFn) isRHOpt() {}

// RIBHolderCheckFn is an option that provides a function f - taking arguments of:
//  * a network instance name as a string
//  * a candidate RIB as an aft.RIB GoStruct
// It must return:
//  * a bool indicating whether the RIB operation should go ahead.
//  * an error that is considered fatal for the entry (i.e., this entry should never
//    be tried again).
//
// TODO(robjs): consider whether this should take an optype too - so that the same checkfn
// can be used when we do deletes for refcounting.
func RIBHolderCheckFn(f func(string, *aft.RIB) (bool, error)) *ribHolderCheckFn {
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
	}

	fn := hasCheckFn(opts)
	// If there is a check function - regenerate it so that it
	// always operates on the local name.
	if fn != nil {
		checkFn := func(r *aft.RIB) (bool, error) {
			return fn.fn(name, r)
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

// AddIPv4 adds the IPv4 entry described by e to the RIB. It returns a bool
// which indicates whether the entry was added, and an error which can be
// considered fatal (i.e., there is no future possibility of this entry
// becoming valid).
func (r *RIBHolder) AddIPv4(e *aftpb.Afts_Ipv4EntryKey) (bool, error) {
	if r.r == nil {
		return false, errors.New("invalid RIB structure, nil")
	}

	if e == nil {
		return false, errors.New("nil IPv4 Entry provided")
	}

	// This is a hack, since ygot does not know that the field that we
	// have provided is a list entry, then it doesn't do the right thing. So
	// we just give it the root so that it knows.
	nr, err := candidateRIB(&aftpb.Afts{
		Ipv4Entry: []*aftpb.Afts_Ipv4EntryKey{e},
	})
	if err != nil {
		return false, fmt.Errorf("invalid IPv4Entry, %v", err)
	}

	if r.checkFn != nil {
		ok, err := r.checkFn(nr)
		if err != nil {
			// This entry can never be installed, so return the error
			// to the caller directly -- signalling to them not to retry.
			return false, err
		}
		if !ok {
			// The checkFn validated the entry and found it to be OK, but
			// indicated that we should not merge it into the RIB because
			// some prerequisite was not satisifed. Based on this, we
			// return false (we didn't install it), but indicate with err == nil
			// that the caller can retry this entry at some later point, and we'll
			// run the checkFn again to see whether it can now be installed.
			return false, nil
		}
	}

	if err := r.doAddIPv4(e.GetPrefix(), nr); err != nil {
		return false, err
	}

	// We expect that there is just a single entry here since we are
	// being called based on a single entry, but we loop since we don't
	// know the key.
	if r.postChangeHook != nil {
		for _, ip4 := range nr.Afts.Ipv4Entry {
			r.postChangeHook(constants.Add, unixTS(), r.name, ip4)
		}
	}

	return true, nil
}

// doAddIPv4 adds an IPv4Entry holding the shortest possible lock on the RIB.
func (r *RIBHolder) doAddIPv4(pfx string, newRIB *aft.RIB) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Sanity check.
	if nhg, nh := len(newRIB.Afts.NextHopGroup), len(newRIB.Afts.NextHop); nhg != 0 || nh != 0 {
		return fmt.Errorf("candidate RIB specifies entries other than NextHopGroups, got: %d nhg, %d nh", nhg, nh)
	}

	// MergeStructInto doesn't completely replace a list entry if it finds a missing key,
	// so will append the two entries together.
	// We don't use Delete itself because it will deadlock (we already hold the lock).
	delete(r.r.GetAfts().Ipv4Entry, pfx)

	// TODO(robjs): consider what happens if this fails -- we may leave the RIB in
	// an inconsistent state.
	if err := ygot.MergeStructInto(r.r, newRIB); err != nil {
		return fmt.Errorf("cannot merge candidate RIB into existing RIB, %v", err)
	}
	return nil
}

// DeleteIPv4 removes the IPv4 entry e from the RIB. If e specifies only the prefix, and
// no payload the prefix is removed if it is found in the set of entries. If the payload
// of the entry is specified it is checked for equality, and removed only if the entries
// match.
func (r *RIBHolder) DeleteIPv4(e *aftpb.Afts_Ipv4EntryKey) error {
	if e == nil {
		return errors.New("nil entry provided")
	}

	if r.r == nil {
		return errors.New("invalid RIB structure, nil")
	}

	// This is an optional check, today some servers do not implement it and return true
	// even if the load does not match. Compliance tests should note this.
	if e.GetIpv4Entry() != nil {
		// We do not mind if we don't find this entry - since this shouldn't be an
		// error.
		existingEntryProto, _, err := r.ipv4EntryProto(e.GetPrefix())
		if err != nil {
			return err
		}
		if !proto.Equal(existingEntryProto, e) {
			return status.Newf(codes.NotFound, "delete of an entry with non-matching, existing: %s, candidate: %s", existingEntryProto, e).Err()
		}
	}

	de := r.r.Afts.Ipv4Entry[e.GetPrefix()]
	r.doDeleteIPv4(e.GetPrefix())

	if r.postChangeHook != nil {
		r.postChangeHook(constants.Delete, unixTS(), r.name, de)
	}

	return nil
}

// ipv4EntryProto returns a protobuf message for the specified IPv4 prefix. It returns
// the found prefix as a Ipv4EntryKey protobuf, along with a bool indicating whether the
// prefix was found in the RIB.
func (r *RIBHolder) ipv4EntryProto(pfx string) (*aftpb.Afts_Ipv4EntryKey, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ribE := r.r.Afts.Ipv4Entry[pfx]
	if ribE == nil {
		return nil, false, nil
	}

	existingEntryProto, err := concreteIPv4Proto(ribE)
	if err != nil {
		return nil, true, status.Newf(codes.Internal, "invalid existing entry in RIB %v", ribE).Err()
	}

	return existingEntryProto, true, nil
}

// doDeleteIPv4 deletes pfx from the IPv4Entry RIB holding the shortest possible lock.
func (r *RIBHolder) doDeleteIPv4(pfx string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.r.Afts.Ipv4Entry, pfx)
}

// AddNextHopGroup adds a NextHopGroup e to the RIBHolder receiver. It returns an error
// if the group cannot be added.
func (r *RIBHolder) AddNextHopGroup(e *aftpb.Afts_NextHopGroupKey) (bool, error) {
	if r.r == nil {
		return false, errors.New("invalid RIB structure, nil")
	}

	if e == nil {
		return false, errors.New("nil NextHopGroup provided")
	}

	nr, err := candidateRIB(&aftpb.Afts{
		NextHopGroup: []*aftpb.Afts_NextHopGroupKey{e},
	})
	if err != nil {
		return false, fmt.Errorf("invalid NextHopGroup, %v", err)
	}

	if r.checkFn != nil {
		ok, err := r.checkFn(nr)
		if err != nil {
			// Entry can never be installed (see the documentation in
			// the AddIPv4 function for additional details).
			return false, err
		}
		if !ok {
			// Entry is not valid for installation right now.
			return false, nil
		}
	}

	if err := r.doAddNHG(e.GetId(), nr); err != nil {
		return false, err
	}

	if r.postChangeHook != nil {
		for _, nhg := range nr.Afts.NextHopGroup {
			r.postChangeHook(constants.Add, unixTS(), r.name, nhg)
		}
	}

	return true, nil
}

// doAddNHG adds a NHG holding the shortest possible lock on the RIB to avoid
// deadlocking.
func (r *RIBHolder) doAddNHG(ID uint64, newRIB *aft.RIB) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Sanity check.
	if ip4, nh := len(newRIB.Afts.Ipv4Entry), len(newRIB.Afts.NextHop); ip4 != 0 || nh != 0 {
		return fmt.Errorf("candidate RIB specifies entries other than NextHopGroups, got: %d ipv4, %d nh", ip4, nh)
	}

	// Handle implicit replace.
	delete(r.r.GetAfts().NextHopGroup, ID)

	if err := ygot.MergeStructInto(r.r, newRIB); err != nil {
		return fmt.Errorf("cannot merge candidate RIB into existing RIB, %v", err)
	}
	return nil
}

// AddNextHop adds a new NextHop e to the RIBHolder receiver. It returns an error if
// the group cannot be added.
func (r *RIBHolder) AddNextHop(e *aftpb.Afts_NextHopKey) (bool, error) {
	if r.r == nil {
		return false, errors.New("invalid RIB structure, nil")
	}

	if e == nil {
		return false, errors.New("nil NextHop provided")
	}
	nr, err := candidateRIB(&aftpb.Afts{
		NextHop: []*aftpb.Afts_NextHopKey{e},
	})
	if err != nil {
		return false, fmt.Errorf("invalid NextHopGroup, %v", err)
	}
	if r.checkFn != nil {
		ok, err := r.checkFn(nr)
		if err != nil {
			// Entry can never be installed (see the documentation in
			// the AddIPv4 function for additional details).
			return false, err
		}
		if !ok {
			// Entry is not valid for installation right now.
			return false, nil
		}
	}

	if err := r.doAddNH(e.GetIndex(), nr); err != nil {
		return false, err
	}

	if r.postChangeHook != nil {
		for _, nh := range nr.Afts.NextHop {
			r.postChangeHook(constants.Add, unixTS(), r.name, nh)
		}
	}

	return true, nil
}

// doAddNHGadds a NH holding the shortest possible lock on the RIB to avoid
// deadlocking.
func (r *RIBHolder) doAddNH(index uint64, newRIB *aft.RIB) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Sanity check.
	if ip4, nhg := len(newRIB.Afts.Ipv4Entry), len(newRIB.Afts.NextHopGroup); ip4 != 0 || nhg != 0 {
		return fmt.Errorf("candidate RIB specifies entries other than NextHopGroups, got: %d ipv4, %d nhg", ip4, nhg)
	}

	// Handle implicit replace.
	delete(r.r.GetAfts().NextHop, index)

	if err := ygot.MergeStructInto(r.r, newRIB); err != nil {
		return fmt.Errorf("cannot merge candidate RIB into existing RIB, %v", err)
	}
	return nil
}

// contextIPv4Proto takes the input Ipv4Entry GoStruct and returns it as a gRIBI
// Ipv4EntryKey protobuf. It returns an error if the protobuf cannot be marshalled.
func concreteIPv4Proto(e *aft.Afts_Ipv4Entry) (*aftpb.Afts_Ipv4EntryKey, error) {
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

// concreteNextHopProto takes the input NextHop GoStruct and returns it as a gRIBI
// NextHopEntryKey protobuf. It returns an error if the protobuf cannot be marshalled.
func concreteNextHopProto(e *aft.Afts_NextHop) (*aftpb.Afts_NextHopKey, error) {
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

// concreteNextHopGroupProto takes the input NextHopGroup GoStruct and returns it as a gRIBI
// NextHopGroupEntryKey protobuf. It returns an error if the protobuf cannot be marshalled.
func concreteNextHopGroupProto(e *aft.Afts_NextHopGroup) (*aftpb.Afts_NextHopGroupKey, error) {
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
func protoFromGoStruct(s ygot.GoStruct, prefix *gpb.Path, pb proto.Message) error {
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
		protomap.IgnoreExtraPaths()); err != nil {
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
			spb.AFTType_NEXTHOP:       true,
			spb.AFTType_NEXTHOP_GROUP: true,
		}
	}

	if filter[spb.AFTType_IPV4] {
		for pfx, e := range r.r.Afts.Ipv4Entry {
			select {
			case <-stopCh:
				return nil
			default:
				p, err := concreteIPv4Proto(e)
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

	if filter[spb.AFTType_NEXTHOP_GROUP] {
		for index, e := range r.r.Afts.NextHopGroup {
			select {
			case <-stopCh:
				return nil
			default:
				p, err := concreteNextHopGroupProto(e)
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
				p, err := concreteNextHopProto(e)
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

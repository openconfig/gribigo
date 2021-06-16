// Package RIB implements a basic RIB for a gRIBI server.
package rib

import (
	"errors"
	"fmt"
	"sync"
	"time"

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

	// TODO(robjs): reference count NHGs and NHs across all AFTs to ensure that we
	// don't allow entries to be deleted that are in use.
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

	// postChangeHook is a function that is called after each of the operations
	// within the RIB completes, it takes arguments of the
	//   - name of the network instance
	// 	 - operation type (as an constants.OpType enumerated value)
	//	 - the changed entry as a ygot.GoStruct.
	postChangeHook RIBHookFn
}

// New returns a new RIB with the default network instance created with name dn.
func New(dn string) *RIB {
	return &RIB{
		niRIB: map[string]*RIBHolder{
			dn: NewRIBHolder(dn),
		},
		defaultName: dn,
	}
}

// SetHook assigns the supplied hook to all network instance RIBs within
// the RIB structure.
func (r *RIB) SetHook(fn RIBHookFn) {
	for _, nir := range r.niRIB {
		nir.postChangeHook = fn
	}
}

// NI returns the RIB for the network instance with name s.
func (r *RIB) RIBForNI(s string) (*RIBHolder, bool) {
	r.nrMu.RLock()
	defer r.nrMu.RUnlock()
	rh, ok := r.niRIB[s]
	return rh, ok
}

// NewRIBHolder returns a new RIB holder for a single network instance.
func NewRIBHolder(name string) *RIBHolder {
	return &RIBHolder{
		name: name,
		r: &aft.RIB{
			Afts: &aft.Afts{},
		},
	}
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

// AddIPv4 adds the IPv4 entry described by e to the RIB. It returns an error
// if the entry cannot be added.
func (r *RIBHolder) AddIPv4(e *aftpb.Afts_Ipv4EntryKey) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.r == nil {
		return errors.New("invalid RIB structure, nil")
	}

	if e == nil {
		return errors.New("nil IPv4 Entry provided")
	}

	// This is a hack, since ygot does not know that the field that we
	// have provided is a list entry, then it doesn't do the right thing. So
	// we just give it the root so that it knows.
	nr, err := candidateRIB(&aftpb.Afts{
		Ipv4Entry: []*aftpb.Afts_Ipv4EntryKey{e},
	})
	if err != nil {
		return fmt.Errorf("invalid IPv4Entry, %v", err)
	}

	// MergeStructInto doesn't completely replace a list entry if it finds a missing key,
	// so will append the two entries together.
	// We don't use Delete itself because it will deadlock (we already hold the lock).
	delete(r.r.GetAfts().Ipv4Entry, e.GetPrefix())

	// TODO(robjs): consider what happens if this fails -- we may leave the RIB in
	// an inconsistent state.
	if err := ygot.MergeStructInto(r.r, nr); err != nil {
		return fmt.Errorf("cannot merge candidate RIB into existing RIB, %v", err)
	}

	// We expect that there is just a single entry here since we are
	// being called based on a single entry, but we loop since we don't
	// know the key.
	if r.postChangeHook != nil {
		for _, ip4 := range nr.Afts.Ipv4Entry {
			r.postChangeHook(constants.ADD, unixTS(), r.name, ip4)
		}
	}

	return nil
}

// DeleteIPv4 removes the IPv4 entry e from the RIB. If e specifies only the prefix, and
// no payload the prefix is removed if it is found in the set of entries. If the payload
// of the entry is specified it is checked for equality, and removed only if the entries
// match.
func (r *RIBHolder) DeleteIPv4(e *aftpb.Afts_Ipv4EntryKey) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e == nil {
		return errors.New("nil entry provided")
	}

	if r.r == nil {
		return errors.New("invalid RIB structure, nil")
	}

	ribE := r.r.Afts.Ipv4Entry[e.GetPrefix()]
	if ribE == nil {
		return status.Newf(codes.NotFound, "cannot find IPv4Entry to delete, %s", e.Prefix).Err()
	}

	// This is an optional check, today some servers do not implement it and return true
	// even if the load does not match. Compliance tests should note this.
	if e.GetIpv4Entry() != nil {
		existingEntryProto, err := concreteIPv4Proto(ribE)
		if err != nil {
			return status.Newf(codes.Internal, "invalid existing entry in RIB %s", e).Err()
		}

		if !proto.Equal(existingEntryProto, e) {
			return status.Newf(codes.NotFound, "delete of an entry with non-matching, existing: %s, candidate: %s", existingEntryProto, e).Err()
		}
	}

	de := r.r.Afts.Ipv4Entry[e.GetPrefix()]

	delete(r.r.Afts.Ipv4Entry, e.GetPrefix())

	if r.postChangeHook != nil {
		r.postChangeHook(constants.DELETE, unixTS(), r.name, de)
	}

	return nil
}

// AddNextHopGroup adds a NextHopGroup e to the RIBHolder receiver. It returns an error
// if the group cannot be added.
func (r *RIBHolder) AddNextHopGroup(e *aftpb.Afts_NextHopGroupKey) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.r == nil {
		return errors.New("invalid RIB structure, nil")
	}

	if e == nil {
		return errors.New("nil NextHopGroup provided")
	}
	nr, err := candidateRIB(&aftpb.Afts{
		NextHopGroup: []*aftpb.Afts_NextHopGroupKey{e},
	})
	if err != nil {
		return fmt.Errorf("invalid NextHopGroup, %v", err)
	}

	// Handle implicit replace.
	delete(r.r.GetAfts().NextHop, e.GetId())

	if err := ygot.MergeStructInto(r.r, nr); err != nil {
		return fmt.Errorf("cannot merge candidate RIB into existing RIB, %v", err)
	}

	if r.postChangeHook != nil {
		for _, nhg := range nr.Afts.NextHopGroup {
			r.postChangeHook(constants.ADD, unixTS(), r.name, nhg)
		}
	}

	return nil
}

// AddNextHop adds a new NextHop e to the RIBHolder receiver. It returns an error if
// the group cannot be added.
func (r *RIBHolder) AddNextHop(e *aftpb.Afts_NextHopKey) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.r == nil {
		return errors.New("invalid RIB structure, nil")
	}

	if e == nil {
		return errors.New("nil NextHop provided")
	}
	nr, err := candidateRIB(&aftpb.Afts{
		NextHop: []*aftpb.Afts_NextHopKey{e},
	})
	if err != nil {
		return fmt.Errorf("invalid NextHopGroup, %v", err)
	}

	// Handle implicit replace.
	delete(r.r.GetAfts().NextHopGroup, e.GetIndex())

	if err := ygot.MergeStructInto(r.r, nr); err != nil {
		return fmt.Errorf("cannot merge candidate RIB into existing RIB, %v", err)
	}

	if r.postChangeHook != nil {
		for _, nh := range nr.Afts.NextHop {
			r.postChangeHook(constants.ADD, unixTS(), r.name, nh)
		}
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

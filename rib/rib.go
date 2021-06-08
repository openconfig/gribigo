// Package RIB implements a basic RIB for a gRIBI server.
package rib

import (
	"errors"
	"fmt"

	"github.com/openconfig/gnmi/value"
	"github.com/openconfig/goyang/pkg/yang"
	"github.com/openconfig/gribigo/aft"
	"github.com/openconfig/ygot/protomap"
	"github.com/openconfig/ygot/ygot"
	"github.com/openconfig/ygot/ytypes"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	aftpb "github.com/openconfig/gribi/v1/proto/gribi_aft"
)

type RIB struct {
	// r is the OpenConfig AFT representation that is used to represent a RIB.
	r *aft.RIB

	// TODO(robjs): we need locking to be implemented for the RIB.

}

// New returns a new RIB.
func New() *RIB {
	return &RIB{
		r: &aft.RIB{},
	}
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

	return nr, nil
}

// AddIPv4 adds the IPv4 entry described by e to the RIB. It returns an error
// if the entry cannot be added.
func (r *RIB) AddIPv4(e *aftpb.Afts_Ipv4EntryKey) error {
	// This is a hack, since ygot does not know that the field that we
	// have provided is a list entry, then it doesn't do the right thing. So
	// we just give it the root so that it knows.
	nr, err := candidateRIB(&aftpb.Afts{
		Ipv4Entry: []*aftpb.Afts_Ipv4EntryKey{e},
	})
	if err != nil {
		return fmt.Errorf("invalid IPv4Entry, %v", err)
	}

	if err := ygot.MergeStructInto(r.r, nr); err != nil {
		return fmt.Errorf("cannot merge candidate RIB into existing RIB, %v", err)
	}

	return nil
}

// DeleteIPv4 removes the IPv4 entry e from the RIB. If e specifies only the prefix, and
// no payload the prefix is removed if it is found in the set of entries. If the payload
// of the entry is specified it is checked for equality, and removed only if the entries
// match.
func (r *RIB) DeleteIPv4(e *aftpb.Afts_Ipv4EntryKey) error {
	if e == nil {
		return errors.New("nil entry provided")
	}
	ribE := r.r.Afts.Ipv4Entry[e.GetPrefix()]
	if ribE == nil {
		return status.Newf(codes.NotFound, "cannot find IPv4Entry to delete, %s", e.Prefix).Err()
	}

	if e.GetIpv4Entry() != nil {
		existingEntryProto, err := concreteIPv4Proto(ribE)
		if err != nil {
			return status.Newf(codes.Internal, "invalid existing entry in RIB %s", e).Err()
		}

		if !proto.Equal(existingEntryProto, e) {
			return status.Newf(codes.NotFound, "delete of an entry with non-matching, existing: %s, candidate: %s", existingEntryProto, e).Err()
		}
	}

	delete(r.r.Afts.Ipv4Entry, e.GetPrefix())

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

	if err := protomap.ProtoFromPaths(pb, vals, prefix, protomap.IgnoreExtraPaths()); err != nil {
		return fmt.Errorf("cannot unmarshal gNMI paths, %v", err)
	}

	return nil
}

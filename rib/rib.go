// Package RIB implements a basic RIB for a gRIBI server.
package rib

import (
	"fmt"

	"github.com/openconfig/gnmi/value"
	"github.com/openconfig/goyang/pkg/yang"
	"github.com/openconfig/gribigo/aft"
	"github.com/openconfig/ygot/protomap"
	"github.com/openconfig/ygot/ygot"
	"github.com/openconfig/ygot/ytypes"

	aftpb "github.com/openconfig/gribi/v1/proto/gribi_aft"
)

type RIB struct {
	// r is the OpenConfig AFT representation that is used to represent a RIB.
	r *aft.RIB
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

// AddIPv4 adds the IPv4 entry described by e to the RIB. It returns an error
// if the entry cannot be added.
func (r *RIB) AddIPv4(e *aftpb.Afts_Ipv4EntryKey) error {
	// This is a hack, since ygot does not know that the field that we
	// have provided is a list entry, then it doesn't do the right thing. So
	// we just give it the root so that it knows.
	hackroot := &aftpb.Afts{
		Ipv4Entry: []*aftpb.Afts_Ipv4EntryKey{e},
	}
	paths, err := protomap.PathsFromProto(hackroot)
	if err != nil {
		return err
	}

	nr := &aft.RIB{}
	rs, err := rootSchema()
	if err != nil {
		return err
	}

	for p, v := range paths {
		sv, err := value.FromScalar(v)

		if err != nil {
			ps := p.String()
			if yps, err := ygot.PathToString(p); err == nil {
				ps = yps
			}
			return fmt.Errorf("cannot convert field %s to scalar, %v", ps, sv)
		}
		if err := ytypes.SetNode(rs, nr, p, sv, &ytypes.InitMissingElements{}); err != nil {
			return fmt.Errorf("invalid IPv4Entry %s, %v", e, err)
		}
	}

	if err := ygot.MergeStructInto(r.r, nr); err != nil {
		return fmt.Errorf("cannot merge candidate RIB into existing RIB, %v", err)
	}

	return nil
}

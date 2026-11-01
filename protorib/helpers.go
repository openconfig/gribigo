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

package protorib

import (
	"fmt"
	"sort"

	aftpb "github.com/openconfig/gribi/v1/proto/gribi_aft"
	spb "github.com/openconfig/gribi/v1/proto/service"
	wpb "github.com/openconfig/ygot/proto/ywrapper"
)

// FromGetResponses constructs a RIB from a slice of gRIBI GetResponse messages in a single pass.
// The supplied defaultName is used as the default network instance name.
// It returns an error if any entry is invalid, duplicated within a network instance, or of an unhandled type.
func FromGetResponses(defaultName string, responses []*spb.GetResponse, opt ...RIBOpt) (*RIB, error) {
	r := New(defaultName, opt...)

	// Throughout this operation we do not lock the newly created RIB structures
	// because no external caller has access to r until FromGetResponses returns.
	for _, resp := range responses {
		for _, e := range resp.GetEntry() {
			ni := e.GetNetworkInstance()
			niR := r.niRIB[ni]
			if niR == nil {
				if err := r.AddNetworkInstance(ni); err != nil {
					return nil, fmt.Errorf("cannot create network instance RIB for NI %s, err: %v", ni, err)
				}
				niR = r.niRIB[ni]
			}

			switch t := e.GetEntry().(type) {
			case *spb.AFTEntry_Ipv4:
				entry, err := validateAndCloneIPv4(t.Ipv4)
				if err != nil {
					return nil, fmt.Errorf("cannot build RIB for NI %s, err: %v", ni, err)
				}
				pfx := entry.GetPrefix()
				if _, exists := niR.r.IPv4Entry[pfx]; exists {
					return nil, fmt.Errorf("cannot build RIB for NI %s, duplicate IPv4 prefix %s", ni, pfx)
				}
				niR.r.IPv4Entry[pfx] = entry
			case *spb.AFTEntry_Mpls:
				lk, entry, err := validateAndCloneMPLS(t.Mpls)
				if err != nil {
					return nil, fmt.Errorf("cannot build RIB for NI %s, err: %v", ni, err)
				}
				if _, exists := niR.r.LabelEntry[lk]; exists {
					return nil, fmt.Errorf("cannot build RIB for NI %s, duplicate MPLS label %v", ni, lk)
				}
				niR.r.LabelEntry[lk] = entry
			case *spb.AFTEntry_Ipv6:
				entry, err := validateAndCloneIPv6(t.Ipv6)
				if err != nil {
					return nil, fmt.Errorf("cannot build RIB for NI %s, err: %v", ni, err)
				}
				pfx := entry.GetPrefix()
				if _, exists := niR.r.IPv6Entry[pfx]; exists {
					return nil, fmt.Errorf("cannot build RIB for NI %s, duplicate IPv6 prefix %s", ni, pfx)
				}
				niR.r.IPv6Entry[pfx] = entry
			case *spb.AFTEntry_NextHopGroup:
				entry, err := validateAndCloneNHG(t.NextHopGroup)
				if err != nil {
					return nil, fmt.Errorf("cannot build RIB for NI %s, err: %v", ni, err)
				}
				id := entry.GetId()
				if _, exists := niR.r.NextHopGroup[id]; exists {
					return nil, fmt.Errorf("cannot build RIB for NI %s, duplicate NextHopGroup ID %d", ni, id)
				}
				niR.r.NextHopGroup[id] = entry
			case *spb.AFTEntry_NextHop:
				entry, err := validateAndCloneNH(t.NextHop)
				if err != nil {
					return nil, fmt.Errorf("cannot build RIB for NI %s, err: %v", ni, err)
				}
				idx := entry.GetIndex()
				if _, exists := niR.r.NextHop[idx]; exists {
					return nil, fmt.Errorf("cannot build RIB for NI %s, duplicate NextHop index %d", ni, idx)
				}
				niR.r.NextHop[idx] = entry
			default:
				return nil, fmt.Errorf("unknown/unhandled type %T in received GetResponses", t)
			}
		}
	}

	return r, nil
}

// FakeRIB wraps a RIB for use in testing and exposes convenience methods to populate AFT entries.
type FakeRIB struct {
	// r is the underlying RIB instance populated by the fake helper methods.
	r *RIB
}

// NewFake returns a new FakeRIB initialised with defaultName and opt.
func NewFake(defaultName string, opt ...RIBOpt) *FakeRIB {
	return &FakeRIB{
		r: New(defaultName, opt...),
	}
}

// RIB returns the constructed RIB held by the FakeRIB.
func (f *FakeRIB) RIB() *RIB {
	return f.r
}

// InjectIPv4 adds an IPv4 entry to network instance ni with prefix pfx referencing next-hop-group nhg.
// It returns an error if the network instance does not exist or the entry cannot be added.
func (f *FakeRIB) InjectIPv4(ni, pfx string, nhg uint64) error {
	niR, ok := f.r.NetworkInstanceRIB(ni)
	if !ok {
		return fmt.Errorf("unknown NI, %s", ni)
	}
	if _, _, err := niR.AddIPv4(&aftpb.Afts_Ipv4EntryKey{
		Prefix: pfx,
		Ipv4Entry: &aftpb.Afts_Ipv4Entry{
			NextHopGroup: &wpb.UintValue{Value: nhg},
		},
	}, false); err != nil {
		return fmt.Errorf("cannot add IPv4 entry, err: %v", err)
	}

	return nil
}

// InjectIPv6 adds an IPv6 entry to network instance ni with prefix pfx referencing next-hop-group nhg.
// It returns an error if the network instance does not exist or the entry cannot be added.
func (f *FakeRIB) InjectIPv6(ni, pfx string, nhg uint64) error {
	niR, ok := f.r.NetworkInstanceRIB(ni)
	if !ok {
		return fmt.Errorf("unknown NI, %s", ni)
	}
	if _, _, err := niR.AddIPv6(&aftpb.Afts_Ipv6EntryKey{
		Prefix: pfx,
		Ipv6Entry: &aftpb.Afts_Ipv6Entry{
			NextHopGroup: &wpb.UintValue{Value: nhg},
		},
	}, false); err != nil {
		return fmt.Errorf("cannot add IPv6 entry, err: %v", err)
	}

	return nil
}

// InjectNHG adds a next-hop-group entry to network instance ni with ID nhgID and member next-hops nhs
// mapping each next-hop index to its weight. It returns an error if the entry cannot be added.
func (f *FakeRIB) InjectNHG(ni string, nhgID uint64, nhs map[uint64]uint64) error {
	niR, ok := f.r.NetworkInstanceRIB(ni)
	if !ok {
		return fmt.Errorf("unknown NI, %s", ni)
	}

	nhg := &aftpb.Afts_NextHopGroupKey{
		Id:           nhgID,
		NextHopGroup: &aftpb.Afts_NextHopGroup{},
	}
	keys := make([]uint64, 0, len(nhs))
	for nh := range nhs {
		keys = append(keys, nh)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, nh := range keys {
		nhg.NextHopGroup.NextHop = append(nhg.NextHopGroup.NextHop, &aftpb.Afts_NextHopGroup_NextHopKey{
			Index: nh,
			NextHop: &aftpb.Afts_NextHopGroup_NextHop{
				Weight: &wpb.UintValue{Value: nhs[nh]},
			},
		})
	}

	if _, _, err := niR.AddNextHopGroup(nhg, false); err != nil {
		return fmt.Errorf("cannot add NHG entry, err: %v", err)
	}

	return nil
}

// InjectNH adds a next-hop entry to network instance ni with index nhIdx and interface reference intName.
// It returns an error if the network instance does not exist or the entry cannot be added.
func (f *FakeRIB) InjectNH(ni string, nhIdx uint64, intName string) error {
	niR, ok := f.r.NetworkInstanceRIB(ni)
	if !ok {
		return fmt.Errorf("unknown NI, %s", ni)
	}

	if _, _, err := niR.AddNextHop(&aftpb.Afts_NextHopKey{
		Index: nhIdx,
		NextHop: &aftpb.Afts_NextHop{
			InterfaceRef: &aftpb.Afts_NextHop_InterfaceRef{
				Interface: &wpb.StringValue{Value: intName},
			},
		},
	}, false); err != nil {
		return fmt.Errorf("cannot add NH entry, err: %v", err)
	}

	return nil
}

// InjectMPLS adds an MPLS label entry to network instance ni with label referencing next-hop-group nhg.
// It returns an error if the network instance does not exist or the entry cannot be added.
func (f *FakeRIB) InjectMPLS(ni string, label, nhg uint64) error {
	niR, ok := f.r.NetworkInstanceRIB(ni)
	if !ok {
		return fmt.Errorf("unknown NI, %s", ni)
	}

	if _, _, err := niR.AddMPLS(&aftpb.Afts_LabelEntryKey{
		Label: &aftpb.Afts_LabelEntryKey_LabelUint64{
			LabelUint64: label,
		},
		LabelEntry: &aftpb.Afts_LabelEntry{
			NextHopGroup: &wpb.UintValue{Value: nhg},
		},
	}, false); err != nil {
		return fmt.Errorf("cannot add MPLS entry, err: %v", err)
	}

	return nil
}

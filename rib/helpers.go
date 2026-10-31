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

package rib

import (
	"fmt"

	aftpb "github.com/openconfig/gribi/v1/proto/gribi_aft"
	spb "github.com/openconfig/gribi/v1/proto/service"
	"github.com/openconfig/gribigo/aft"
	wpb "github.com/openconfig/ygot/proto/ywrapper"
)

// niCounts tracks per-AFT entry counts for a network instance when pre-sizing maps in FromGetResponses.
type niCounts struct {
	ipv4 int
	ipv6 int
	mpls int
	nhg  int
	nh   int
}

// FromGetResponses returns a RIB from a slice of gRIBI GetResponse messages.
// The supplied defaultName is used as the default network instance name.
func FromGetResponses(defaultName string, responses []*spb.GetResponse, opt ...RIBOpt) (*RIB, error) {
	r := New(defaultName, opt...)
	counts := map[string]*niCounts{}
	for _, resp := range responses {
		for _, e := range resp.GetEntry() {
			ni := e.GetNetworkInstance()
			c := counts[ni]
			if c == nil {
				c = &niCounts{}
				counts[ni] = c
			}
			switch e.GetEntry().(type) {
			case *spb.AFTEntry_Ipv4:
				c.ipv4++
			case *spb.AFTEntry_Mpls:
				c.mpls++
			case *spb.AFTEntry_Ipv6:
				c.ipv6++
			case *spb.AFTEntry_NextHopGroup:
				c.nhg++
			case *spb.AFTEntry_NextHop:
				c.nh++
			default:
				return nil, fmt.Errorf("unknown/unhandled type %T in received GetResponses", e.GetEntry())
			}
		}
	}

	// Throughout this operation we do not worry about locking the structures in the new
	// RIB because we are the only function that can access the newly created data structure.
	for ni, c := range counts {
		if ni != defaultName {
			if err := r.AddNetworkInstance(ni); err != nil {
				return nil, fmt.Errorf("cannot create network instance RIB for NI %s, err: %v", ni, err)
			}
		}
		afts := &aft.Afts{}
		if c.ipv4 > 0 {
			afts.Ipv4Entry = make(map[string]*aft.Afts_Ipv4Entry, c.ipv4)
		}
		if c.ipv6 > 0 {
			afts.Ipv6Entry = make(map[string]*aft.Afts_Ipv6Entry, c.ipv6)
		}
		if c.mpls > 0 {
			afts.LabelEntry = make(map[aft.Afts_LabelEntry_Label_Union]*aft.Afts_LabelEntry, c.mpls)
		}
		if c.nhg > 0 {
			afts.NextHopGroup = make(map[uint64]*aft.Afts_NextHopGroup, c.nhg)
		}
		if c.nh > 0 {
			afts.NextHop = make(map[uint64]*aft.Afts_NextHop, c.nh)
		}
		r.niRIB[ni].r = &aft.RIB{Afts: afts}
	}

	for _, resp := range responses {
		for _, e := range resp.GetEntry() {
			ni := e.GetNetworkInstance()
			afts := r.niRIB[ni].r.Afts
			switch t := e.GetEntry().(type) {
			case *spb.AFTEntry_Ipv4:
				v4, err := ipv4EntryFromProto(t.Ipv4)
				if err != nil {
					return nil, fmt.Errorf("cannot build RIB for NI %s, err: %v", ni, err)
				}
				afts.Ipv4Entry[t.Ipv4.GetPrefix()] = v4
			case *spb.AFTEntry_Mpls:
				lk, mpls, err := mplsEntryFromProto(t.Mpls)
				if err != nil {
					return nil, fmt.Errorf("cannot build RIB for NI %s, err: %v", ni, err)
				}
				afts.LabelEntry[lk] = mpls
			case *spb.AFTEntry_Ipv6:
				v6, err := ipv6EntryFromProto(t.Ipv6)
				if err != nil {
					return nil, fmt.Errorf("cannot build RIB for NI %s, err: %v", ni, err)
				}
				afts.Ipv6Entry[t.Ipv6.GetPrefix()] = v6
			case *spb.AFTEntry_NextHopGroup:
				nhg, err := nhgEntryFromProto(t.NextHopGroup)
				if err != nil {
					return nil, fmt.Errorf("cannot build RIB for NI %s, err: %v", ni, err)
				}
				afts.NextHopGroup[t.NextHopGroup.GetId()] = nhg
			case *spb.AFTEntry_NextHop:
				nh, err := nhEntryFromProto(t.NextHop)
				if err != nil {
					return nil, fmt.Errorf("cannot build RIB for NI %s, err: %v", ni, err)
				}
				afts.NextHop[t.NextHop.GetIndex()] = nh
			}
		}
	}

	return r, nil
}

// fakeRIB is a RIB for use in testing which exposes methods that can be used to more easily
// construct a RIB's contents.
type fakeRIB struct {
	r *RIB
}

// NewFake returns a new Fake RIB.
func NewFake(defaultName string, opt ...RIBOpt) *fakeRIB {
	return &fakeRIB{
		r: New(defaultName, opt...),
	}
}

// RIB returns the constructed fake RIB to the caller.
func (f *fakeRIB) RIB() *RIB {
	return f.r
}

// InjectIPv4 adds an IPv4 entry to network instance ni, with the specified
// prefix (pfx), and referencing the specified next-hop-group with index nhg.
// It returns an error if the entry cannot be injected.
func (f *fakeRIB) InjectIPv4(ni, pfx string, nhg uint64) error {
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

// InjectIPv6 adds an IPv6 entry to network instance ni, with the specified
// prefix (pfx), and referencing the specified next-hop-group with index nhg.
// It returns an error if the entry cannot be injected.
func (f *fakeRIB) InjectIPv6(ni, pfx string, nhg uint64) error {
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

// InjectNHG adds a next-hop-group entry to network instance ni, with the specified
// ID (nhgID). The next-hop-group contains the next hops specified in the nhs map,
// with the key of the map being the next-hop ID and the value being the weight within
// the group.
func (f *fakeRIB) InjectNHG(ni string, nhgID uint64, nhs map[uint64]uint64) error {
	niR, ok := f.r.NetworkInstanceRIB(ni)
	if !ok {
		return fmt.Errorf("unknown NI, %s", ni)
	}

	nhg := &aftpb.Afts_NextHopGroupKey{
		Id:           nhgID,
		NextHopGroup: &aftpb.Afts_NextHopGroup{},
	}
	for nh, weight := range nhs {
		nhg.NextHopGroup.NextHop = append(nhg.NextHopGroup.NextHop, &aftpb.Afts_NextHopGroup_NextHopKey{
			Index: nh,
			NextHop: &aftpb.Afts_NextHopGroup_NextHop{
				Weight: &wpb.UintValue{Value: weight},
			},
		})
	}

	if _, _, err := niR.AddNextHopGroup(nhg, false); err != nil {
		return fmt.Errorf("cannot add NHG entry, err: %v", err)
	}

	return nil
}

// InjectNH adds a next-hop entry to network instance ni, with the specified
// index (nhIdx), and interface ref to intName. An error is returned if it cannot
// be added.
func (f *fakeRIB) InjectNH(ni string, nhIdx uint64, intName string) error {
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

// InjectMPLS adds an MPLS (Label) entry to network instance ni, with the
// specified next-hop-group. An error is returned if it cannot be added.
func (f *fakeRIB) InjectMPLS(ni string, label, nhg uint64) error {
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

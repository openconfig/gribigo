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

	"github.com/openconfig/ygot/ygot"

	aftpb "github.com/openconfig/gribi/v1/proto/gribi_aft"
	spb "github.com/openconfig/gribi/v1/proto/service"
	wpb "github.com/openconfig/ygot/proto/ywrapper"
)

// FromGetResponses returns a RIB from a slice of gRIBI GetResponse messages.
// The supplied defaultName is used as the default network instance name.
func FromGetResponses(defaultName string, responses []*spb.GetResponse, opt ...RIBOpt) (*RIB, error) {
	r := New(defaultName)
	niAFTs := map[string]*aftpb.Afts{}

	for _, resp := range responses {
		for _, e := range resp.Entry {
			ni := e.GetNetworkInstance()
			if _, ok := niAFTs[ni]; !ok {
				niAFTs[ni] = &aftpb.Afts{}
			}
			switch t := e.GetEntry().(type) {
			case *spb.AFTEntry_Ipv4:
				niAFTs[ni].Ipv4Entry = append(niAFTs[ni].Ipv4Entry, t.Ipv4)
			case *spb.AFTEntry_Mpls:
				niAFTs[ni].LabelEntry = append(niAFTs[ni].LabelEntry, t.Mpls)
			case *spb.AFTEntry_NextHopGroup:
				niAFTs[ni].NextHopGroup = append(niAFTs[ni].NextHopGroup, t.NextHopGroup)
			case *spb.AFTEntry_NextHop:
				niAFTs[ni].NextHop = append(niAFTs[ni].NextHop, t.NextHop)
			default:
				return nil, fmt.Errorf("unknown/unhandled type %T in received GetResponses", t)
			}
		}
	}

	// Throughout this operation we don't worry about locking the structures in the new
	// RIB because we are the only function that can access the newly created data structure.
	for ni, niRIB := range niAFTs {
		cr, err := candidateRIB(niRIB)
		if err != nil {
			return nil, fmt.Errorf("cannot build RIB for NI %s, err: %v", ni, err)
		}

		if ni != defaultName {
			if err := r.AddNetworkInstance(ni); err != nil {
				return nil, fmt.Errorf("cannot create network instance RIB for NI %s, err: %v", ni, err)
			}
		}

		if err := ygot.MergeStructInto(r.niRIB[ni].r, cr); err != nil {
			return nil, fmt.Errorf("cannot populate network instance RIB for NI %s, err: %v", ni, err)
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

// InjectNHG adds a next-hop-group entry to network instance ni, with the specified
// ID (nhgId). The next-hop-group contains the next hops specified in the nhs map,
// with the key of the map being the next-hop ID and the value being the weight within
// the group.
func (f *fakeRIB) InjectNHG(ni string, nhgId uint64, nhs map[uint64]uint64) error {
	niR, ok := f.r.NetworkInstanceRIB(ni)
	if !ok {
		return fmt.Errorf("unknown NI, %s", ni)
	}

	nhg := &aftpb.Afts_NextHopGroupKey{
		Id:           nhgId,
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

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
	"github.com/openconfig/ygot/ygot"
)

// RIBFromGetResponses returns a RIB from a slice of gRIBI GetResponse messages.
// The supplied defaultName is used as the default network instance name.
func RIBFromGetResponses(defaultName string, responses []*spb.GetResponse) (*RIB, error) {
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

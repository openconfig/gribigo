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

// Package afthelper provides helper functions for handling the OpenConfig
// AFT schema.
package afthelper

import (
	"fmt"
	"net/netip"

	"github.com/openconfig/gribigo/aft"
)

// NextHopSummary provides a summary of an next-hop for a particular entry.
type NextHopSummary struct {
	// Weight is the share of traffic that the next-hop gets.
	Weight uint64 `json:"weight"`
	// Address is the IP address of the next-hop.
	Address string `json:"address"`
	// NetworkInstance is the network instance within which the address was resolved.
	NetworkInstance string `json:"network-instance"`
	// Index is the gRIBI index of the next hop.
	Index uint64 `json:"index"`
}

// NextHopAddrsForPrefix unrolls the prefix specified within the network-instance netInst from the
// specified ribs. It returns a map of next-hop IP address to a summary of the resolved next-hop.
//
// TODO(robjs): support recursion.
func NextHopAddrsForPrefix(rib map[string]*aft.RIB, netinst, prefix string) (map[string]*NextHopSummary, error) {
	niAFT, ok := rib[netinst]
	if !ok {
		return nil, fmt.Errorf("network instance %s does not exist", netinst)
	}

	pfx, err := netip.ParsePrefix(prefix)
	if err != nil {
		return nil, fmt.Errorf("invalid prefix: %v", err)
	}

	var otherNI string
	var nhgID uint64
	if pfx.Addr().Is4() {
		v4 := niAFT.GetAfts().GetIpv4Entry(prefix)
		if v4 == nil {
			return nil, fmt.Errorf("cannot find IPv4 prefix in AFT")
		}
		otherNI = v4.GetNextHopGroupNetworkInstance()
		nhgID = v4.GetNextHopGroup()
	} else {
		v6 := niAFT.GetAfts().GetIpv6Entry(prefix)
		if v6 == nil {
			return nil, fmt.Errorf("cannot find IPv6 prefix in AFT")
		}
		otherNI = v6.GetNextHopGroupNetworkInstance()
		nhgID = v6.GetNextHopGroup()
	}

	nhNI := netinst
	if otherNI != "" {
		nhNI = otherNI
	}

	if _, ok := rib[nhNI]; !ok {
		return nil, fmt.Errorf("got invalid network instance, %s", nhNI)
	}

	nhg := rib[nhNI].GetAfts().GetNextHopGroup(nhgID)
	if nhg == nil {
		return nil, fmt.Errorf("got unknown NHG %d in NI %s", nhgID, nhNI)
	}

	// sum is a map of index -> weight.
	weights := map[uint64]uint64{}
	for _, nh := range nhg.NextHop {
		weights[nh.GetIndex()] = nh.GetWeight()
	}

	ret := map[string]*NextHopSummary{}
	for nhID := range weights {
		nh := rib[nhNI].GetAfts().GetNextHop(nhID).GetIpAddress()
		if nh == "" {
			return nil, fmt.Errorf("invalid next-hop %d", nhID)
		}
		ret[nh] = &NextHopSummary{
			Address:         nh,
			Weight:          weights[nhID],
			NetworkInstance: nhNI,
			Index:           nhID,
		}
	}

	return ret, nil
}

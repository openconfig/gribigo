// Copyright 2021 Google LLC
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
package afthelper

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/openconfig/gribigo/aft"
	"github.com/openconfig/ygot/ygot"
)

const (
	defName = "DEFAULT"
)

func TestNextHopAddrsForPrefix(t *testing.T) {

	tests := []struct {
		desc      string
		inRIB     map[string]*aft.RIB
		inNetInst string
		inPrefix  string
		want      map[string]*NextHopSummary
		wantErr   bool
	}{{
		desc: "simple ipv4 to one NH",
		inRIB: map[string]*aft.RIB{
			defName: func() *aft.RIB {
				r := &aft.RIB{}
				r.GetOrCreateAfts().GetOrCreateIpv4Entry("8.8.8.8/32").NextHopGroup = ygot.Uint64(1)
				r.GetOrCreateAfts().GetOrCreateNextHopGroup(1).GetOrCreateNextHop(1).Weight = ygot.Uint64(1)
				r.GetOrCreateAfts().GetOrCreateNextHop(1).IpAddress = ygot.String("1.1.1.1")
				return r
			}(),
		},
		inNetInst: defName,
		inPrefix:  "8.8.8.8/32",
		want: map[string]*NextHopSummary{
			"1.1.1.1": {
				Weight:          1,
				Address:         "1.1.1.1",
				NetworkInstance: defName,
			},
		},
	}, {
		desc: "ipv4 with two NH",
		inRIB: map[string]*aft.RIB{
			defName: func() *aft.RIB {
				r := &aft.RIB{}
				r.GetOrCreateAfts().GetOrCreateIpv4Entry("8.8.8.8/32").NextHopGroup = ygot.Uint64(1)
				r.GetOrCreateAfts().GetOrCreateNextHopGroup(1).GetOrCreateNextHop(1).Weight = ygot.Uint64(1)
				r.GetOrCreateAfts().GetOrCreateNextHopGroup(1).GetOrCreateNextHop(2).Weight = ygot.Uint64(1)
				r.GetOrCreateAfts().GetOrCreateNextHop(1).IpAddress = ygot.String("1.1.1.1")
				r.GetOrCreateAfts().GetOrCreateNextHop(2).IpAddress = ygot.String("2.2.2.2")
				return r
			}(),
		},
		inNetInst: defName,
		inPrefix:  "8.8.8.8/32",
		want: map[string]*NextHopSummary{
			"1.1.1.1": {
				Address:         "1.1.1.1",
				Weight:          1,
				NetworkInstance: defName,
			},
			"2.2.2.2": {
				Address:         "2.2.2.2",
				Weight:          1,
				NetworkInstance: defName,
			},
		},
	}, {
		desc: "ipv6 with two NH, one v4 and one v6",
		inRIB: map[string]*aft.RIB{
			defName: func() *aft.RIB {
				r := &aft.RIB{}
				r.GetOrCreateAfts().GetOrCreateIpv6Entry("2001:aaaa::/64").NextHopGroup = ygot.Uint64(1)
				r.GetOrCreateAfts().GetOrCreateNextHopGroup(1).GetOrCreateNextHop(1).Weight = ygot.Uint64(1)
				r.GetOrCreateAfts().GetOrCreateNextHopGroup(1).GetOrCreateNextHop(2).Weight = ygot.Uint64(1)
				r.GetOrCreateAfts().GetOrCreateNextHop(1).IpAddress = ygot.String("1000:10:10::10")
				r.GetOrCreateAfts().GetOrCreateNextHop(2).IpAddress = ygot.String("2.2.2.2")
				return r
			}(),
		},
		inNetInst: defName,
		inPrefix:  "2001:aaaa::/64",
		want: map[string]*NextHopSummary{
			"1000:10:10::10": {
				Address:         "1000:10:10::10",
				Weight:          1,
				NetworkInstance: defName,
			},
			"2.2.2.2": {
				Address:         "2.2.2.2",
				Weight:          1,
				NetworkInstance: defName,
			},
		},
	}, {
		desc:      "can't find network instance",
		inRIB:     map[string]*aft.RIB{},
		inNetInst: "fish",
		wantErr:   true,
	}, {
		desc: "can't find ipv4 prefix",
		inRIB: map[string]*aft.RIB{
			defName: {},
		},
		inPrefix: "42.42.42.42/32",
		wantErr:  true,
	}, {
		desc: "ipv4 with next-hop-group in another network instance",
		inRIB: map[string]*aft.RIB{
			defName: func() *aft.RIB {
				r := &aft.RIB{}
				v4 := r.GetOrCreateAfts().GetOrCreateIpv4Entry("1.2.3.4/32")
				v4.NextHopGroup = ygot.Uint64(1)
				v4.NextHopGroupNetworkInstance = ygot.String("VRF-1")
				return r
			}(),
			"VRF-1": func() *aft.RIB {
				r := &aft.RIB{}
				r.GetOrCreateAfts().GetOrCreateNextHopGroup(1).GetOrCreateNextHop(2).Weight = ygot.Uint64(2)
				r.GetOrCreateAfts().GetOrCreateNextHop(2).IpAddress = ygot.String("2.2.2.2")
				return r
			}(),
		},
		inPrefix:  "1.2.3.4/32",
		inNetInst: defName,
		want: map[string]*NextHopSummary{
			"2.2.2.2": {
				Weight:          2,
				Address:         "2.2.2.2",
				NetworkInstance: "VRF-1",
			},
		},
	}, {
		desc: "ipv6 with two NH, one v4 and one v6 in different network instance",
		inRIB: map[string]*aft.RIB{
			defName: func() *aft.RIB {
				r := &aft.RIB{}
				v6 := r.GetOrCreateAfts().GetOrCreateIpv6Entry("2001:aaaa::/64")
				v6.NextHopGroup = ygot.Uint64(1)
				v6.NextHopGroupNetworkInstance = ygot.String("VRF-1")
				return r
			}(),
			"VRF-1": func() *aft.RIB {
				r := &aft.RIB{}
				r.GetOrCreateAfts().GetOrCreateNextHopGroup(1).GetOrCreateNextHop(1).Weight = ygot.Uint64(1)
				r.GetOrCreateAfts().GetOrCreateNextHopGroup(1).GetOrCreateNextHop(2).Weight = ygot.Uint64(1)
				r.GetOrCreateAfts().GetOrCreateNextHop(1).IpAddress = ygot.String("1000:10:10::10")
				r.GetOrCreateAfts().GetOrCreateNextHop(2).IpAddress = ygot.String("2.2.2.2")
				return r
			}(),
		},
		inNetInst: defName,
		inPrefix:  "2001:aaaa::/64",
		want: map[string]*NextHopSummary{
			"1000:10:10::10": {
				Address:         "1000:10:10::10",
				Weight:          1,
				NetworkInstance: "VRF-1",
			},
			"2.2.2.2": {
				Address:         "2.2.2.2",
				Weight:          1,
				NetworkInstance: "VRF-1",
			},
		},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got, err := NextHopAddrsForPrefix(tt.inRIB, tt.inNetInst, tt.inPrefix)
			if (err != nil) != tt.wantErr {
				t.Fatalf("did not get expected error, got: %v, wantErr? %v", err, tt.wantErr)
			}

			if diff := cmp.Diff(got, tt.want); diff != "" {
				t.Fatalf("did not get expected output, diff(-got,+want):\n%s", diff)
			}
		})
	}
}

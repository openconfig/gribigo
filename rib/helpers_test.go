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

package rib

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/openconfig/gribigo/aft"

	aftpb "github.com/openconfig/gribi/v1/proto/gribi_aft"
	spb "github.com/openconfig/gribi/v1/proto/service"
	wpb "github.com/openconfig/ygot/proto/ywrapper"
	"github.com/openconfig/ygot/ygot"
)

func TestRIBFromGetResponses(t *testing.T) {
	defaultName := "DEFAULT"
	tests := []struct {
		desc          string
		inDefaultName string
		inResponses   []*spb.GetResponse
		wantRIB       *RIB
		wantErr       bool
	}{{
		desc: "single ipv4 entry",
		inResponses: []*spb.GetResponse{{
			Entry: []*spb.AFTEntry{{
				NetworkInstance: "VRF-1",
				Entry: &spb.AFTEntry_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix: "1.1.1.1/32",
						Ipv4Entry: &aftpb.Afts_Ipv4Entry{
							NextHopGroup: &wpb.UintValue{Value: 42},
						},
					},
				},
			}},
		}},
		inDefaultName: "DEFAULT",
		wantRIB: &RIB{
			defaultName: defaultName,
			niRIB: map[string]*RIBHolder{
				"DEFAULT": {
					name: "DEFAULT",
					r: &aft.RIB{
						Afts: &aft.Afts{},
					},
				},
				"VRF-1": {
					name: "VRF-1",
					r: &aft.RIB{
						Afts: &aft.Afts{
							Ipv4Entry: map[string]*aft.Afts_Ipv4Entry{
								"1.1.1.1/32": {
									Prefix:       ygot.String("1.1.1.1/32"),
									NextHopGroup: ygot.Uint64(42),
								},
							},
						},
					},
				},
			},
		},
	}, {
		desc: "next-hop group entry",
		inResponses: []*spb.GetResponse{{
			Entry: []*spb.AFTEntry{{
				NetworkInstance: defaultName,
				Entry: &spb.AFTEntry_NextHopGroup{
					NextHopGroup: &aftpb.Afts_NextHopGroupKey{
						Id: 42,
						NextHopGroup: &aftpb.Afts_NextHopGroup{
							BackupNextHopGroup: &wpb.UintValue{Value: 42},
						},
					},
				},
			}},
		}},
		inDefaultName: "DEFAULT",
		wantRIB: &RIB{
			defaultName: defaultName,
			niRIB: map[string]*RIBHolder{
				"DEFAULT": {
					name: "DEFAULT",
					r: &aft.RIB{
						Afts: &aft.Afts{
							NextHopGroup: map[uint64]*aft.Afts_NextHopGroup{
								42: {
									Id:                 ygot.Uint64(42),
									BackupNextHopGroup: ygot.Uint64(42),
								},
							},
						},
					},
				},
			},
		},
	}, {
		desc: "nexthop entry",
		inResponses: []*spb.GetResponse{{
			Entry: []*spb.AFTEntry{{
				NetworkInstance: defaultName,
				Entry: &spb.AFTEntry_NextHop{
					NextHop: &aftpb.Afts_NextHopKey{
						Index: 42,
						NextHop: &aftpb.Afts_NextHop{
							IpAddress: &wpb.StringValue{Value: "1.1.1.1"},
						},
					},
				},
			}},
		}},
		inDefaultName: "DEFAULT",
		wantRIB: &RIB{
			defaultName: defaultName,
			niRIB: map[string]*RIBHolder{
				"DEFAULT": {
					name: "DEFAULT",
					r: &aft.RIB{
						Afts: &aft.Afts{
							NextHop: map[uint64]*aft.Afts_NextHop{
								42: {
									Index:     ygot.Uint64(42),
									IpAddress: ygot.String("1.1.1.1"),
								},
							},
						},
					},
				},
			},
		},
	}, {
		desc: "mpls entry",
		inResponses: []*spb.GetResponse{{
			Entry: []*spb.AFTEntry{{
				NetworkInstance: defaultName,
				Entry: &spb.AFTEntry_Mpls{
					Mpls: &aftpb.Afts_LabelEntryKey{
						Label: &aftpb.Afts_LabelEntryKey_LabelUint64{
							LabelUint64: 42,
						},
						LabelEntry: &aftpb.Afts_LabelEntry{
							NextHopGroup: &wpb.UintValue{Value: 42},
						},
					},
				},
			}},
		}},
		inDefaultName: "DEFAULT",
		wantRIB: &RIB{
			defaultName: defaultName,
			niRIB: map[string]*RIBHolder{
				"DEFAULT": {
					name: "DEFAULT",
					r: &aft.RIB{
						Afts: &aft.Afts{
							LabelEntry: map[aft.Afts_LabelEntry_Label_Union]*aft.Afts_LabelEntry{
								aft.UnionUint32(42): {
									Label:        aft.UnionUint32(42),
									NextHopGroup: ygot.Uint64(42),
								},
							},
						},
					},
				},
			},
		},
	}, {
		desc: "multiple network instances",
		inResponses: []*spb.GetResponse{{
			Entry: []*spb.AFTEntry{{
				NetworkInstance: "VRF-1",
				Entry: &spb.AFTEntry_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix: "1.1.1.1/32",
						Ipv4Entry: &aftpb.Afts_Ipv4Entry{
							NextHopGroup: &wpb.UintValue{Value: 42},
						},
					},
				},
			}, {
				NetworkInstance: defaultName,
				Entry: &spb.AFTEntry_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix: "2.2.2.2/32",
						Ipv4Entry: &aftpb.Afts_Ipv4Entry{
							NextHopGroup: &wpb.UintValue{Value: 42},
						},
					},
				},
			}},
		}},
		inDefaultName: "DEFAULT",
		wantRIB: &RIB{
			defaultName: defaultName,
			niRIB: map[string]*RIBHolder{
				"DEFAULT": {
					name: "DEFAULT",
					r: &aft.RIB{
						Afts: &aft.Afts{
							Ipv4Entry: map[string]*aft.Afts_Ipv4Entry{
								"2.2.2.2/32": {
									Prefix:       ygot.String("2.2.2.2/32"),
									NextHopGroup: ygot.Uint64(42),
								},
							},
						},
					},
				},
				"VRF-1": {
					name: "VRF-1",
					r: &aft.RIB{
						Afts: &aft.Afts{
							Ipv4Entry: map[string]*aft.Afts_Ipv4Entry{
								"1.1.1.1/32": {
									Prefix:       ygot.String("1.1.1.1/32"),
									NextHopGroup: ygot.Uint64(42),
								},
							},
						},
					},
				},
			},
		},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got, err := RIBFromGetResponses(tt.inDefaultName, tt.inResponses)
			if (err != nil) != tt.wantErr {
				t.Fatalf("RIBFromGetResponses(...): did not get expected error, got: %v, wantErr? %v", err, tt.wantErr)
			}

			if diff := cmp.Diff(got, tt.wantRIB,
				cmpopts.EquateEmpty(), cmp.AllowUnexported(RIB{}),
				cmpopts.IgnoreFields(RIB{}, "nrMu", "pendMu", "ribCheck"),
				cmp.AllowUnexported(RIBHolder{}),
				cmpopts.IgnoreFields(RIBHolder{}, "mu", "refCounts", "checkFn"),
			); diff != "" {
				t.Fatalf("RIBFromGetResponses(...): did not get expected RIB, diff(-got,+want):\n%s", diff)
			}
		})
	}
}

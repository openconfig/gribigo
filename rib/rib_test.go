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
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/openconfig/gnmi/errdiff"
	"github.com/openconfig/gnmi/value"
	"github.com/openconfig/gribigo/aft"
	"github.com/openconfig/gribigo/afthelper"
	"github.com/openconfig/gribigo/constants"
	"github.com/openconfig/ygot/testutil"
	"github.com/openconfig/ygot/ygot"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	aftpb "github.com/openconfig/gribi/v1/proto/gribi_aft"
	"github.com/openconfig/gribi/v1/proto/gribi_aft/enums"
	spb "github.com/openconfig/gribi/v1/proto/service"
	wpb "github.com/openconfig/ygot/proto/ywrapper"
)

type ribType int64

const (
	_ ribType = iota
	ipv4
	nhg
	nh
	mpls
)

func TestAdd(t *testing.T) {
	tests := []struct {
		desc              string
		inRIBHolder       *RIBHolder
		inType            ribType
		inEntry           proto.Message
		inExplicitReplace bool
		wantInstalled     bool
		wantReplace       bool
		wantRIB           *aft.RIB
		wantErr           bool
	}{{
		desc:        "ipv4 prefix only",
		inRIBHolder: NewRIBHolder("DEFAULT"),
		inType:      ipv4,
		inEntry: &aftpb.Afts_Ipv4EntryKey{
			Prefix:    "1.0.0.0/24",
			Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
		},
		wantInstalled: true,
		wantRIB: &aft.RIB{
			Afts: &aft.Afts{
				Ipv4Entry: map[string]*aft.Afts_Ipv4Entry{
					"1.0.0.0/24": {
						Prefix: ygot.String("1.0.0.0/24"),
					},
				},
			},
		},
	}, {
		desc:        "ipv4 prefix and attributes",
		inRIBHolder: NewRIBHolder("DEFAULT"),
		inType:      ipv4,
		inEntry: &aftpb.Afts_Ipv4EntryKey{
			Prefix: "1.0.0.0/24",
			Ipv4Entry: &aftpb.Afts_Ipv4Entry{
				EntryMetadata: &wpb.BytesValue{Value: []byte{0, 1, 2, 3, 4, 5, 6, 7}},
			},
		},
		wantInstalled: true,
		wantRIB: &aft.RIB{
			Afts: &aft.Afts{
				Ipv4Entry: map[string]*aft.Afts_Ipv4Entry{
					"1.0.0.0/24": {
						Prefix:        ygot.String("1.0.0.0/24"),
						EntryMetadata: aft.Binary{0, 1, 2, 3, 4, 5, 6, 7},
					},
				},
			},
		},
	}, {
		desc:        "nil update",
		inRIBHolder: NewRIBHolder("DEFAULT"),
		inType:      ipv4,
		inEntry:     nil,
		wantErr:     true,
	}, {
		desc:        "nil IPv4 prefix value",
		inRIBHolder: NewRIBHolder("DEFAULT"),
		inType:      ipv4,
		inEntry:     &aftpb.Afts_Ipv4EntryKey{},
		wantErr:     true,
	}, {
		desc:        "nil IPv4 value",
		inRIBHolder: NewRIBHolder("DEFAULT"),
		inType:      ipv4,
		inEntry: &aftpb.Afts_Ipv4EntryKey{
			Prefix: "8.8.8.8/32",
		},
		wantErr: true,
	}, {
		desc: "implicit ipv4 replace",
		inRIBHolder: func() *RIBHolder {
			r := NewRIBHolder("DEFAULT")
			if _, _, err := r.AddIPv4(&aftpb.Afts_Ipv4EntryKey{
				Prefix: "8.8.8.8/32",
				Ipv4Entry: &aftpb.Afts_Ipv4Entry{
					EntryMetadata: &wpb.BytesValue{Value: []byte{0, 1, 2, 3, 4, 5, 6, 7}},
				},
			}, false); err != nil {
				t.Fatalf("cannot init test case, %v", err)
			}
			return r
		}(),
		inType: ipv4,
		inEntry: &aftpb.Afts_Ipv4EntryKey{
			Prefix: "8.8.8.8/32",
			Ipv4Entry: &aftpb.Afts_Ipv4Entry{
				EntryMetadata: &wpb.BytesValue{Value: []byte{10, 11, 12, 13, 14, 15, 16, 17}},
			},
		},
		wantInstalled: true,
		wantReplace:   true,
		wantRIB: &aft.RIB{
			Afts: &aft.Afts{
				Ipv4Entry: map[string]*aft.Afts_Ipv4Entry{
					"8.8.8.8/32": {
						Prefix:        ygot.String("8.8.8.8/32"),
						EntryMetadata: aft.Binary{10, 11, 12, 13, 14, 15, 16, 17},
					},
				},
			},
		},
	}, {
		desc:        "ipv4 replace with invalid data",
		inRIBHolder: NewRIBHolder("DEFAULT"),
		inType:      ipv4,
		inEntry: &aftpb.Afts_Ipv4EntryKey{
			Prefix:    "NOT AN IPV$ PREFIX",
			Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
		},
		wantErr: true,
	}, {
		desc:        "nhg ID only",
		inRIBHolder: NewRIBHolder("DEFAULT"),
		inType:      nhg,
		inEntry: &aftpb.Afts_NextHopGroupKey{
			Id:           1,
			NextHopGroup: &aftpb.Afts_NextHopGroup{},
		},
		wantInstalled: true,
		wantRIB: &aft.RIB{
			Afts: &aft.Afts{
				NextHopGroup: map[uint64]*aft.Afts_NextHopGroup{
					1: {
						Id: ygot.Uint64(1),
					},
				},
			},
		},
	}, {
		desc: "nhg implicit replace",
		inRIBHolder: func() *RIBHolder {
			r := NewRIBHolder("DEFAULT")
			if _, _, err := r.AddNextHopGroup(&aftpb.Afts_NextHopGroupKey{
				Id:           1,
				NextHopGroup: &aftpb.Afts_NextHopGroup{},
			}, false); err != nil {
				t.Fatalf("cannot init test case, %v", err)
			}
			return r
		}(),
		inType: nhg,
		inEntry: &aftpb.Afts_NextHopGroupKey{
			Id:           1,
			NextHopGroup: &aftpb.Afts_NextHopGroup{},
		},
		wantInstalled: true,
		wantReplace:   true,
		wantRIB: &aft.RIB{
			Afts: &aft.Afts{
				NextHopGroup: map[uint64]*aft.Afts_NextHopGroup{
					1: {
						Id: ygot.Uint64(1),
					},
				},
			},
		},
	}, {
		desc: "nh implicit replace",
		inRIBHolder: func() *RIBHolder {
			r := NewRIBHolder("DEFAULT")
			if _, _, err := r.AddNextHop(&aftpb.Afts_NextHopKey{
				Index:   1,
				NextHop: &aftpb.Afts_NextHop{},
			}, false); err != nil {
				t.Fatalf("cannot init test case, %v", err)
			}
			return r
		}(),
		inType: nh,
		inEntry: &aftpb.Afts_NextHopKey{
			Index:   1,
			NextHop: &aftpb.Afts_NextHop{},
		},
		wantInstalled: true,
		wantReplace:   true,
		wantRIB: &aft.RIB{
			Afts: &aft.Afts{
				NextHop: map[uint64]*aft.Afts_NextHop{
					1: {
						Index: ygot.Uint64(1),
					},
				},
			},
		},
	}, {
		desc: "nhg explicit replace",
		inRIBHolder: func() *RIBHolder {
			r := NewRIBHolder("DEFAULT")
			if _, _, err := r.AddNextHopGroup(&aftpb.Afts_NextHopGroupKey{
				Id:           1,
				NextHopGroup: &aftpb.Afts_NextHopGroup{},
			}, false); err != nil {
				t.Fatalf("cannot init test case, %v", err)
			}
			return r
		}(),
		inType: nhg,
		inEntry: &aftpb.Afts_NextHopGroupKey{
			Id: 1,
			NextHopGroup: &aftpb.Afts_NextHopGroup{
				Color: &wpb.UintValue{Value: 42},
			},
		},
		inExplicitReplace: true,
		wantInstalled:     true,
		wantReplace:       true,
		wantRIB: &aft.RIB{
			Afts: &aft.Afts{
				NextHopGroup: map[uint64]*aft.Afts_NextHopGroup{
					1: {
						Id:    ygot.Uint64(1),
						Color: ygot.Uint64(42),
					},
				},
			},
		},
	}, {
		desc: "nh explicit replace",
		inRIBHolder: func() *RIBHolder {
			r := NewRIBHolder("DEFAULT")
			if _, _, err := r.AddNextHop(&aftpb.Afts_NextHopKey{
				Index:   1,
				NextHop: &aftpb.Afts_NextHop{},
			}, false); err != nil {
				t.Fatalf("cannot init test case, %v", err)
			}
			return r
		}(),
		inType: nh,
		inEntry: &aftpb.Afts_NextHopKey{
			Index: 1,
			NextHop: &aftpb.Afts_NextHop{
				IpAddress: &wpb.StringValue{Value: "1.2.3.4"},
			},
		},
		inExplicitReplace: true,
		wantInstalled:     true,
		wantReplace:       true,
		wantRIB: &aft.RIB{
			Afts: &aft.Afts{
				NextHop: map[uint64]*aft.Afts_NextHop{
					1: {
						Index:     ygot.Uint64(1),
						IpAddress: ygot.String("1.2.3.4"),
					},
				},
			},
		},
	}, {
		desc: "ipv4 explicit replace",
		inRIBHolder: func() *RIBHolder {
			r := NewRIBHolder("DEFAULT")
			if _, _, err := r.AddIPv4(&aftpb.Afts_Ipv4EntryKey{
				Prefix: "22.32.42.52/32",
				Ipv4Entry: &aftpb.Afts_Ipv4Entry{
					EntryMetadata: &wpb.BytesValue{Value: []byte{0, 1, 2, 3, 4, 5, 6, 7}},
				},
			}, false); err != nil {
				t.Fatalf("cannot init test case, %v", err)
			}
			return r
		}(),
		inType: ipv4,
		inEntry: &aftpb.Afts_Ipv4EntryKey{
			Prefix:    "22.32.42.52/32",
			Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
		},
		inExplicitReplace: true,
		wantInstalled:     true,
		wantReplace:       true,
		wantRIB: &aft.RIB{
			Afts: &aft.Afts{
				Ipv4Entry: map[string]*aft.Afts_Ipv4Entry{
					"22.32.42.52/32": {
						Prefix: ygot.String("22.32.42.52/32"),
					},
				},
			},
		},
	}, {
		desc:        "nhg id and attributes",
		inRIBHolder: NewRIBHolder("DEFAULT"),
		inType:      nhg,
		inEntry: &aftpb.Afts_NextHopGroupKey{
			Id: 1,
			NextHopGroup: &aftpb.Afts_NextHopGroup{
				NextHop: []*aftpb.Afts_NextHopGroup_NextHopKey{{
					Index: 1,
					NextHop: &aftpb.Afts_NextHopGroup_NextHop{
						Weight: &wpb.UintValue{Value: 1},
					},
				}},
			},
		},
		wantInstalled: true,
		wantRIB: &aft.RIB{
			Afts: &aft.Afts{
				NextHopGroup: map[uint64]*aft.Afts_NextHopGroup{
					1: {
						Id: ygot.Uint64(1),
						NextHop: map[uint64]*aft.Afts_NextHopGroup_NextHop{
							1: {
								Index:  ygot.Uint64(1),
								Weight: ygot.Uint64(1),
							},
						},
					},
				},
			},
		},
	}, {
		desc:        "nh ID only",
		inRIBHolder: NewRIBHolder("DEFAULT"),
		inType:      nh,
		inEntry: &aftpb.Afts_NextHopKey{
			Index:   1,
			NextHop: &aftpb.Afts_NextHop{},
		},
		wantInstalled: true,
		wantRIB: &aft.RIB{
			Afts: &aft.Afts{
				NextHop: map[uint64]*aft.Afts_NextHop{
					1: {
						Index: ygot.Uint64(1),
					},
				},
			},
		},
	}, {
		desc:        "nh id and attributes",
		inRIBHolder: NewRIBHolder("DEFAULT"),
		inType:      nh,
		inEntry: &aftpb.Afts_NextHopKey{
			Index: 1,
			NextHop: &aftpb.Afts_NextHop{
				IpAddress: &wpb.StringValue{Value: "8.8.4.4"},
			},
		},
		wantInstalled: true,
		wantRIB: &aft.RIB{
			Afts: &aft.Afts{
				NextHop: map[uint64]*aft.Afts_NextHop{
					1: {
						Index:     ygot.Uint64(1),
						IpAddress: ygot.String("8.8.4.4"),
					},
				},
			},
		},
	}, {
		desc:        "nil rib, error",
		inRIBHolder: &RIBHolder{},
		inType:      ipv4,
		inEntry: &aftpb.Afts_Ipv4EntryKey{
			Prefix:    "1.0.0.0/24",
			Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
		},
		wantErr: true,
	}, {
		desc:        "MPLS - valid entry",
		inRIBHolder: NewRIBHolder("DEFAULT"),
		inType:      mpls,
		inEntry: &aftpb.Afts_LabelEntryKey{
			Label: &aftpb.Afts_LabelEntryKey_LabelUint64{
				LabelUint64: 42,
			},
			LabelEntry: &aftpb.Afts_LabelEntry{},
		},
		wantInstalled: true,
		wantRIB: &aft.RIB{
			Afts: &aft.Afts{
				LabelEntry: map[aft.Afts_LabelEntry_Label_Union]*aft.Afts_LabelEntry{
					aft.UnionUint32(42): {
						Label: aft.UnionUint32(42),
					},
				},
			},
		},
	}, {
		desc:        "MPLS - invalid entry",
		inRIBHolder: NewRIBHolder("DEFAULT"),
		inType:      mpls,
		inEntry: &aftpb.Afts_LabelEntryKey{
			Label:      &aftpb.Afts_LabelEntryKey_LabelUint64{},
			LabelEntry: &aftpb.Afts_LabelEntry{},
		},
		wantErr: true,
	}, {
		desc: "MPLS - implicit replacae",
		inRIBHolder: func() *RIBHolder {
			r := NewRIBHolder("DEFAULT")
			r.r.GetOrCreateAfts().GetOrCreateLabelEntry(aft.UnionUint32(42))
			return r
		}(),
		inType: mpls,
		inEntry: &aftpb.Afts_LabelEntryKey{
			Label: &aftpb.Afts_LabelEntryKey_LabelUint64{
				LabelUint64: 42,
			},
			LabelEntry: &aftpb.Afts_LabelEntry{},
		},
		wantInstalled: true,
		wantReplace:   true,
		wantRIB: &aft.RIB{
			Afts: &aft.Afts{
				LabelEntry: map[aft.Afts_LabelEntry_Label_Union]*aft.Afts_LabelEntry{
					aft.UnionUint32(42): {
						Label: aft.UnionUint32(42),
					},
				},
			},
		},
	}, {
		desc: "MPLS - explicit replace",
		inRIBHolder: func() *RIBHolder {
			r := NewRIBHolder("DEFAULT")
			r.r.GetOrCreateAfts().GetOrCreateLabelEntry(aft.UnionUint32(42))
			return r
		}(),
		inType: mpls,
		inEntry: &aftpb.Afts_LabelEntryKey{
			Label: &aftpb.Afts_LabelEntryKey_LabelUint64{
				LabelUint64: 42,
			},
			LabelEntry: &aftpb.Afts_LabelEntry{},
		},
		inExplicitReplace: true,
		wantInstalled:     true,
		wantReplace:       true,
		wantRIB: &aft.RIB{
			Afts: &aft.Afts{
				LabelEntry: map[aft.Afts_LabelEntry_Label_Union]*aft.Afts_LabelEntry{
					aft.UnionUint32(42): {
						Label: aft.UnionUint32(42),
					},
				},
			},
		},
	}, {
		desc:        "MPLS - explicit replace - missing",
		inRIBHolder: NewRIBHolder("DEFAULT"),
		inType:      mpls,
		inEntry: &aftpb.Afts_LabelEntryKey{
			Label: &aftpb.Afts_LabelEntryKey_LabelUint64{
				LabelUint64: 42,
			},
			LabelEntry: &aftpb.Afts_LabelEntry{},
		},
		inExplicitReplace: true,
		wantErr:           true,
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			r := tt.inRIBHolder

			var (
				gotInstalled, gotImplicit bool
				err                       error
			)

			switch tt.inType {
			case ipv4:
				// special case for nil test
				if tt.inEntry == nil {
					_, _, err := r.AddIPv4(nil, false)
					if (err != nil) != tt.wantErr {
						t.Fatalf("got unexpected error adding IPv4 entry, got: %v, wantErr? %v", err, tt.wantErr)
					}
					return
				}
				gotInstalled, gotImplicit, err = r.AddIPv4(tt.inEntry.(*aftpb.Afts_Ipv4EntryKey), tt.inExplicitReplace)
			case nhg:
				gotInstalled, gotImplicit, err = r.AddNextHopGroup(tt.inEntry.(*aftpb.Afts_NextHopGroupKey), tt.inExplicitReplace)
			case nh:
				gotInstalled, gotImplicit, err = r.AddNextHop(tt.inEntry.(*aftpb.Afts_NextHopKey), tt.inExplicitReplace)

			case mpls:
				gotInstalled, gotImplicit, err = r.AddMPLS(tt.inEntry.(*aftpb.Afts_LabelEntryKey), tt.inExplicitReplace)
			default:
				t.Fatalf("unsupported test type in Add, %v", tt.inType)
			}

			if (err != nil) != tt.wantErr {
				t.Fatalf("got unexpected error adding entry, got: %v, wantErr? %v", err, tt.wantErr)
			}
			if gotInstalled != tt.wantInstalled {
				t.Fatalf("did not get expected installed bool, got: %v, want: %v", gotInstalled, tt.wantInstalled)
			}
			if gotImplicit != tt.wantReplace {
				t.Fatalf("did not get expected implicit bool, got: %v, want: %v", gotImplicit, tt.wantReplace)
			}

			diffN, err := ygot.Diff(r.r, tt.wantRIB)
			if err != nil {
				t.Fatalf("cannot diff expected RIB with got RIB, %v", err)
			}
			if diffN != nil && len(diffN.Update) != 0 {
				t.Fatalf("did not get expected RIB, diff(as notifications):\n%s", diffN)
			}
		})
	}
}

func TestIndividualDeleteEntryFunctions(t *testing.T) {
	type ipv4entry struct {
		prefix   string
		metadata []byte
		nhg      uint64
		nhgni    string
	}

	type nh struct {
		id uint64
		ip string
	}

	type nhg struct {
		id uint64
		nh []uint64
	}

	type entry struct {
		v4      *ipv4entry
		group   *nhg
		nexthop *nh
	}

	ribEntries := func(e []*entry) *aft.RIB {
		a := &aft.RIB{}
		for _, en := range e {
			switch {
			case en.v4 != nil:
				p := a.GetOrCreateAfts().GetOrCreateIpv4Entry(en.v4.prefix)
				if en.v4.metadata != nil {
					p.EntryMetadata = en.v4.metadata
				}
				if en.v4.nhg != 0 {
					p.NextHopGroup = ygot.Uint64(en.v4.nhg)
				}
				if en.v4.nhgni != "" {
					p.NextHopGroupNetworkInstance = ygot.String(en.v4.nhgni)
				}
			case en.group != nil:
				n := a.GetOrCreateAfts().GetOrCreateNextHopGroup(en.group.id)
				for _, nh := range en.group.nh {
					n.GetOrCreateNextHop(nh)
				}
			case en.nexthop != nil:
				n := a.GetOrCreateAfts().GetOrCreateNextHop(en.nexthop.id)
				if en.nexthop.ip != "" {
					n.IpAddress = ygot.String(en.nexthop.ip)
				}
			}
		}
		return a
	}

	tests := []struct {
		desc        string
		inRIB       *RIBHolder
		inEntry     proto.Message
		wantOK      bool
		wantOrig    ygot.ValidatedGoStruct
		wantErr     bool
		wantPostRIB *RIBHolder
	}{{
		desc:    "nil input",
		inRIB:   &RIBHolder{},
		inEntry: (*aftpb.Afts_Ipv4EntryKey)(nil),
		wantErr: true,
	}, {
		desc: "delete ipv4 entry, no payload",
		inRIB: &RIBHolder{
			r: ribEntries([]*entry{
				{v4: &ipv4entry{prefix: "1.1.1.1/32"}},
			}),
		},
		inEntry: &aftpb.Afts_Ipv4EntryKey{
			Prefix: "1.1.1.1/32",
		},
		wantOK:   true,
		wantOrig: &aft.Afts_Ipv4Entry{Prefix: ygot.String("1.1.1.1/32")},
		wantPostRIB: &RIBHolder{
			r: ribEntries([]*entry{}),
		},
	}, {
		desc: "delete ipv4 entry, matching payload",
		inRIB: &RIBHolder{
			r: ribEntries([]*entry{
				{v4: &ipv4entry{prefix: "1.1.1.1/32", metadata: []byte{0, 1, 2, 3, 4, 5, 6, 7}}},
			}),
		},
		inEntry: &aftpb.Afts_Ipv4EntryKey{
			Prefix: "1.1.1.1/32",
			Ipv4Entry: &aftpb.Afts_Ipv4Entry{
				EntryMetadata: &wpb.BytesValue{Value: []byte{0, 1, 2, 3, 4, 5, 6, 7}},
			},
		},
		wantOK: true,
		wantOrig: &aft.Afts_Ipv4Entry{
			Prefix:        ygot.String("1.1.1.1/32"),
			EntryMetadata: aft.Binary{0, 1, 2, 3, 4, 5, 6, 7},
		},
		wantPostRIB: &RIBHolder{
			r: ribEntries([]*entry{}),
		},
	}, {
		desc: "delete ipv4 entry, mismatched payload",
		inRIB: &RIBHolder{
			r: ribEntries([]*entry{{
				v4: &ipv4entry{prefix: "1.1.1.1/32", metadata: []byte{1, 2, 3, 4}},
			}}),
		},
		inEntry: &aftpb.Afts_Ipv4EntryKey{
			Prefix: "1.1.1.1/32",
			Ipv4Entry: &aftpb.Afts_Ipv4Entry{
				EntryMetadata: &wpb.BytesValue{Value: []byte{2, 3, 4, 5}},
			},
		},
		wantOK: true,
		wantOrig: &aft.Afts_Ipv4Entry{
			Prefix:        ygot.String("1.1.1.1/32"),
			EntryMetadata: aft.Binary{1, 2, 3, 4},
		},
		wantPostRIB: &RIBHolder{},
	}, {
		desc:  "delete ipv4 entry, no such entry",
		inRIB: NewRIBHolder("foo"),
		inEntry: &aftpb.Afts_Ipv4EntryKey{
			Prefix: "1.1.1.1/32",
		},
		wantOrig: (*aft.Afts_Ipv4Entry)(nil),
		wantOK:   true,
	}, {
		desc: "delete nhg",
		inRIB: &RIBHolder{
			r: ribEntries([]*entry{{
				group: &nhg{
					id: 42,
				},
			}, {
				group: &nhg{
					id: 44,
				},
			}}),
		},
		inEntry: &aftpb.Afts_NextHopGroupKey{
			Id:           42,
			NextHopGroup: &aftpb.Afts_NextHopGroup{},
		},
		wantOK: true,
		wantOrig: &aft.Afts_NextHopGroup{
			Id: ygot.Uint64(42),
		},
		wantPostRIB: &RIBHolder{
			r: ribEntries([]*entry{{
				group: &nhg{
					id: 44,
				},
			}}),
		},
	}, {
		desc: "delete nh",
		inRIB: &RIBHolder{
			r: ribEntries([]*entry{{
				nexthop: &nh{id: 1},
			}, {
				nexthop: &nh{id: 2},
			}}),
		},
		inEntry: &aftpb.Afts_NextHopKey{
			Index: 2,
		},
		wantOK: true,
		wantOrig: &aft.Afts_NextHop{
			Index: ygot.Uint64(2),
		},
		wantPostRIB: &RIBHolder{
			r: ribEntries([]*entry{{
				nexthop: &nh{id: 1},
			}}),
		},
	}, {
		desc:  "delete nhg entry, no such entry",
		inRIB: NewRIBHolder("foo"),
		inEntry: &aftpb.Afts_NextHopGroupKey{
			Id: 1,
		},
		wantOrig: (*aft.Afts_NextHopGroup)(nil),
		wantOK:   true,
	}, {
		desc:  "delete nh entry, no such entry",
		inRIB: NewRIBHolder("foo"),
		inEntry: &aftpb.Afts_NextHopKey{
			Index: 42,
		},
		wantOrig: (*aft.Afts_NextHop)(nil),
		wantOK:   true,
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			var (
				ok      bool
				err     error
				gotOrig ygot.ValidatedGoStruct
			)
			switch v := tt.inEntry.(type) {
			case *aftpb.Afts_Ipv4EntryKey:
				ok, gotOrig, err = tt.inRIB.DeleteIPv4(v)
			case *aftpb.Afts_NextHopGroupKey:
				ok, gotOrig, err = tt.inRIB.DeleteNextHopGroup(v)
			case *aftpb.Afts_NextHopKey:
				ok, gotOrig, err = tt.inRIB.DeleteNextHop(v)
			default:
				t.Fatalf("unknown input entry type, %T", err)
			}
			if (err != nil) != tt.wantErr {
				t.Fatalf("did not get expected error, got; %v, wantErr? %v", err, tt.wantErr)
			}
			if ok != tt.wantOK {
				t.Fatalf("did not get expected error status, got: %v, wantErr? %v", err, tt.wantErr)
			}
			if ok {
				if diff := cmp.Diff(gotOrig, tt.wantOrig); diff != "" {
					t.Fatalf("did not get expected original entry, diff(-got,+want):\n%s", diff)
				}
			}
		})
	}
}

func TestConcreteIPv4Proto(t *testing.T) {
	tests := []struct {
		desc    string
		inEntry *aft.Afts_Ipv4Entry
		want    *aftpb.Afts_Ipv4EntryKey
		wantErr bool
	}{{
		desc: "prefix-only",
		inEntry: &aft.Afts_Ipv4Entry{
			Prefix: ygot.String("8.8.8.8/32"),
		},
		want: &aftpb.Afts_Ipv4EntryKey{
			Prefix:    "8.8.8.8/32",
			Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
		},
	}, {
		desc: "with metadata",
		inEntry: &aft.Afts_Ipv4Entry{
			Prefix:        ygot.String("8.8.8.8/32"),
			EntryMetadata: []byte{1, 2, 3, 4},
		},
		want: &aftpb.Afts_Ipv4EntryKey{
			Prefix: "8.8.8.8/32",
			Ipv4Entry: &aftpb.Afts_Ipv4Entry{
				EntryMetadata: &wpb.BytesValue{
					Value: []byte{1, 2, 3, 4},
				},
			},
		},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got, err := concreteIPv4Proto(tt.inEntry)
			if (err != nil) != tt.wantErr {
				t.Fatalf("did not get expec	ted error, got: %v, wantErr? %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(got, tt.want, protocmp.Transform(), cmpopts.EquateEmpty()); diff != "" {
				t.Fatalf("did not get expected proto, diff(-got,+want):\n%s", diff)
			}
		})
	}
}

type hookMode int64

const (
	STORE hookMode = iota
	GNMI
)

func mustPath(s string) *gpb.Path {
	p, err := ygot.StringToStructuredPath(s)
	if err != nil {
		panic(fmt.Sprintf("cannot convert path %s, %v", s, err))
	}
	return p
}

func mustTypedValue(i interface{}) *gpb.TypedValue {
	tv, err := value.FromScalar(i)
	if err != nil {
		panic(fmt.Sprintf("cannot convert value %v, %v", i, err))
	}
	return tv
}

func TestHooks(t *testing.T) {
	type op struct {
		Do  constants.OpType
		TS  int64
		NH  uint64
		NHG uint64
		IP4 string
	}

	tests := []struct {
		desc    string
		inOps   []*op
		storeFn bool
		gnmiFn  bool
		want    []interface{}
	}{{
		desc: "store add hooks",
		inOps: []*op{{
			Do:  constants.Add,
			IP4: "8.8.8.8/32",
		}, {
			Do:  constants.Add,
			NHG: 42,
		}, {
			Do: constants.Add,
			NH: 84,
		}},
		storeFn: true,
		want: []interface{}{
			&op{Do: constants.Add, TS: 0, IP4: "8.8.8.8/32"},
			&op{Do: constants.Add, TS: 1, NHG: 42},
			&op{Do: constants.Add, TS: 2, NH: 84},
		},
	}, {
		desc: "store delete hooks",
		inOps: []*op{{
			Do:  constants.Delete,
			IP4: "8.8.8.8/32",
		}, {
			Do:  constants.Delete,
			IP4: "1.1.1.1/32",
		}},
		storeFn: true,
		want: []interface{}{
			&op{Do: constants.Delete, TS: 0, IP4: "8.8.8.8/32"},
			&op{Do: constants.Delete, TS: 1, IP4: "1.1.1.1/32"},
		},
	}, {
		desc: "store add and delete",
		inOps: []*op{{
			Do:  constants.Add,
			IP4: "1.1.1.1/32",
		}, {
			Do:  constants.Delete,
			IP4: "2.2.2.2/32",
		}},
		want: []interface{}{
			&op{Do: constants.Delete, TS: 0, IP4: "1.1.1.1/32"},
			&op{Do: constants.Delete, TS: 1, IP4: "2.2.2.2/32"},
		},
	}, {
		desc: "gnmi add and delete",
		inOps: []*op{{
			Do:  constants.Add,
			IP4: "1.2.3.4/32",
		}, {
			Do:  constants.Delete,
			IP4: "4.5.6.7/32",
		}},
		gnmiFn: true,
		want: []interface{}{
			&gpb.Notification{
				Timestamp: 42,
				Atomic:    true,
				Prefix:    mustPath("/network-instances/network-instance[name=DEFAULT]/afts/ipv4-unicast/ipv4-entry[prefix=1.2.3.4/32]"),
				Update: []*gpb.Update{{
					Path: mustPath("/prefix"),
					Val:  mustTypedValue("1.2.3.4/32"),
				}, {
					Path: mustPath("state/prefix"),
					Val:  mustTypedValue("1.2.3.4/32"),
				}, {
					Path: mustPath("state/next-hop-group"),
					Val:  mustTypedValue(uint64(1)),
				}},
			},
			&gpb.Notification{
				Timestamp: 42,
				Atomic:    true,
				Delete: []*gpb.Path{
					mustPath("/network-instances/network-instance[name=DEFAULT]/afts/ipv4-unicast/ipv4-entry[prefix=4.5.6.7/32]"),
				},
			},
		},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got := []interface{}{}

			tsFn := func() int64 {
				return int64(len(got))
			}
			store := func(o constants.OpType, _ int64, _ string, gs ygot.ValidatedGoStruct) {
				switch t := gs.(type) {
				case *aft.Afts_Ipv4Entry:
					got = append(got, &op{Do: o, TS: tsFn(), IP4: t.GetPrefix()})
				case *aft.Afts_NextHopGroup:
					got = append(got, &op{Do: o, TS: tsFn(), NHG: t.GetId()})
				case *aft.Afts_NextHop:
					got = append(got, &op{Do: o, TS: tsFn(), NH: t.GetIndex()})
				}
			}

			gnmiNoti := func(o constants.OpType, _ int64, ni string, gs ygot.ValidatedGoStruct) {
				if o == constants.Delete {
					switch t := gs.(type) {
					case *aft.Afts_Ipv4Entry:
						got = append(got, &gpb.Notification{
							Delete: []*gpb.Path{
								mustPath(fmt.Sprintf("/network-instances/network-instance[name=%s]/afts/ipv4-unicast/ipv4-entry[prefix=%s]", ni, t.GetPrefix())),
							},
							Timestamp: 42,
							Atomic:    true,
						})
					case *aft.Afts_NextHop:
						got = append(got, &gpb.Notification{
							Delete: []*gpb.Path{
								mustPath(fmt.Sprintf("/network-instances/network-instance[name=%s]/afts/next-hop-groups/next-hop-group[id=%d]", ni, t.GetIndex())),
							},
							Timestamp: 42,
							Atomic:    true,
						})
					case *aft.Afts_NextHopGroup:
						got = append(got, &gpb.Notification{
							Delete: []*gpb.Path{
								mustPath(fmt.Sprintf("/network-instances/network-instance[name=%s]/afts/next-hops/next-hop[index=%d]", ni, t.GetId())),
							},
							Timestamp: 42,
							Atomic:    true,
						})
					}
					return
				}

				// Technically you could just diff to the existing entry, but atomic indicates that you're going to update all of these
				// paths at the same time every time, so just go with that so that we trade bandwidth for CPU cycles. This is probably
				// the right thing to do w.r.t latency of update.
				var ns []*gpb.Notification
				var err error
				switch t := gs.(type) {
				case *aft.Afts_Ipv4Entry:
					ns, err = ygot.TogNMINotifications(gs, 42, ygot.GNMINotificationsConfig{
						UsePathElem:    true,
						PathElemPrefix: mustPath(fmt.Sprintf("/network-instances/network-instance[name=%s]/afts/ipv4-unicast/ipv4-entry[prefix=%s]", ni, t.GetPrefix())).Elem,
					})
				case *aft.Afts_NextHopGroup:
					ns, err = ygot.TogNMINotifications(gs, 42, ygot.GNMINotificationsConfig{
						UsePathElem:    true,
						PathElemPrefix: mustPath(fmt.Sprintf("/network-instances/network-instance[name=%s]/afts/nexthop-groups/nexthop-group[id=%d]", ni, t.GetId())).Elem,
					})
				case *aft.Afts_NextHop:
					ns, err = ygot.TogNMINotifications(gs, 42, ygot.GNMINotificationsConfig{
						UsePathElem:    true,
						PathElemPrefix: mustPath(fmt.Sprintf("/network-instances/network-instance[name=%s]/afts/nexthops/nexthop[index=%d]", ni, t.GetIndex())).Elem,
					})
				}
				if err != nil {
					t.Fatalf("cannot generate notifications, %v", err)
				}
				if len(ns) != 1 {
					t.Fatalf("wrong number of notifications, got: %d, want: 1", len(ns))
				}
				ns[0].Atomic = true
				got = append(got, ns[0])
			}

			r := New("DEFAULT")
			// override the default check function.
			r.niRIB["DEFAULT"].checkFn = nil

			// do setup before we start, we can't delete or modify entries
			// that don't exist, so remove them.
			for _, o := range tt.inOps {
				switch {
				case o.IP4 != "":
					switch o.Do {
					case constants.Replace, constants.Delete:
						if _, _, err := r.niRIB[r.defaultName].AddIPv4(&aftpb.Afts_Ipv4EntryKey{
							Prefix:    o.IP4,
							Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
						}, false); err != nil {
							t.Fatalf("cannot pre-add IPv4 entry %s: %v", o.IP4, err)
						}
					}
				case o.NHG != 0:
					switch o.Do {
					case constants.Replace, constants.Delete:
						if _, _, err := r.niRIB[r.defaultName].AddNextHopGroup(&aftpb.Afts_NextHopGroupKey{
							Id:           o.NHG,
							NextHopGroup: &aftpb.Afts_NextHopGroup{},
						}, false); err != nil {
							t.Fatalf("cannot add NHG entry %d, %v", o.NHG, err)
						}
					}
				case o.NH != 0:
					switch o.Do {
					case constants.Replace, constants.Delete:
						if _, _, err := r.niRIB[r.defaultName].AddNextHop(&aftpb.Afts_NextHopKey{
							Index:   o.NH,
							NextHop: &aftpb.Afts_NextHop{},
						}, false); err != nil {
							t.Fatalf("cannot add NH entry %d, %v", o.NH, err)
						}
					}
				}
			}

			if tt.storeFn {
				r.SetPostChangeHook(store)
			}

			if tt.gnmiFn {
				r.SetPostChangeHook(gnmiNoti)
			}

			for _, o := range tt.inOps {
				switch {
				case o.IP4 != "":
					switch o.Do {
					case constants.Add:
						if _, _, err := r.niRIB[r.defaultName].AddIPv4(&aftpb.Afts_Ipv4EntryKey{
							Prefix: o.IP4,
							Ipv4Entry: &aftpb.Afts_Ipv4Entry{
								NextHopGroup: &wpb.UintValue{Value: 1},
							},
						}, false); err != nil {
							t.Fatalf("cannot add IPv4 entry %s: %v", o.IP4, err)
						}
					case constants.Delete:
						if _, _, err := r.niRIB[r.defaultName].DeleteIPv4(&aftpb.Afts_Ipv4EntryKey{
							Prefix: o.IP4,
						}); err != nil {
							t.Fatalf("cannote delete IPv4 entry %s: %v", o.IP4, err)
						}
					}
				case o.NHG != 0:
					switch o.Do {
					case constants.Add:
						if _, _, err := r.niRIB[r.defaultName].AddNextHopGroup(&aftpb.Afts_NextHopGroupKey{
							Id:           o.NHG,
							NextHopGroup: &aftpb.Afts_NextHopGroup{},
						}, false); err != nil {
							t.Fatalf("cannot add NHG entry %d, %v", o.NHG, err)
						}
					}
				case o.NH != 0:
					switch o.Do {
					case constants.Add:
						if _, _, err := r.niRIB[r.defaultName].AddNextHop(&aftpb.Afts_NextHopKey{
							Index:   o.NH,
							NextHop: &aftpb.Afts_NextHop{},
						}, false); err != nil {
							t.Fatalf("cannot add NH entry %d, %v", o.NH, err)
						}
					}
				}
			}

			if tt.storeFn {
				if diff := cmp.Diff(got, tt.want, cmpopts.IgnoreUnexported(op{})); diff != "" {
					t.Fatalf("did not get expected queue, diff(-got,+want):\n%s", diff)
				}
			}

			if tt.gnmiFn {
				gotN := []*gpb.Notification{}
				for _, n := range got {
					gotN = append(gotN, n.(*gpb.Notification))
				}

				wantN := []*gpb.Notification{}
				for _, n := range tt.want {
					wantN = append(wantN, n.(*gpb.Notification))
				}

				if !testutil.NotificationSetEqual(gotN, wantN) {
					t.Fatalf("did not get expected notifications, diff(-got,+want):\n%s", cmp.Diff(gotN, wantN, protocmp.Transform()))
				}
			}
		})
	}
}

func TestCanResolve(t *testing.T) {
	tests := []struct {
		desc             string
		inRIB            *RIB
		inNI             string
		inCand           *aft.RIB
		want             bool
		wantErrSubstring string
	}{{
		desc:             "nil input",
		inRIB:            New(defName),
		want:             false,
		wantErrSubstring: "invalid nil candidate",
	}, {
		desc:  "ipv6 entry - invalid",
		inRIB: New(defName),
		inNI:  defName,
		inCand: func() *aft.RIB {
			r := &aft.RIB{}
			r.GetOrCreateAfts().GetOrCreateIpv6Entry("ff00::1/128")
			return r
		}(),
		wantErrSubstring: "IPv6 entries are unsupported",
	}, {

		desc:  "Ethernet - invalid",
		inRIB: New(defName),
		inNI:  defName,
		inCand: func() *aft.RIB {
			r := &aft.RIB{}
			r.GetOrCreateAfts().GetOrCreateMacEntry("1a:ce:8e:df:7b:40")
			return r
		}(),
		wantErrSubstring: "ethernet MAC entries are unsupported",
	}, {

		desc:  "PBR - invalid",
		inRIB: New(defName),
		inNI:  defName,
		inCand: func() *aft.RIB {
			r := &aft.RIB{}
			r.GetOrCreateAfts().GetOrCreatePolicyForwardingEntry(42)
			return r
		}(),
		wantErrSubstring: "PBR entries are unsupported",
	}, {
		desc:  "empty candidate - invalid",
		inRIB: New(defName),
		inNI:  defName,
		inCand: &aft.RIB{
			Afts: &aft.Afts{},
		},
		wantErrSubstring: "no entries in specified candidate",
	}, {
		desc:  "multiple entries in candidate - invalid",
		inRIB: New(defName),
		inNI:  defName,
		inCand: func() *aft.RIB {
			r := &aft.RIB{}
			r.GetOrCreateAfts().GetOrCreateIpv4Entry("1.1.1.1")
			r.GetOrCreateAfts().GetOrCreateNextHop(1)
			return r
		}(),
		wantErrSubstring: "multiple entries are unsupported",
	}, {
		desc:  "next-hop can be successfully resolved",
		inRIB: New(defName),
		inNI:  defName,
		inCand: func() *aft.RIB {
			r := &aft.RIB{}
			n := r.GetOrCreateAfts().GetOrCreateNextHop(1)
			n.IpAddress = ygot.String("1.1.1.1")
			return r
		}(),
		want: true,
	}, {
		desc:  "next-hop with index 0 - invalid",
		inRIB: New(defName),
		inNI:  defName,
		inCand: func() *aft.RIB {
			r := &aft.RIB{}
			n := r.GetOrCreateAfts().GetOrCreateNextHop(0)
			n.IpAddress = ygot.String("1.1.1.1")
			return r
		}(),
		wantErrSubstring: "invalid index zero for next-hop",
	}, {
		desc:  "resolve in invalid network-instance",
		inRIB: New(defName),
		inNI:  "does-not-exist",
		inCand: func() *aft.RIB {
			r := &aft.RIB{}
			r.GetOrCreateAfts().GetOrCreateNextHopGroup(42)
			return r
		}(),
		wantErrSubstring: "invalid network-instance does-not-exist",
	}, {
		desc:  "nhg with index 0 - invalid",
		inRIB: New(defName),
		inNI:  defName,
		inCand: func() *aft.RIB {
			r := &aft.RIB{}
			r.GetOrCreateAfts().GetOrCreateNextHopGroup(0)
			return r
		}(),
		wantErrSubstring: "invalid zero-index NHG",
	}, {
		desc:  "nhg with nh with index 0 - invalid",
		inRIB: New(defName),
		inNI:  defName,
		inCand: func() *aft.RIB {
			r := &aft.RIB{}
			r.GetOrCreateAfts().GetOrCreateNextHopGroup(1).GetOrCreateNextHop(0)
			return r
		}(),
		wantErrSubstring: "invalid zero index NH in NHG 1",
	}, {
		desc:  "nhg refers to missing nh",
		inRIB: New(defName),
		inNI:  defName,
		inCand: func() *aft.RIB {
			r := &aft.RIB{}
			r.GetOrCreateAfts().GetOrCreateNextHopGroup(1).GetOrCreateNextHop(1)
			return r
		}(),
		want: false,
	}, {
		desc: "nhg refers to valid nhs",
		inRIB: func() *RIB {
			r := New(defName)
			r.niRIB[defName].r = &aft.RIB{}
			cc := r.niRIB[defName].r.GetOrCreateAfts()
			cc.GetOrCreateNextHop(1)
			cc.GetOrCreateNextHop(2)
			return r
		}(),
		inCand: func() *aft.RIB {
			r := &aft.RIB{}
			r.GetOrCreateAfts().GetOrCreateNextHopGroup(1).GetOrCreateNextHop(1)
			r.GetOrCreateAfts().GetOrCreateNextHopGroup(1).GetOrCreateNextHop(2)
			return r
		}(),
		want: true,
	}, {
		desc:  "zero index nhg in ipv4 entry - invalid",
		inRIB: New(defName),
		inCand: func() *aft.RIB {
			r := &aft.RIB{}
			i := r.GetOrCreateAfts().GetOrCreateIpv4Entry("1.1.1.1/32")
			i.NextHopGroup = ygot.Uint64(0)
			return r
		}(),
		wantErrSubstring: "invalid zero-index NHG in IPv4Entry",
	}, {
		desc:  "resolve ipv4 entry in nil NI - invalid",
		inRIB: New(defName),
		inCand: func() *aft.RIB {
			r := &aft.RIB{}
			i := r.GetOrCreateAfts().GetOrCreateIpv4Entry("1.1.1.1/32")
			i.NextHopGroup = ygot.Uint64(1)
			i.NextHopGroupNetworkInstance = ygot.String("404")
			return r
		}(),
		wantErrSubstring: "invalid unknown network-instance for entry",
	}, {
		desc: "resolve ipv4 entry in default NI - implicit name",
		inRIB: func() *RIB {
			r := New(defName)
			r.niRIB[defName].r = &aft.RIB{}
			cc := r.niRIB[defName].r.GetOrCreateAfts()
			cc.GetOrCreateNextHopGroup(1)
			return r
		}(),
		inCand: func() *aft.RIB {
			r := &aft.RIB{}
			i := r.GetOrCreateAfts().GetOrCreateIpv4Entry("1.1.1.1/32")
			i.NextHopGroup = ygot.Uint64(1)
			return r
		}(),
		want: true,
	}, {
		desc: "resolve ipv4 entry in default NI - explicit name",
		inRIB: func() *RIB {
			r := New(defName)
			r.niRIB[defName].r = &aft.RIB{}
			cc := r.niRIB[defName].r.GetOrCreateAfts()
			cc.GetOrCreateNextHopGroup(1)
			return r
		}(),
		inCand: func() *aft.RIB {
			r := &aft.RIB{}
			i := r.GetOrCreateAfts().GetOrCreateIpv4Entry("1.1.1.1/32")
			i.NextHopGroup = ygot.Uint64(1)
			i.NextHopGroupNetworkInstance = ygot.String(defName)
			return r
		}(),
		want: true,
	}, {
		desc: "resolve ipv4 entry in non-default NI",
		inRIB: func() *RIB {
			r := New(defName)
			r.niRIB["vrf-42"] = NewRIBHolder("vrf-42")
			r.niRIB["vrf-42"].r = &aft.RIB{}
			cc := r.niRIB["vrf-42"].r.GetOrCreateAfts()
			cc.GetOrCreateNextHopGroup(1)
			return r
		}(),
		inCand: func() *aft.RIB {
			r := &aft.RIB{}
			i := r.GetOrCreateAfts().GetOrCreateIpv4Entry("1.1.1.1/32")
			i.NextHopGroup = ygot.Uint64(1)
			i.NextHopGroupNetworkInstance = ygot.String("vrf-42")
			return r
		}(),
		want: true,
	}, {
		desc: "resolve ipv4 entry in non-default NI - not found",
		inRIB: func() *RIB {
			r := New(defName)
			r.niRIB["vrf-42"] = NewRIBHolder("vrf-42")
			r.niRIB["vrf-42"].r = &aft.RIB{}
			r.niRIB["vrf-42"].r.GetOrCreateAfts()
			// explicitly don't create NHG
			return r
		}(),
		inCand: func() *aft.RIB {
			r := &aft.RIB{}
			i := r.GetOrCreateAfts().GetOrCreateIpv4Entry("1.1.1.1/32")
			i.NextHopGroup = ygot.Uint64(1)
			i.NextHopGroupNetworkInstance = ygot.String("vrf-42")
			return r
		}(),
	}, {
		desc: "MPLS - found in default NI specified explicitly",
		inNI: defName,
		inRIB: func() *RIB {
			r := New(defName)
			r.niRIB[defName].r = &aft.RIB{}
			cc := r.niRIB[defName].r.GetOrCreateAfts()
			cc.GetOrCreateNextHopGroup(1)
			return r
		}(),
		inCand: func() *aft.RIB {
			r := &aft.RIB{}
			i := r.GetOrCreateAfts().GetOrCreateLabelEntry(aft.UnionUint32(42))
			i.NextHopGroup = ygot.Uint64(1)
			i.NextHopGroupNetworkInstance = ygot.String(defName)
			return r
		}(),
		want: true,
	}, {
		desc: "MPLS - found in default NI specified implicitly",
		inRIB: func() *RIB {
			r := New(defName)
			r.niRIB[defName].r = &aft.RIB{}
			cc := r.niRIB[defName].r.GetOrCreateAfts()
			cc.GetOrCreateNextHopGroup(1)
			return r
		}(),
		inCand: func() *aft.RIB {
			r := &aft.RIB{}
			i := r.GetOrCreateAfts().GetOrCreateLabelEntry(aft.UnionUint32(42))
			i.NextHopGroup = ygot.Uint64(1)
			i.NextHopGroupNetworkInstance = ygot.String(defName)
			return r
		}(),
		want: true,
	}, {
		desc: "MPLS - not found",
		inNI: defName,
		inRIB: func() *RIB {
			r := New(defName)
			r.niRIB[defName].r = &aft.RIB{}
			return r
		}(),
		inCand: func() *aft.RIB {
			r := &aft.RIB{}
			i := r.GetOrCreateAfts().GetOrCreateLabelEntry(aft.UnionUint32(42))
			i.NextHopGroup = ygot.Uint64(1)
			i.NextHopGroupNetworkInstance = ygot.String(defName)
			return r
		}(),
		want: false,
	}, {
		desc: "MPLS found in a non-default NI",
		inNI: "MPLS-NI",
		inRIB: func() *RIB {
			r := New("MPLS-NI")
			r.niRIB["MPLS-NI"].r = &aft.RIB{}
			cc := r.niRIB["MPLS-NI"].r.GetOrCreateAfts()
			cc.GetOrCreateNextHopGroup(1)
			return r
		}(),
		inCand: func() *aft.RIB {
			r := &aft.RIB{}
			i := r.GetOrCreateAfts().GetOrCreateLabelEntry(aft.UnionUint32(42))
			i.NextHopGroup = ygot.Uint64(1)
			i.NextHopGroupNetworkInstance = ygot.String("MPLS-NI")
			return r
		}(),
		want: true,
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got, err := tt.inRIB.canResolve(tt.inNI, tt.inCand)
			if err != nil {
				if diff := errdiff.Substring(err, tt.wantErrSubstring); diff != "" {
					t.Fatalf("did not get expected error, %s", diff)
				}
				return
			}
			if got != tt.want {
				t.Fatalf("did not get expected canResolve, got: %v, want: %v", got, tt.want)
			}
		})
	}
}

func sortResultSlice(s []*OpResult) {
	sort.Slice(s, func(i, j int) bool { return s[i].ID < s[j].ID })
}

func TestRIBAddEntry(t *testing.T) {
	tests := []struct {
		desc               string
		inRIB              *RIB
		inNI               string
		inOp               *spb.AFTOperation
		wantOKs            []*OpResult
		wantFails          []*OpResult
		wantErr            bool
		wantReferenceCheck func(r *RIB) error
	}{{
		desc:  "single nexthop - no pending",
		inRIB: New(defName),
		inNI:  defName,
		inOp: &spb.AFTOperation{
			Id: 42,
			Entry: &spb.AFTOperation_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index:   42,
					NextHop: &aftpb.Afts_NextHop{},
				},
			},
		},
		wantOKs: []*OpResult{{
			ID: 42,
			Op: &spb.AFTOperation{
				Id: 42,
				Entry: &spb.AFTOperation_NextHop{
					NextHop: &aftpb.Afts_NextHopKey{
						Index:   42,
						NextHop: &aftpb.Afts_NextHop{},
					},
				},
			},
		}},
	}, {
		desc:  "unsupported type - ethernet",
		inRIB: New(defName),
		inNI:  defName,
		inOp: &spb.AFTOperation{
			Id: 42,
			Entry: &spb.AFTOperation_MacEntry{
				MacEntry: &aftpb.Afts_MacEntryKey{
					MacAddress: "82:33:1b:e9:a4:01",
				},
			},
		},
		wantErr: true,
	}, {
		desc:  "invalid nh",
		inRIB: New(defName),
		inNI:  defName,
		inOp: &spb.AFTOperation{
			Id: 42,
			Entry: &spb.AFTOperation_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index: 42,
					// nil nexthop
				},
			},
		},
		wantFails: []*OpResult{{
			ID: 42,
			Op: &spb.AFTOperation{
				Id: 42,
				Entry: &spb.AFTOperation_NextHop{
					NextHop: &aftpb.Afts_NextHopKey{
						Index: 42,
					},
				},
			},
			Error: "invalid NextHopGroup, could not parse a field within the list gribi_aft.Afts.next_hop , nil list member in field gribi_aft.Afts.NextHopKey.next_hop, <nil>",
		}},
	}, {
		desc: "nh makes nhg resolvable",
		inRIB: func() *RIB {
			r := New(defName)
			r.pendingEntries[1] = &pendingEntry{
				ni: defName,
				op: &spb.AFTOperation{
					Id: 1,
					Entry: &spb.AFTOperation_NextHopGroup{
						NextHopGroup: &aftpb.Afts_NextHopGroupKey{
							Id: 1,
							NextHopGroup: &aftpb.Afts_NextHopGroup{
								NextHop: []*aftpb.Afts_NextHopGroup_NextHopKey{{
									Index:   1,
									NextHop: &aftpb.Afts_NextHopGroup_NextHop{},
								}},
							},
						},
					},
				},
			}
			return r
		}(),
		inNI: defName,
		inOp: &spb.AFTOperation{
			Id: 2,
			Entry: &spb.AFTOperation_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index:   1,
					NextHop: &aftpb.Afts_NextHop{},
				},
			},
		},
		wantOKs: []*OpResult{{
			ID: 1,
			Op: &spb.AFTOperation{
				Id: 1,
				Entry: &spb.AFTOperation_NextHopGroup{
					NextHopGroup: &aftpb.Afts_NextHopGroupKey{
						Id: 1,
						NextHopGroup: &aftpb.Afts_NextHopGroup{
							NextHop: []*aftpb.Afts_NextHopGroup_NextHopKey{{
								Index:   1,
								NextHop: &aftpb.Afts_NextHopGroup_NextHop{},
							}},
						},
					},
				},
			},
		}, {
			ID: 2,
			Op: &spb.AFTOperation{
				Id: 2,
				Entry: &spb.AFTOperation_NextHop{
					NextHop: &aftpb.Afts_NextHopKey{
						Index:   1,
						NextHop: &aftpb.Afts_NextHop{},
					},
				},
			},
		}},
		wantReferenceCheck: func(r *RIB) error {
			ni, ok := r.NetworkInstanceRIB(defName)
			if !ok {
				return fmt.Errorf("invalid network instance, %s", defName)
			}

			nhIndex := uint64(1)
			if !r.niRIB[defName].nhReferenced(nhIndex) {
				return fmt.Errorf("got invalid nhReferenced result for NHG %d, got: false, want: true", nhIndex)
			}

			if got, want := ni.refCounts.NextHop[nhIndex], uint64(1); got != want {
				return fmt.Errorf("got invalid reference count for next-hop %d in network-instance %s, got: %d, want: %d", nhIndex, defName, got, want)
			}

			return nil
		},
	}, {
		desc: "nh does not make nhg resolvable",
		inRIB: func() *RIB {
			r := New(defName)
			r.pendingEntries[1] = &pendingEntry{
				ni: defName,
				op: &spb.AFTOperation{
					Id: 1,
					Entry: &spb.AFTOperation_NextHopGroup{
						NextHopGroup: &aftpb.Afts_NextHopGroupKey{
							Id: 1,
							NextHopGroup: &aftpb.Afts_NextHopGroup{
								NextHop: []*aftpb.Afts_NextHopGroup_NextHopKey{{
									// waiting on NH 42
									Index:   42,
									NextHop: &aftpb.Afts_NextHopGroup_NextHop{},
								}},
							},
						},
					},
				},
			}
			return r
		}(),
		inNI: defName,
		// install NH 1
		inOp: &spb.AFTOperation{
			Id: 2,
			Entry: &spb.AFTOperation_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index:   1,
					NextHop: &aftpb.Afts_NextHop{},
				},
			},
		},
		// only NH 1 install is OK
		wantOKs: []*OpResult{{
			ID: 2,
			Op: &spb.AFTOperation{
				Id: 2,
				Entry: &spb.AFTOperation_NextHop{
					NextHop: &aftpb.Afts_NextHopKey{
						Index:   1,
						NextHop: &aftpb.Afts_NextHop{},
					},
				},
			},
		}},
	}, {
		desc: "nh makes nhg makes ipv4 resolvable",
		inRIB: func() *RIB {
			r := New(defName)
			// op#1: NHG ID 1 pending on NH index = 1 showing up
			r.pendingEntries[1] = &pendingEntry{
				ni: defName,
				op: &spb.AFTOperation{
					Id: 1,
					Entry: &spb.AFTOperation_NextHopGroup{
						NextHopGroup: &aftpb.Afts_NextHopGroupKey{
							Id: 1,
							NextHopGroup: &aftpb.Afts_NextHopGroup{
								NextHop: []*aftpb.Afts_NextHopGroup_NextHopKey{{
									Index:   1,
									NextHop: &aftpb.Afts_NextHopGroup_NextHop{},
								}},
							},
						},
					},
				},
			}

			// op#2: IPv4 prefix 42.42.42.42/32 pending on NNG index 1 showing up (q'd above)
			r.pendingEntries[2] = &pendingEntry{
				ni: defName,
				op: &spb.AFTOperation{
					Id: 2,
					Entry: &spb.AFTOperation_Ipv4{
						Ipv4: &aftpb.Afts_Ipv4EntryKey{
							Prefix: "42.42.42.42/32",
							Ipv4Entry: &aftpb.Afts_Ipv4Entry{
								NextHopGroup: &wpb.UintValue{Value: 1},
							},
						},
					},
				},
			}
			return r
		}(),
		inNI: defName,
		// op#3 -> NH = 1 shows up, makes NHG 1 and 42.42.42.42/32 resolvable.
		inOp: &spb.AFTOperation{
			Id: 3,
			Entry: &spb.AFTOperation_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index:   1,
					NextHop: &aftpb.Afts_NextHop{},
				},
			},
		},
		wantOKs: []*OpResult{{
			ID: 1,
			Op: &spb.AFTOperation{
				Id: 1,
				Entry: &spb.AFTOperation_NextHopGroup{
					NextHopGroup: &aftpb.Afts_NextHopGroupKey{
						Id: 1,
						NextHopGroup: &aftpb.Afts_NextHopGroup{
							NextHop: []*aftpb.Afts_NextHopGroup_NextHopKey{{
								Index:   1,
								NextHop: &aftpb.Afts_NextHopGroup_NextHop{},
							}},
						},
					},
				},
			},
		}, {
			ID: 2,
			Op: &spb.AFTOperation{
				Id: 2,
				Entry: &spb.AFTOperation_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix: "42.42.42.42/32",
						Ipv4Entry: &aftpb.Afts_Ipv4Entry{
							NextHopGroup: &wpb.UintValue{Value: 1},
						},
					},
				},
			},
		}, {
			ID: 3,
			Op: &spb.AFTOperation{
				Id: 3,
				Entry: &spb.AFTOperation_NextHop{
					NextHop: &aftpb.Afts_NextHopKey{
						Index:   1,
						NextHop: &aftpb.Afts_NextHop{},
					},
				},
			},
		}},
		wantReferenceCheck: func(r *RIB) error {
			ni, ok := r.NetworkInstanceRIB(defName)
			if !ok {
				return fmt.Errorf("invalid network instance, %s", defName)
			}

			nhIndex := uint64(1)
			if !r.niRIB[defName].nhReferenced(nhIndex) {
				return fmt.Errorf("got invalid nhReferenced result for NHG %d, got: false, want: true", nhIndex)
			}
			if got, want := ni.refCounts.NextHop[nhIndex], uint64(1); got != want {
				return fmt.Errorf("got invalid reference count for next-hop %d in network-instance %s, got: %d, want: %d", nhIndex, defName, got, want)
			}

			nhgIndex := uint64(1)
			if !r.niRIB[defName].nhgReferenced(nhgIndex) {
				return fmt.Errorf("got invalid nhgReferenced result for NHG %d, got: false, want: true", nhIndex)
			}
			if got, want := ni.refCounts.NextHopGroup[nhgIndex], uint64(1); got != want {
				return fmt.Errorf("got invalid reference count for next-hop-group %d in network-instance %s, got: %d, want: %d", nhgIndex, defName, got, want)
			}

			return nil
		},
	}, {
		desc: "check function that always returns false",
		inRIB: func() *RIB {
			r := New(defName)
			failEntry := func(_ constants.OpType, _ *aft.RIB) (bool, error) {
				return false, nil
			}
			r.niRIB[defName].checkFn = failEntry
			return r
		}(),
		inNI: defName,
		inOp: &spb.AFTOperation{
			Id: 42,
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix: "1.1.1.1/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{
						NextHopGroup: &wpb.UintValue{Value: 42},
					},
				},
			},
		},
		// No result, and no error, since we don't have anything to return.
	}, {
		desc: "check function that always returns an error",
		inRIB: func() *RIB {
			r := New(defName)
			failEntry := func(_ constants.OpType, _ *aft.RIB) (bool, error) {
				return false, errors.New("cannot install")
			}
			r.niRIB[defName].checkFn = failEntry
			return r
		}(),
		inNI: defName,
		inOp: &spb.AFTOperation{
			Id: 42,
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix: "1.1.1.1/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{
						NextHopGroup: &wpb.UintValue{Value: 42},
					},
				},
			},
		},
		// error makes this entry fail.
		wantFails: []*OpResult{{
			ID: 42,
			Op: &spb.AFTOperation{
				Id: 42,
				Entry: &spb.AFTOperation_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix: "1.1.1.1/32",
						Ipv4Entry: &aftpb.Afts_Ipv4Entry{
							NextHopGroup: &wpb.UintValue{Value: 42},
						},
					},
				},
			},
			Error: "cannot install",
		}},
	}, {
		desc: "IPv4 prefix pointing to a NHG in another network-instance",
		inRIB: func() *RIB {
			r := New(defName)
			if err := r.AddNetworkInstance("VRF-1"); err != nil {
				t.Fatalf(fmt.Sprintf("cannot add network instance, %v", err))
			}

			_, fails, err := r.AddEntry(defName, &spb.AFTOperation{
				Id: 1,
				Entry: &spb.AFTOperation_NextHop{
					NextHop: &aftpb.Afts_NextHopKey{
						Index:   1,
						NextHop: &aftpb.Afts_NextHop{},
					},
				},
			})
			if err != nil || len(fails) != 0 {
				t.Fatalf(fmt.Sprintf("cannot build test case, cannot add NH, got: %v or failed ops %d", err, len(fails)))
			}

			_, fails, err = r.AddEntry(defName, &spb.AFTOperation{
				Id: 1,
				Entry: &spb.AFTOperation_NextHopGroup{
					NextHopGroup: &aftpb.Afts_NextHopGroupKey{
						Id: 1,
						NextHopGroup: &aftpb.Afts_NextHopGroup{
							NextHop: []*aftpb.Afts_NextHopGroup_NextHopKey{{
								Index:   1,
								NextHop: &aftpb.Afts_NextHopGroup_NextHop{},
							}},
						},
					},
				},
			})
			if err != nil || len(fails) != 0 {
				t.Fatalf("cannot build test case, cannot add NHG, got: %v or failed ops %d", err, len(fails))
			}

			return r
		}(),
		inNI: defName,
		inOp: &spb.AFTOperation{
			Id:              42,
			NetworkInstance: "VRF-1",
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix: "1.1.1.1/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{
						NextHopGroup:                &wpb.UintValue{Value: 1},
						NextHopGroupNetworkInstance: &wpb.StringValue{Value: defName},
					},
				},
			},
		},
		wantOKs: []*OpResult{{
			ID: 42,
			Op: &spb.AFTOperation{
				Id:              42,
				NetworkInstance: "VRF-1",
				Entry: &spb.AFTOperation_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix: "1.1.1.1/32",
						Ipv4Entry: &aftpb.Afts_Ipv4Entry{
							NextHopGroup:                &wpb.UintValue{Value: 1},
							NextHopGroupNetworkInstance: &wpb.StringValue{Value: defName},
						},
					},
				},
			},
		}},
		wantReferenceCheck: func(r *RIB) error {
			ni, ok := r.NetworkInstanceRIB(defName)
			if !ok {
				return fmt.Errorf("invalid network instance, %s", defName)
			}

			nhIndex := uint64(1)
			if got, want := ni.refCounts.NextHop[nhIndex], uint64(1); got != want {
				return fmt.Errorf("got invalid reference count for next-hop %d in network-instance %s, got: %d, want: %d", nhIndex, defName, got, want)
			}

			nhgIndex := uint64(1)
			if got, want := ni.refCounts.NextHopGroup[nhgIndex], uint64(1); got != want {
				return fmt.Errorf("got invalid reference count for next-hop-group %d in network-instance %s, got: %d, want: %d", nhgIndex, defName, got, want)
			}

			return nil
		},
	}, {
		desc: "Two IPv4 prefixes pointing to a NHG in another network-instance, multiple NHs",
		inRIB: func() *RIB {
			r := New(defName)
			if err := r.AddNetworkInstance("VRF-1"); err != nil {
				t.Fatalf("cannot add network instance, %v", err)
			}

			_, fails, err := r.AddEntry(defName, &spb.AFTOperation{
				Id:              1,
				NetworkInstance: defName,
				Entry: &spb.AFTOperation_NextHop{
					NextHop: &aftpb.Afts_NextHopKey{
						Index:   1,
						NextHop: &aftpb.Afts_NextHop{},
					},
				},
			})
			if err != nil || len(fails) != 0 {
				t.Fatalf("cannot build test case, cannot add NH, got: %v or failed ops %d", err, len(fails))
			}

			_, fails, err = r.AddEntry(defName, &spb.AFTOperation{
				Id:              1,
				NetworkInstance: defName,
				Entry: &spb.AFTOperation_NextHopGroup{
					NextHopGroup: &aftpb.Afts_NextHopGroupKey{
						Id: 1,
						NextHopGroup: &aftpb.Afts_NextHopGroup{
							NextHop: []*aftpb.Afts_NextHopGroup_NextHopKey{{
								Index:   1,
								NextHop: &aftpb.Afts_NextHopGroup_NextHop{},
							}},
						},
					},
				},
			})
			if err != nil || len(fails) != 0 {
				t.Fatalf("cannot build test case, cannot add NHG, got: %v or failed ops %d", err, len(fails))
			}

			_, fails, err = r.AddEntry("VRF-1", &spb.AFTOperation{
				Id:              42,
				NetworkInstance: "VRF-1",
				Entry: &spb.AFTOperation_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix: "42.42.42.42/32",
						Ipv4Entry: &aftpb.Afts_Ipv4Entry{
							NextHopGroup:                &wpb.UintValue{Value: 1},
							NextHopGroupNetworkInstance: &wpb.StringValue{Value: defName},
						},
					},
				},
			})
			if err != nil || len(fails) != 0 {
				t.Fatalf("cannot build test case, cannot add NHG, got: %v or failed ops %d", err, len(fails))
			}

			return r
		}(),
		inNI: "VRF-1",
		inOp: &spb.AFTOperation{
			Id:              42,
			NetworkInstance: "VRF-1",
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix: "1.1.1.1/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{
						NextHopGroup:                &wpb.UintValue{Value: 1},
						NextHopGroupNetworkInstance: &wpb.StringValue{Value: defName},
					},
				},
			},
		},
		wantOKs: []*OpResult{{
			ID: 42,
			Op: &spb.AFTOperation{
				Id:              42,
				NetworkInstance: "VRF-1",
				Entry: &spb.AFTOperation_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix: "1.1.1.1/32",
						Ipv4Entry: &aftpb.Afts_Ipv4Entry{
							NextHopGroup:                &wpb.UintValue{Value: 1},
							NextHopGroupNetworkInstance: &wpb.StringValue{Value: defName},
						},
					},
				},
			},
		}},
		wantReferenceCheck: func(r *RIB) error {
			ni, ok := r.NetworkInstanceRIB(defName)
			if !ok {
				return fmt.Errorf("invalid network instance, %s", defName)
			}

			nhIndex := uint64(1)
			if got, want := ni.refCounts.NextHop[nhIndex], uint64(1); got != want {
				return fmt.Errorf("got invalid reference count for next-hop %d in network-instance %s, got: %d, want: %d", nhIndex, defName, got, want)
			}

			nhgIndex := uint64(1)
			if got, want := ni.refCounts.NextHopGroup[nhgIndex], uint64(2); got != want {
				return fmt.Errorf("got invalid reference count for next-hop-group %d in network-instance %s, got: %d, want: %d", nhgIndex, defName, got, want)
			}

			return nil
		},
	}, {
		desc: "IPv4 prefix implicit replace",
		inRIB: func() *RIB {
			r := New(defName)
			if err := r.AddNetworkInstance("VRF-1"); err != nil {
				t.Fatalf("cannot add network instance, %v", err)
			}

			_, fails, err := r.AddEntry(defName, &spb.AFTOperation{
				Id: 1,
				Entry: &spb.AFTOperation_NextHop{
					NextHop: &aftpb.Afts_NextHopKey{
						Index:   1,
						NextHop: &aftpb.Afts_NextHop{},
					},
				},
			})
			if err != nil || len(fails) != 0 {
				t.Fatalf("cannot build test case, cannot add NH, got: %v or failed ops %d", err, len(fails))
			}

			_, fails, err = r.AddEntry(defName, &spb.AFTOperation{
				Id: 1,
				Entry: &spb.AFTOperation_NextHopGroup{
					NextHopGroup: &aftpb.Afts_NextHopGroupKey{
						Id: 1,
						NextHopGroup: &aftpb.Afts_NextHopGroup{
							NextHop: []*aftpb.Afts_NextHopGroup_NextHopKey{{
								Index:   1,
								NextHop: &aftpb.Afts_NextHopGroup_NextHop{},
							}},
						},
					},
				},
			})
			if err != nil || len(fails) != 0 {
				t.Fatalf("cannot build test case, cannot add NHG, got: %v or failed ops %d", err, len(fails))
			}

			_, fails, err = r.AddEntry("VRF-1", &spb.AFTOperation{
				Id:              1,
				NetworkInstance: "VRF-1",
				Entry: &spb.AFTOperation_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix: "1.1.1.1/32",
						Ipv4Entry: &aftpb.Afts_Ipv4Entry{
							NextHopGroup:                &wpb.UintValue{Value: 1},
							NextHopGroupNetworkInstance: &wpb.StringValue{Value: defName},
						},
					},
				},
			})
			if err != nil || len(fails) != 0 {
				t.Fatalf("cannot build test case, cannot add IPv4, got: %v or failed ops %d", err, len(fails))
			}

			return r
		}(),
		inNI: "VRF-1",
		inOp: &spb.AFTOperation{
			Id:              42,
			NetworkInstance: "VRF-1",
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix: "1.1.1.1/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{
						NextHopGroup:                &wpb.UintValue{Value: 1},
						NextHopGroupNetworkInstance: &wpb.StringValue{Value: defName},
					},
				},
			},
		},
		wantOKs: []*OpResult{{
			ID: 42,
			Op: &spb.AFTOperation{
				Id:              42,
				NetworkInstance: "VRF-1",
				Entry: &spb.AFTOperation_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix: "1.1.1.1/32",
						Ipv4Entry: &aftpb.Afts_Ipv4Entry{
							NextHopGroup:                &wpb.UintValue{Value: 1},
							NextHopGroupNetworkInstance: &wpb.StringValue{Value: defName},
						},
					},
				},
			},
		}},
		wantReferenceCheck: func(r *RIB) error {
			ni, ok := r.NetworkInstanceRIB(defName)
			if !ok {
				return fmt.Errorf("invalid network instance, %s", defName)
			}

			nhIndex := uint64(1)
			if got, want := ni.refCounts.NextHop[nhIndex], uint64(1); got != want {
				return fmt.Errorf("got invalid reference count for next-hop %d in network-instance %s, got: %d, want: %d", nhIndex, defName, got, want)
			}

			nhgIndex := uint64(1)
			if got, want := ni.refCounts.NextHopGroup[nhgIndex], uint64(1); got != want {
				return fmt.Errorf("got invalid reference count for next-hop-group %d in network-instance %s, got: %d, want: %d", nhgIndex, defName, got, want)
			}

			return nil
		},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			gotOKs, gotFails, err := tt.inRIB.AddEntry(tt.inNI, tt.inOp)
			if (err != nil) != tt.wantErr {
				t.Fatalf("got unexpected error, got: %v, wantErr?", err)
			}
			sortResultSlice(gotOKs)
			sortResultSlice(gotFails)
			sortResultSlice(tt.wantOKs)
			sortResultSlice(tt.wantFails)

			if diff := cmp.Diff(gotOKs, tt.wantOKs, protocmp.Transform(), cmpopts.EquateEmpty()); diff != "" {
				t.Fatalf("did not get expected OK IDs, diff(-got,+want):\n%s", diff)
			}
			if diff := cmp.Diff(gotFails, tt.wantFails, protocmp.Transform(), cmpopts.EquateEmpty()); diff != "" {
				t.Fatalf("did not get expected failed IDs, diff(-got,+want):\n%s", diff)
			}

			if tt.wantReferenceCheck != nil {
				if err := tt.wantReferenceCheck(tt.inRIB); err != nil {
					t.Fatalf("did not get expected references, %v", err)
				}
			}
		})
	}
}

func TestDeleteEntry(t *testing.T) {
	tests := []struct {
		desc                   string
		inRIB                  *RIB
		inNetworkInstance      string
		inOp                   *spb.AFTOperation
		wantOKs                []*OpResult
		wantFails              []*OpResult
		wantErr                bool
		wantReferenceCheckPre  func(*RIB) error
		wantReferenceCheckPost func(*RIB) error
	}{{
		desc: "delete nexthop",
		inRIB: func() *RIB {
			r := New(defName)
			if _, _, err := r.AddEntry(defName, &spb.AFTOperation{
				Id: 42,
				Entry: &spb.AFTOperation_NextHop{
					NextHop: &aftpb.Afts_NextHopKey{
						Index:   42,
						NextHop: &aftpb.Afts_NextHop{},
					},
				},
			}); err != nil {
				t.Fatalf("cannot set up test case, %v", err)
			}
			return r
		}(),
		inNetworkInstance: defName,
		inOp: &spb.AFTOperation{
			Id: 42,
			Entry: &spb.AFTOperation_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index:   42,
					NextHop: &aftpb.Afts_NextHop{},
				},
			},
		},
		wantOKs: []*OpResult{{
			ID: 42,
			Op: &spb.AFTOperation{
				Id: 42,
				Entry: &spb.AFTOperation_NextHop{
					NextHop: &aftpb.Afts_NextHopKey{
						Index:   42,
						NextHop: &aftpb.Afts_NextHop{},
					},
				},
			},
		}},
	}, {
		desc:              "delete missing NH",
		inRIB:             New(defName),
		inNetworkInstance: defName,
		inOp: &spb.AFTOperation{
			Id: 42,
			Entry: &spb.AFTOperation_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index:   42,
					NextHop: &aftpb.Afts_NextHop{},
				},
			},
		},
		wantOKs: []*OpResult{{
			ID: 42,
			Op: &spb.AFTOperation{
				Id: 42,
				Entry: &spb.AFTOperation_NextHop{
					NextHop: &aftpb.Afts_NextHopKey{
						Index:   42,
						NextHop: &aftpb.Afts_NextHop{},
					},
				},
			},
		}},
	}, {
		desc: "delete IPv4",
		inRIB: func() *RIB {
			r := New(defName, DisableRIBCheckFn())
			if _, _, err := r.AddEntry(defName, &spb.AFTOperation{
				Id: 42,
				Entry: &spb.AFTOperation_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix:    "1.1.1.1/32",
						Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
					},
				},
			}); err != nil {
				t.Fatalf("cannot set up test case, %v", err)
			}
			return r
		}(),
		inNetworkInstance: defName,
		inOp: &spb.AFTOperation{
			Id: 42,
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix:    "1.1.1.1/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
				},
			},
		},
		wantOKs: []*OpResult{{
			ID: 42,
			Op: &spb.AFTOperation{
				Id: 42,
				Entry: &spb.AFTOperation_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix:    "1.1.1.1/32",
						Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
					},
				},
			},
		}},
	}, {
		desc: "nil MPLS input",
		inRIB: func() *RIB {
			r := New(defName, DisableRIBCheckFn())
			return r
		}(),
		inNetworkInstance: defName,
		wantErr:           true,
	}, {
		desc: "unsupported MPLS input",
		inRIB: func() *RIB {
			r := New(defName, DisableRIBCheckFn())
			return r
		}(),
		inNetworkInstance: defName,
		inOp: &spb.AFTOperation{
			Id: 42,
			Entry: &spb.AFTOperation_Mpls{
				Mpls: &aftpb.Afts_LabelEntryKey{
					Label: &aftpb.Afts_LabelEntryKey_LabelOpenconfigmplstypesmplslabelenum{
						LabelOpenconfigmplstypesmplslabelenum: enums.OpenconfigMplsTypesMplsLabelEnum_OPENCONFIGMPLSTYPESMPLSLABELENUM_IMPLICIT_NULL,
					},
				},
			},
		},
		wantFails: []*OpResult{{
			ID: 42,
			Op: &spb.AFTOperation{
				Id: 42,
				Entry: &spb.AFTOperation_Mpls{
					Mpls: &aftpb.Afts_LabelEntryKey{
						Label: &aftpb.Afts_LabelEntryKey_LabelOpenconfigmplstypesmplslabelenum{
							LabelOpenconfigmplstypesmplslabelenum: enums.OpenconfigMplsTypesMplsLabelEnum_OPENCONFIGMPLSTYPESMPLSLABELENUM_IMPLICIT_NULL,
						},
					},
				},
			},
			Error: "unsupported label type *gribi_aft.Afts_LabelEntryKey, only uint64 labels are supported, label_openconfigmplstypesmplslabelenum:OPENCONFIGMPLSTYPESMPLSLABELENUM_IMPLICIT_NULL",
		}},
	}, {
		desc: "delete MPLS",
		inRIB: func() *RIB {
			r := New(defName, DisableRIBCheckFn())
			if _, _, err := r.AddEntry(defName, &spb.AFTOperation{
				Id: 42,
				Entry: &spb.AFTOperation_Mpls{
					Mpls: &aftpb.Afts_LabelEntryKey{
						Label:      &aftpb.Afts_LabelEntryKey_LabelUint64{LabelUint64: 42},
						LabelEntry: &aftpb.Afts_LabelEntry{},
					},
				},
			}); err != nil {
				t.Fatalf("cannot set up test case, %v", err)
			}
			return r
		}(),
		inNetworkInstance: defName,
		inOp: &spb.AFTOperation{
			Id: 42,
			Entry: &spb.AFTOperation_Mpls{
				Mpls: &aftpb.Afts_LabelEntryKey{
					Label:      &aftpb.Afts_LabelEntryKey_LabelUint64{LabelUint64: 42},
					LabelEntry: &aftpb.Afts_LabelEntry{},
				},
			},
		},
		wantOKs: []*OpResult{{
			ID: 42,
			Op: &spb.AFTOperation{
				Id: 42,
				Entry: &spb.AFTOperation_Mpls{
					Mpls: &aftpb.Afts_LabelEntryKey{
						Label:      &aftpb.Afts_LabelEntryKey_LabelUint64{LabelUint64: 42},
						LabelEntry: &aftpb.Afts_LabelEntry{},
					},
				},
			},
		}},
	}, {
		desc: "delete NHG",
		inRIB: func() *RIB {
			r := New(defName)
			if _, _, err := r.AddEntry(defName, &spb.AFTOperation{
				Id: 42,
				Entry: &spb.AFTOperation_NextHopGroup{
					NextHopGroup: &aftpb.Afts_NextHopGroupKey{
						Id:           1,
						NextHopGroup: &aftpb.Afts_NextHopGroup{},
					},
				},
			}); err != nil {
				t.Fatalf("cannot set up test case, %v", err)
			}
			return r
		}(),
		inNetworkInstance: defName,
		inOp: &spb.AFTOperation{
			Id: 42,
			Entry: &spb.AFTOperation_NextHopGroup{
				NextHopGroup: &aftpb.Afts_NextHopGroupKey{
					Id:           1,
					NextHopGroup: &aftpb.Afts_NextHopGroup{},
				},
			},
		},
		wantOKs: []*OpResult{{
			ID: 42,
			Op: &spb.AFTOperation{
				Id: 42,
				Entry: &spb.AFTOperation_NextHopGroup{
					NextHopGroup: &aftpb.Afts_NextHopGroupKey{
						Id:           1,
						NextHopGroup: &aftpb.Afts_NextHopGroup{},
					},
				},
			},
		}},
	}, {
		desc:              "unknown type",
		inRIB:             New(defName),
		inNetworkInstance: defName,
		inOp: &spb.AFTOperation{
			Id: 42,
			Entry: &spb.AFTOperation_MacEntry{
				MacEntry: &aftpb.Afts_MacEntryKey{
					MacAddress: "82:33:1b:e9:a4:01",
					MacEntry:   &aftpb.Afts_MacEntry{},
				},
			},
		},
		wantErr: true,
	}, {
		desc:              "invalid network instance",
		inRIB:             New(defName),
		inNetworkInstance: "fish",
		wantErr:           true,
	}, {
		desc:              "cannot remove NH that is referenced",
		inRIB:             mustChainRIB(),
		inNetworkInstance: defName,
		inOp: &spb.AFTOperation{
			Id: 42,
			Entry: &spb.AFTOperation_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index: 1,
				},
			},
		},
		wantFails: []*OpResult{{
			ID: 42,
			Op: &spb.AFTOperation{
				Id: 42,
				Entry: &spb.AFTOperation_NextHop{
					NextHop: &aftpb.Afts_NextHopKey{
						Index: 1,
					},
				},
			},
		}},
	}, {
		desc:              "cannot remove NHG that is referenced",
		inRIB:             mustChainRIB(),
		inNetworkInstance: defName,
		inOp: &spb.AFTOperation{
			Id: 42,
			Entry: &spb.AFTOperation_NextHopGroup{
				NextHopGroup: &aftpb.Afts_NextHopGroupKey{
					Id: 1,
				},
			},
		},
		wantFails: []*OpResult{{
			ID: 42,
			Op: &spb.AFTOperation{
				Id: 42,
				Entry: &spb.AFTOperation_NextHopGroup{
					NextHopGroup: &aftpb.Afts_NextHopGroupKey{
						Id: 1,
					},
				},
			},
		}},
	}, {
		desc:              "badly formed NHG",
		inRIB:             mustChainRIB(),
		inNetworkInstance: defName,
		inOp: &spb.AFTOperation{
			Id: 42,
			Entry: &spb.AFTOperation_NextHopGroup{
				NextHopGroup: &aftpb.Afts_NextHopGroupKey{},
			},
		},
		wantFails: []*OpResult{{
			ID: 42,
			Op: &spb.AFTOperation{
				Id: 42,
				Entry: &spb.AFTOperation_NextHopGroup{
					NextHopGroup: &aftpb.Afts_NextHopGroupKey{},
				},
			},
			Error: "invalid NHG ID 0",
		}},
	}, {
		desc:              "NHG that doesn't exist",
		inRIB:             mustChainRIB(),
		inNetworkInstance: defName,
		inOp: &spb.AFTOperation{
			Id: 42,
			Entry: &spb.AFTOperation_NextHopGroup{
				NextHopGroup: &aftpb.Afts_NextHopGroupKey{
					Id: 512,
				},
			},
		},
		wantOKs: []*OpResult{{
			ID: 42,
			Op: &spb.AFTOperation{
				Id: 42,
				Entry: &spb.AFTOperation_NextHopGroup{
					NextHopGroup: &aftpb.Afts_NextHopGroupKey{
						Id: 512,
					},
				},
			},
		}},
	}, {
		desc:              "reference check after one referring entry removed - default NI",
		inRIB:             mustTwoEntryRIB(defName),
		inNetworkInstance: defName,
		inOp: &spb.AFTOperation{
			Id: 42,
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix:    "2.2.2.2/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
				},
			},
		},
		wantOKs: []*OpResult{{
			ID: 42,
			Op: &spb.AFTOperation{
				Id: 42,
				Entry: &spb.AFTOperation_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix:    "2.2.2.2/32",
						Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
					},
				},
			},
		}},
		wantReferenceCheckPre: func(r *RIB) error {
			niR, ok := r.NetworkInstanceRIB(defName)
			if !ok {
				t.Fatalf("cannot build test case, can't get %s RIB", defName)
			}

			// nhChecks is keyed by the NH Index and has a value of the expected reference count.
			nhChecks := map[uint64]uint64{
				1: 1,
				2: 1,
			}
			// nhgChecks is keyed by the NHG Index and has a value of the expected reference count.
			nhgChecks := map[uint64]uint64{
				1: 2,
			}

			for nh, wantRef := range nhChecks {
				if got, want := niR.refCounts.NextHop[nh], wantRef; got != want {
					return fmt.Errorf("did not get expected references for NH %d, got: %d, want: %d", nh, got, want)
				}
			}
			for nhg, wantRef := range nhgChecks {
				if got, want := niR.refCounts.NextHopGroup[nhg], wantRef; got != want {
					return fmt.Errorf("did not get expected references for NHG %d, got: %d, want: %d", nhg, got, want)
				}
			}
			return nil
		},
		wantReferenceCheckPost: func(r *RIB) error {
			niR, ok := r.NetworkInstanceRIB(defName)
			if !ok {
				t.Fatalf("cannot build test case, can't get %s RIB", defName)
			}

			// nhChecks is keyed by the NH Index and has a value of the expected reference count.
			nhChecks := map[uint64]uint64{
				1: 1,
				2: 1,
			}
			// nhgChecks is keyed by the NHG Index and has a value of the expected reference count.
			nhgChecks := map[uint64]uint64{
				1: 1,
			}

			for nh, wantRef := range nhChecks {
				if got, want := niR.refCounts.NextHop[nh], wantRef; got != want {
					return fmt.Errorf("did not get expected references for NH %d, got: %d, want: %d", nh, got, want)
				}
			}
			for nhg, wantRef := range nhgChecks {
				if got, want := niR.refCounts.NextHopGroup[nhg], wantRef; got != want {
					return fmt.Errorf("did not get expected references for NHG %d, got: %d, want: %d", nhg, got, want)
				}
			}
			return nil
		},
	}, {
		desc:              "reference check after one referring entry removed - NHG+NI in VRF",
		inRIB:             mustTwoEntryRIB("VRF-42"),
		inNetworkInstance: defName,
		inOp: &spb.AFTOperation{
			Id: 42,
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix:    "2.2.2.2/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
				},
			},
		},
		wantOKs: []*OpResult{{
			ID: 42,
			Op: &spb.AFTOperation{
				Id: 42,
				Entry: &spb.AFTOperation_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix:    "2.2.2.2/32",
						Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
					},
				},
			},
		}},
		wantReferenceCheckPre: func(r *RIB) error {
			niR, ok := r.NetworkInstanceRIB("VRF-42")
			if !ok {
				t.Fatalf("cannot build test case, can't get %s RIB", "VRF-42")
			}

			// nhChecks is keyed by the NH Index and has a value of the expected reference count.
			nhChecks := map[uint64]uint64{
				1: 1,
				2: 1,
			}
			// nhgChecks is keyed by the NHG Index and has a value of the expected reference count.
			nhgChecks := map[uint64]uint64{
				1: 2,
			}

			for nh, wantRef := range nhChecks {
				if got, want := niR.refCounts.NextHop[nh], wantRef; got != want {
					return fmt.Errorf("did not get expected references for NH %d, got: %d, want: %d", nh, got, want)
				}
			}
			for nhg, wantRef := range nhgChecks {
				if got, want := niR.refCounts.NextHopGroup[nhg], wantRef; got != want {
					return fmt.Errorf("did not get expected references for NHG %d, got: %d, want: %d", nhg, got, want)
				}
			}
			return nil
		},
		wantReferenceCheckPost: func(r *RIB) error {
			niR, ok := r.NetworkInstanceRIB("VRF-42")
			if !ok {
				t.Fatalf("cannot build test case, can't get %s RIB", "VRF-42")
			}

			// nhChecks is keyed by the NH Index and has a value of the expected reference count.
			nhChecks := map[uint64]uint64{
				1: 1,
				2: 1,
			}
			// nhgChecks is keyed by the NHG Index and has a value of the expected reference count.
			nhgChecks := map[uint64]uint64{
				1: 1,
			}

			for nh, wantRef := range nhChecks {
				if got, want := niR.refCounts.NextHop[nh], wantRef; got != want {
					return fmt.Errorf("did not get expected references for NH %d, got: %d, want: %d", nh, got, want)
				}
			}
			for nhg, wantRef := range nhgChecks {
				if got, want := niR.refCounts.NextHopGroup[nhg], wantRef; got != want {
					return fmt.Errorf("did not get expected references for NHG %d, got: %d, want: %d", nhg, got, want)
				}
			}
			return nil
		},
	}, {
		desc:              "reference check after one referring NHG removed from NHs",
		inRIB:             mustSharedNHRIB(defName),
		inNetworkInstance: defName,
		inOp: &spb.AFTOperation{
			Id: 42,
			Entry: &spb.AFTOperation_NextHopGroup{
				NextHopGroup: &aftpb.Afts_NextHopGroupKey{
					Id:           2,
					NextHopGroup: &aftpb.Afts_NextHopGroup{},
				},
			},
		},
		wantOKs: []*OpResult{{
			ID: 42,
			Op: &spb.AFTOperation{
				Id: 42,
				Entry: &spb.AFTOperation_NextHopGroup{
					NextHopGroup: &aftpb.Afts_NextHopGroupKey{
						Id:           2,
						NextHopGroup: &aftpb.Afts_NextHopGroup{},
					},
				},
			},
		}},
		wantReferenceCheckPre: func(r *RIB) error {
			niR, ok := r.NetworkInstanceRIB(defName)
			if !ok {
				t.Fatalf("cannot build test case, can't get %s RIB", defName)
			}

			// nhChecks is keyed by the NH Index and has a value of the expected reference count.
			nhChecks := map[uint64]uint64{
				1: 2,
				2: 2,
			}
			// nhgChecks is keyed by the NHG Index and has a value of the expected reference count.
			nhgChecks := map[uint64]uint64{
				1: 1, // NHG 1 is referenced by 1.1.1.1/32
				2: 0, // NHG 2 is unreferencded so that it can be removed.
			}

			for nh, wantRef := range nhChecks {
				if got, want := niR.refCounts.NextHop[nh], wantRef; got != want {
					return fmt.Errorf("did not get expected references for NH %d, got: %d, want: %d", nh, got, want)
				}
			}
			for nhg, wantRef := range nhgChecks {
				if got, want := niR.refCounts.NextHopGroup[nhg], wantRef; got != want {
					return fmt.Errorf("did not get expected references for NHG %d, got: %d, want: %d", nhg, got, want)
				}
			}
			return nil
		},
		wantReferenceCheckPost: func(r *RIB) error {
			niR, ok := r.NetworkInstanceRIB(defName)
			if !ok {
				t.Fatalf("cannot build test case, can't get %s RIB", defName)
			}

			// nhChecks is keyed by the NH Index and has a value of the expected reference count.
			nhChecks := map[uint64]uint64{
				1: 1,
				2: 1,
			}
			// nhgChecks is keyed by the NHG Index and has a value of the expected reference count.
			nhgChecks := map[uint64]uint64{
				1: 1,
			}

			for nh, wantRef := range nhChecks {
				if got, want := niR.refCounts.NextHop[nh], wantRef; got != want {
					return fmt.Errorf("did not get expected references for NH %d, got: %d, want: %d", nh, got, want)
				}
			}
			for nhg, wantRef := range nhgChecks {
				if got, want := niR.refCounts.NextHopGroup[nhg], wantRef; got != want {
					return fmt.Errorf("did not get expected references for NHG %d, got: %d, want: %d", nhg, got, want)
				}
			}
			return nil
		},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			if tt.wantReferenceCheckPre != nil {
				if err := tt.wantReferenceCheckPre(tt.inRIB); err != nil {
					t.Fatalf("invalid reference checks before operation, %v", err)
				}
			}

			gotOKs, gotFails, err := tt.inRIB.DeleteEntry(tt.inNetworkInstance, tt.inOp)
			if (err != nil) != tt.wantErr {
				t.Fatalf("did not get expected error, got: %v, wantErr? %v", err, tt.wantErr)
			}

			if diff := cmp.Diff(gotOKs, tt.wantOKs, protocmp.Transform(), cmpopts.EquateEmpty()); diff != "" {
				t.Fatalf("did not get expected OK IDs, diff(-got,+want):\n%s", diff)
			}
			if diff := cmp.Diff(gotFails, tt.wantFails, protocmp.Transform(), cmpopts.EquateEmpty()); diff != "" {
				t.Fatalf("did not get expected failed IDs, diff(-got,+want):\n%s", diff)
			}

			if tt.wantReferenceCheckPost != nil {
				if err := tt.wantReferenceCheckPost(tt.inRIB); err != nil {
					t.Fatalf("invalid reference checks after operation, %v", err)
				}
			}
		})
	}
}

func TestKnownNetworkInstances(t *testing.T) {
	tests := []struct {
		desc      string
		inRIB     *RIB
		wantNames []string
	}{{
		desc:      "empty set of network instances",
		inRIB:     &RIB{},
		wantNames: []string{},
	}, {
		desc: "set of known names",
		inRIB: &RIB{
			niRIB: map[string]*RIBHolder{
				"one": {},
				"two": {},
			},
		},
		wantNames: []string{"one", "two"},
	}, {
		desc: "ensure alphabetical",
		inRIB: &RIB{
			niRIB: map[string]*RIBHolder{
				"zebra":    {},
				"antelope": {},
			},
		},
		wantNames: []string{"antelope", "zebra"},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got := tt.inRIB.KnownNetworkInstances()
			if diff := cmp.Diff(got, tt.wantNames); diff != "" {
				t.Fatalf("did not get expected set of network instance names, diff(-got,+want):\n%s", diff)
			}
		})
	}
}

func TestGetRIB(t *testing.T) {
	// allPopRIB is a RIB with one entry in each table.
	allPopRIB := func() *RIBHolder {
		r := NewRIBHolder("VRF-42")

		cr := &aft.RIB{}
		nh := cr.GetOrCreateAfts().GetOrCreateNextHop(1)
		nh.IpAddress = ygot.String("1.1.1.1/32")

		if _, err := r.doAddNH(1, cr); err != nil {
			t.Fatalf("cannot build RIB, %v", err)
		}

		cr = &aft.RIB{}
		nhg := cr.GetOrCreateAfts().GetOrCreateNextHopGroup(42).GetOrCreateNextHop(1)
		nhg.Weight = ygot.Uint64(1)

		if _, err := r.doAddNHG(42, cr); err != nil {
			t.Fatalf("cannot build RIB, %v", err)
		}

		cr = &aft.RIB{}
		ipv4 := cr.GetOrCreateAfts().GetOrCreateIpv4Entry("42.42.42.42/32")
		ipv4.NextHopGroup = ygot.Uint64(42)

		if _, err := r.doAddIPv4("42.42.42.42/32", cr); err != nil {
			t.Fatalf("cannot build RIB, %v", err)
		}

		return r
	}()

	tests := []struct {
		desc          string
		inRIB         *RIBHolder
		inFilter      map[spb.AFTType]bool
		wantResponses []*spb.GetResponse
		wantErr       bool
	}{{
		desc:  "empty RIB",
		inRIB: NewRIBHolder("VRF-1"),
		inFilter: map[spb.AFTType]bool{
			spb.AFTType_ALL: true,
		},
		wantResponses: []*spb.GetResponse{},
	}, {
		desc: "ipv4 entry",
		inRIB: func() *RIBHolder {
			r := NewRIBHolder("VRF-1")

			cr := &aft.RIB{}
			ipv4 := cr.GetOrCreateAfts().GetOrCreateIpv4Entry("1.1.1.1/32")
			ipv4.NextHopGroup = ygot.Uint64(42)

			if _, err := r.doAddIPv4("1.1.1.1/32", cr); err != nil {
				t.Fatalf("cannot build RIB, %v", err)
			}
			return r
		}(),
		inFilter: map[spb.AFTType]bool{
			spb.AFTType_ALL: true,
		},
		wantResponses: []*spb.GetResponse{{
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
	}, {
		desc: "next-hop-group entry",
		inRIB: func() *RIBHolder {
			r := NewRIBHolder("VRF-42")

			cr := &aft.RIB{}
			nhg := cr.GetOrCreateAfts().GetOrCreateNextHopGroup(42)
			nhg.Color = ygot.Uint64(1)

			if _, err := r.doAddNHG(42, cr); err != nil {
				t.Fatalf("cannot build RIB, %v", err)
			}
			return r
		}(),
		inFilter: map[spb.AFTType]bool{
			spb.AFTType_ALL: true,
		},
		wantResponses: []*spb.GetResponse{{
			Entry: []*spb.AFTEntry{{
				NetworkInstance: "VRF-42",
				Entry: &spb.AFTEntry_NextHopGroup{
					NextHopGroup: &aftpb.Afts_NextHopGroupKey{
						Id: 42,
						NextHopGroup: &aftpb.Afts_NextHopGroup{
							Color: &wpb.UintValue{Value: 1},
						},
					},
				},
			}},
		}},
	}, {
		desc: "next-hop entry",
		inRIB: func() *RIBHolder {
			r := NewRIBHolder("VRF-42")

			cr := &aft.RIB{}
			nh := cr.GetOrCreateAfts().GetOrCreateNextHop(1)
			nh.IpAddress = ygot.String("1.1.1.1/32")

			if _, err := r.doAddNH(1, cr); err != nil {
				t.Fatalf("cannot build RIB, %v", err)
			}
			return r
		}(),
		inFilter: map[spb.AFTType]bool{
			spb.AFTType_ALL: true,
		},
		wantResponses: []*spb.GetResponse{{
			Entry: []*spb.AFTEntry{{
				NetworkInstance: "VRF-42",
				Entry: &spb.AFTEntry_NextHop{
					NextHop: &aftpb.Afts_NextHopKey{
						Index: 1,
						NextHop: &aftpb.Afts_NextHop{
							IpAddress: &wpb.StringValue{Value: "1.1.1.1/32"},
						},
					},
				},
			}},
		}},
	}, {
		desc:  "all tables populated but filtered to ipv4",
		inRIB: allPopRIB,
		inFilter: map[spb.AFTType]bool{
			spb.AFTType_IPV4: true,
		},
		wantResponses: []*spb.GetResponse{{
			Entry: []*spb.AFTEntry{{
				NetworkInstance: "VRF-42",
				Entry: &spb.AFTEntry_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix: "42.42.42.42/32",
						Ipv4Entry: &aftpb.Afts_Ipv4Entry{
							NextHopGroup: &wpb.UintValue{Value: 42},
						},
					},
				},
			}},
		}},
	}, {
		desc:  "all tables populated but filtered to nhg",
		inRIB: allPopRIB,
		inFilter: map[spb.AFTType]bool{
			spb.AFTType_NEXTHOP_GROUP: true,
		},
		wantResponses: []*spb.GetResponse{{
			Entry: []*spb.AFTEntry{{
				NetworkInstance: "VRF-42",
				Entry: &spb.AFTEntry_NextHopGroup{
					NextHopGroup: &aftpb.Afts_NextHopGroupKey{
						Id:           42,
						NextHopGroup: &aftpb.Afts_NextHopGroup{},
					},
				},
			}},
		}},
	}, {
		desc:  "all tables populated but filtered to nh",
		inRIB: allPopRIB,
		inFilter: map[spb.AFTType]bool{
			spb.AFTType_NEXTHOP: true,
		},
		wantResponses: []*spb.GetResponse{{
			Entry: []*spb.AFTEntry{{
				NetworkInstance: "VRF-42",
				Entry: &spb.AFTEntry_NextHop{
					NextHop: &aftpb.Afts_NextHopKey{
						Index: 1,
						NextHop: &aftpb.Afts_NextHop{
							IpAddress: &wpb.StringValue{Value: "1.1.1.1/32"},
						},
					},
				},
			}},
		}},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			msgCh := make(chan *spb.GetResponse)
			stopCh := make(chan struct{})

			doneCh := make(chan struct{})
			defer close(doneCh)

			got := []*spb.GetResponse{}
			go func() {
				for {
					select {
					case r := <-msgCh:
						got = append(got, r)
					case <-doneCh:
						return
					}
				}
			}()

			if err := tt.inRIB.GetRIB(tt.inFilter, msgCh, stopCh); (err != nil) != tt.wantErr {
				t.Fatalf("did not get expected error, got: %v, wantErr? %v", err, tt.wantErr)
			}
			doneCh <- struct{}{}

			if diff := cmp.Diff(got, tt.wantResponses, protocmp.Transform()); diff != "" {
				t.Fatalf("did not get expected responses, diff(-got,+want):\n%s", diff)
			}
		})
	}
}

func TestAddNetworkInstance(t *testing.T) {
	tests := []struct {
		desc    string
		inRIB   *RIB
		inName  string
		wantErr bool
	}{{
		desc:   "successfully created",
		inRIB:  New("DEFAULT"),
		inName: "NEW",
	}, {
		desc:    "existing RIB",
		inRIB:   New("DEFAULT"),
		inName:  "DEFAULT",
		wantErr: true,
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			if err := tt.inRIB.AddNetworkInstance(tt.inName); (err != nil) != tt.wantErr {
				t.Fatalf("did not get expected error, got: %v, wantErr? %v", err, tt.wantErr)
			}
		})
	}
}

func TestResolvedEntryHook(t *testing.T) {
	defName := "DEFAULT"

	baseRIB := func() *RIB {
		r := New(defName)
		ops := []*spb.AFTOperation{{
			Entry: &spb.AFTOperation_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index: 1,
					NextHop: &aftpb.Afts_NextHop{
						IpAddress: &wpb.StringValue{Value: "1.1.1.1"},
					},
				},
			},
		}, {
			Entry: &spb.AFTOperation_NextHopGroup{
				NextHopGroup: &aftpb.Afts_NextHopGroupKey{
					Id: 1,
					NextHopGroup: &aftpb.Afts_NextHopGroup{
						NextHop: []*aftpb.Afts_NextHopGroup_NextHopKey{{
							Index: 1,
							NextHop: &aftpb.Afts_NextHopGroup_NextHop{
								Weight: &wpb.UintValue{Value: 32},
							},
						}},
					},
				},
			},
		}}

		for i, op := range ops {
			op.Id = uint64(i)
			op.Op = spb.AFTOperation_ADD

			if _, _, err := r.AddEntry(defName, op); err != nil {
				t.Fatalf("cannot add entry %s, %v", prototext.Format(op), err)
			}
		}

		return r
	}

	gotCh := make(chan interface{})
	tests := []struct {
		desc        string
		inRIB       *RIB
		inOperation *spb.AFTOperation
		inHook      ResolvedEntryFn
		checkFn     func() error
	}{{
		desc:  "simple check that the hook was called",
		inRIB: baseRIB(),
		inOperation: &spb.AFTOperation{
			Id: 42,
			Op: spb.AFTOperation_ADD,
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix: "10.0.0.0/8",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{
						NextHopGroup: &wpb.UintValue{Value: 1},
					},
				},
			},
		},
		inHook: func(_ map[string]*aft.RIB, _ constants.OpType, netinst, prefix string) {
			gotCh <- fmt.Sprintf("%s->%s", netinst, prefix)
		},
		checkFn: func() error {
			got := <-gotCh
			gotS, ok := got.(string)
			if !ok {
				return fmt.Errorf("got wrong type of result, got: %T, want: string", got)
			}
			if got, want := gotS, fmt.Sprintf("%s->10.0.0.0/8", defName); got != want {
				return fmt.Errorf("did not get expected result, got: %v, want: %v", got, want)
			}
			return nil
		},
	}, {
		desc:  "aft helper",
		inRIB: baseRIB(),
		inOperation: &spb.AFTOperation{
			Id: 42,
			Op: spb.AFTOperation_ADD,
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix: "10.0.0.0/8",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{
						NextHopGroup: &wpb.UintValue{Value: 1},
					},
				},
			},
		},
		inHook: func(ribs map[string]*aft.RIB, op constants.OpType, netinst, prefix string) {
			summ, err := afthelper.NextHopAddrsForPrefix(ribs, netinst, prefix)
			if err != nil {
				gotCh <- err
			}
			gotCh <- summ
		},
		checkFn: func() error {
			got := <-gotCh
			switch t := got.(type) {
			case error:
				return fmt.Errorf("got error, %v", t)
			case map[string]*afthelper.NextHopSummary:
				want := map[string]*afthelper.NextHopSummary{
					"1.1.1.1": {
						Weight:          32,
						Address:         "1.1.1.1",
						NetworkInstance: "DEFAULT",
					},
				}
				if diff := cmp.Diff(got, want); diff != "" {
					return fmt.Errorf("got diff, %s", diff)
				}
			}
			return nil
		},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {

			tt.inRIB.SetResolvedEntryHook(tt.inHook)

			var (
				wg       sync.WaitGroup
				checkErr error
			)

			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := tt.checkFn(); err != nil {
					checkErr = err
				}
			}()

			if _, fails, err := tt.inRIB.AddEntry(defName, tt.inOperation); err != nil || len(fails) != 0 {
				t.Fatalf("did not successfully add entry, error: %v, fails: %v", err, fails)
			}
			wg.Wait()
			if checkErr != nil {
				t.Fatalf("%v", checkErr)
			}
		})
	}
}

func TestCanDelete(t *testing.T) {
	tests := []struct {
		desc           string
		inRIB          *RIB
		inNetInst      string
		inDelCandidate *aft.RIB
		want           bool
		wantErr        bool
	}{{
		desc: "can delete IPv4 - always OK",
		inRIB: func() *RIB {
			r := New(defName)

			if _, _, err := r.AddEntry(defName, &spb.AFTOperation{
				Id: 1,
				Entry: &spb.AFTOperation_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix:    "1.1.1.1/32",
						Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
					},
				},
			}); err != nil {
				t.Fatalf("cannot build testcase, got err: %v", err)
			}

			return r
		}(),
		inNetInst: defName,
		inDelCandidate: func() *aft.RIB {
			r := &aft.RIB{}
			r.GetOrCreateAfts().GetOrCreateIpv4Entry("1.1.1.1/32")
			return r
		}(),
		want: true,
	}, {
		desc:      "cannot delete referenced NHG",
		inRIB:     mustChainRIB(),
		inNetInst: defName,
		inDelCandidate: func() *aft.RIB {
			r := &aft.RIB{}
			r.GetOrCreateAfts().GetOrCreateNextHopGroup(1)
			return r
		}(),
		want: false,
	}, {
		desc:      "cannot delete referenced NH",
		inRIB:     mustChainRIB(),
		inNetInst: defName,
		inDelCandidate: func() *aft.RIB {
			r := &aft.RIB{}
			r.GetOrCreateAfts().GetOrCreateNextHop(1)
			return r
		}(),
		want: false,
	}, {
		desc:      "nil candidate",
		inRIB:     New(defName),
		inNetInst: defName,
		wantErr:   true,
	}, {
		desc:  "empty candidate",
		inRIB: New(defName),
		inDelCandidate: func() *aft.RIB {
			r := &aft.RIB{}
			r.GetOrCreateAfts()
			return r
		}(),
		inNetInst: defName,
		wantErr:   true,
	}, {
		desc:  "bad NI name",
		inRIB: New(defName),
		inDelCandidate: func() *aft.RIB {
			r := &aft.RIB{}
			r.GetOrCreateAfts().GetOrCreateIpv4Entry("1.2.3.4/32")
			return r
		}(),
		inNetInst: "pollock",
		wantErr:   true,
	}, {
		desc: "can delete IPv4 - rewritten NI name",
		inRIB: func() *RIB {
			r := New(defName)

			if _, _, err := r.AddEntry(defName, &spb.AFTOperation{
				Id: 1,
				Entry: &spb.AFTOperation_Ipv4{
					Ipv4: &aftpb.Afts_Ipv4EntryKey{
						Prefix:    "1.1.1.1/32",
						Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
					},
				},
			}); err != nil {
				t.Fatalf("cannot build testcase, got err: %v", err)
			}

			return r
		}(),
		inDelCandidate: func() *aft.RIB {
			r := &aft.RIB{}
			r.GetOrCreateAfts().GetOrCreateIpv4Entry("1.1.1.1/32")
			return r
		}(),
		want: true,
	}, {
		desc:  "badly formed NHG",
		inRIB: New(defName),
		inDelCandidate: func() *aft.RIB {
			r := &aft.RIB{}
			r.GetOrCreateAfts().GetOrCreateNextHopGroup(0)
			return r
		}(),
		wantErr: true,
	}, {
		desc:  "badly formed NH",
		inRIB: New(defName),
		inDelCandidate: func() *aft.RIB {
			r := &aft.RIB{}
			r.GetOrCreateAfts().GetOrCreateNextHop(0)
			return r
		}(),
		wantErr: true,
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got, err := tt.inRIB.canDelete(tt.inNetInst, tt.inDelCandidate)

			if (err != nil) != tt.wantErr {
				t.Fatalf("did not get expected error, got: %v, wantErr? %v", err, tt.wantErr)
			}

			if got != tt.want {
				t.Fatalf("did not get expected canDelete, got: %v, want: %v", got, tt.want)
			}
		})
	}
}

// defName is the default network instance name used in tests.
const (
	defName string = "DEFAULT"
)

// mustChainRIB returns a RIB that has an IPv4 -> NHG -> NH, and panics if it cannot.
func mustChainRIB() *RIB {
	r := New(defName)

	if _, _, err := r.AddEntry(defName, &spb.AFTOperation{
		Id: 1,
		Entry: &spb.AFTOperation_Ipv4{
			Ipv4: &aftpb.Afts_Ipv4EntryKey{
				Prefix: "1.1.1.1/32",
				Ipv4Entry: &aftpb.Afts_Ipv4Entry{
					NextHopGroup: &wpb.UintValue{Value: 1},
				},
			},
		},
	}); err != nil {
		panic(fmt.Sprintf("cannot build testcase, can't add ipv4, got err: %v", err))
	}

	if _, _, err := r.AddEntry(defName, &spb.AFTOperation{
		Id: 2,
		Entry: &spb.AFTOperation_NextHopGroup{
			NextHopGroup: &aftpb.Afts_NextHopGroupKey{
				Id: 1,
				NextHopGroup: &aftpb.Afts_NextHopGroup{
					NextHop: []*aftpb.Afts_NextHopGroup_NextHopKey{{
						Index:   1,
						NextHop: &aftpb.Afts_NextHopGroup_NextHop{},
					}, {
						Index:   2,
						NextHop: &aftpb.Afts_NextHopGroup_NextHop{},
					}},
				},
			},
		},
	}); err != nil {
		panic(fmt.Sprintf("cannot build testcase, can't add nhg, got err: %v", err))
	}

	for _, idx := range []uint64{1, 2} {
		if _, _, err := r.AddEntry(defName, &spb.AFTOperation{
			Id: idx + 2,
			Entry: &spb.AFTOperation_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index:   idx,
					NextHop: &aftpb.Afts_NextHop{},
				},
			},
		}); err != nil {
			panic(fmt.Sprintf("cannot build testcase, can't add nh, got err: %v", err))
		}
	}
	return r
}

// mustTwoEntryRIB returns a RIB that has two IPv4 entries referring to the same -> NHG -> NH, and panics if it cannot.
// The NHG and NHs are created in the network instance specified by nhNI.
func mustTwoEntryRIB(nhNI string) *RIB {
	r := New(defName)

	if nhNI != defName {
		if err := r.AddNetworkInstance(nhNI); err != nil {
			panic(fmt.Sprintf("cannot create network instance %s, %v", nhNI, err))
		}
	}

	for _, idx := range []uint64{1, 2} {
		if _, _, err := r.AddEntry(nhNI, &spb.AFTOperation{
			Id: idx + 2,
			Entry: &spb.AFTOperation_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index:   idx,
					NextHop: &aftpb.Afts_NextHop{},
				},
			},
		}); err != nil {
			panic(fmt.Sprintf("cannot build testcase, can't add nh, got err: %v", err))
		}
	}

	if _, _, err := r.AddEntry(nhNI, &spb.AFTOperation{
		Id: 2,
		Entry: &spb.AFTOperation_NextHopGroup{
			NextHopGroup: &aftpb.Afts_NextHopGroupKey{
				Id: 1,
				NextHopGroup: &aftpb.Afts_NextHopGroup{
					NextHop: []*aftpb.Afts_NextHopGroup_NextHopKey{{
						Index:   1,
						NextHop: &aftpb.Afts_NextHopGroup_NextHop{},
					}, {
						Index:   2,
						NextHop: &aftpb.Afts_NextHopGroup_NextHop{},
					}},
				},
			},
		},
	}); err != nil {
		panic(fmt.Sprintf("cannot build testcase, can't add nhg, got err: %v", err))
	}

	for i, pfx := range []string{"1.1.1.1/32", "2.2.2.2/32"} {
		if _, _, err := r.AddEntry(defName, &spb.AFTOperation{
			Id: 20 + uint64(i),
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix: pfx,
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{
						NextHopGroup:                &wpb.UintValue{Value: uint64(i)},
						NextHopGroupNetworkInstance: &wpb.StringValue{Value: nhNI},
					},
				},
			},
		}); err != nil {
			panic(fmt.Sprintf("cannot build testcase, can't add ipv4, got err: %v", err))
		}
	}

	if _, _, err := r.AddEntry(defName, &spb.AFTOperation{
		Id: 1,
		Entry: &spb.AFTOperation_Ipv4{
			Ipv4: &aftpb.Afts_Ipv4EntryKey{
				Prefix: "1.1.1.1/32",
				Ipv4Entry: &aftpb.Afts_Ipv4Entry{
					NextHopGroup:                &wpb.UintValue{Value: 1},
					NextHopGroupNetworkInstance: &wpb.StringValue{Value: nhNI},
				},
			},
		},
	}); err != nil {
		panic(fmt.Sprintf("cannot build testcase, can't add ipv4, got err: %v", err))
	}

	return r
}

// mustSharedNHRIB returns a RIB that has one IPv4 entry that points to a NHG that shares
// two NHs with another NHG (unreferenced).
func mustSharedNHRIB(nhNI string) *RIB {
	r := New(defName)

	for _, idx := range []uint64{1, 2} {
		if _, _, err := r.AddEntry(nhNI, &spb.AFTOperation{
			Id: idx + 2,
			Entry: &spb.AFTOperation_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index:   idx,
					NextHop: &aftpb.Afts_NextHop{},
				},
			},
		}); err != nil {
			panic(fmt.Sprintf("cannot build testcase, can't add nh, got err: %v", err))
		}
	}

	for _, id := range []uint64{1, 2} {
		if _, _, err := r.AddEntry(nhNI, &spb.AFTOperation{
			Id: 10 + id,
			Entry: &spb.AFTOperation_NextHopGroup{
				NextHopGroup: &aftpb.Afts_NextHopGroupKey{
					Id: id,
					NextHopGroup: &aftpb.Afts_NextHopGroup{
						NextHop: []*aftpb.Afts_NextHopGroup_NextHopKey{{
							Index:   1,
							NextHop: &aftpb.Afts_NextHopGroup_NextHop{},
						}, {
							Index:   2,
							NextHop: &aftpb.Afts_NextHopGroup_NextHop{},
						}},
					},
				},
			},
		}); err != nil {
			panic(fmt.Sprintf("cannot build testcase, can't add nhg, got err: %v", err))
		}
	}

	for i, pfx := range []string{"1.1.1.1/32"} {
		if _, _, err := r.AddEntry(defName, &spb.AFTOperation{
			Id: 20 + uint64(i),
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix: pfx,
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{
						NextHopGroup:                &wpb.UintValue{Value: uint64(i) + 1},
						NextHopGroupNetworkInstance: &wpb.StringValue{Value: nhNI},
					},
				},
			},
		}); err != nil {
			panic(fmt.Sprintf("cannot build testcase, can't add ipv4, got err: %v", err))
		}
	}

	return r
}

func TestFlush(t *testing.T) {
	tests := []struct {
		desc             string
		inRIB            *RIBHolder
		wantErrSubstring string
	}{{
		desc: "nh only",
		inRIB: func() *RIBHolder {
			r := NewRIBHolder("DEFAULT")

			_, _, err := r.AddNextHop(&aftpb.Afts_NextHopKey{
				Index: 1,
				NextHop: &aftpb.Afts_NextHop{
					IpAddress: &wpb.StringValue{Value: "1.2.3.4"},
				},
			}, false)
			if err != nil {
				t.Fatalf("cannot add next-hop, %v", err)
			}

			return r
		}(),
	}, {
		desc: "nhg + nh",
		inRIB: func() *RIBHolder {
			r := NewRIBHolder("DEFAULT")

			_, _, err := r.AddNextHop(&aftpb.Afts_NextHopKey{
				Index: 1,
				NextHop: &aftpb.Afts_NextHop{
					IpAddress: &wpb.StringValue{Value: "1.2.3.4"},
				},
			}, false)
			if err != nil {
				t.Fatalf("cannot add next-hop, %v", err)
			}

			_, _, err = r.AddNextHopGroup(&aftpb.Afts_NextHopGroupKey{
				Id: 1,
				NextHopGroup: &aftpb.Afts_NextHopGroup{
					NextHop: []*aftpb.Afts_NextHopGroup_NextHopKey{{
						Index: 1,
						NextHop: &aftpb.Afts_NextHopGroup_NextHop{
							Weight: &wpb.UintValue{Value: 1},
						},
					}},
				},
			}, false)
			if err != nil {
				t.Fatalf("cannot add next-hop-group, %v", err)
			}

			return r
		}(),
	}, {
		desc: "nhg with backup + nh",
		inRIB: func() *RIBHolder {
			r := NewRIBHolder("DEFAULT")

			_, _, err := r.AddNextHop(&aftpb.Afts_NextHopKey{
				Index: 1,
				NextHop: &aftpb.Afts_NextHop{
					IpAddress: &wpb.StringValue{Value: "1.2.3.4"},
				},
			}, false)
			if err != nil {
				t.Fatalf("cannot add next-hop, %v", err)
			}

			_, _, err = r.AddNextHopGroup(&aftpb.Afts_NextHopGroupKey{
				Id: 1,
				NextHopGroup: &aftpb.Afts_NextHopGroup{
					NextHop: []*aftpb.Afts_NextHopGroup_NextHopKey{{
						Index: 1,
						NextHop: &aftpb.Afts_NextHopGroup_NextHop{
							Weight: &wpb.UintValue{Value: 1},
						},
					}},
				},
			}, false)
			if err != nil {
				t.Fatalf("cannot add next-hop-group, %v", err)
			}

			_, _, err = r.AddNextHopGroup(&aftpb.Afts_NextHopGroupKey{
				Id: 2,
				NextHopGroup: &aftpb.Afts_NextHopGroup{
					NextHop: []*aftpb.Afts_NextHopGroup_NextHopKey{{
						Index: 1,
						NextHop: &aftpb.Afts_NextHopGroup_NextHop{
							Weight: &wpb.UintValue{Value: 1},
						},
					}},
					BackupNextHopGroup: &wpb.UintValue{Value: 1},
				},
			}, false)
			if err != nil {
				t.Fatalf("cannot add next-hop-group, %v", err)
			}

			return r
		}(),
	}, {
		desc: "circular reference to NHG",
		inRIB: func() *RIBHolder {
			r := NewRIBHolder("DEFAULT")

			_, _, err := r.AddNextHop(&aftpb.Afts_NextHopKey{
				Index: 1,
				NextHop: &aftpb.Afts_NextHop{
					IpAddress: &wpb.StringValue{Value: "1.2.3.4"},
				},
			}, false)
			if err != nil {
				t.Fatalf("cannot add next-hop, %v", err)
			}

			_, _, err = r.AddNextHopGroup(&aftpb.Afts_NextHopGroupKey{
				Id: 1,
				NextHopGroup: &aftpb.Afts_NextHopGroup{
					NextHop: []*aftpb.Afts_NextHopGroup_NextHopKey{{
						Index: 1,
						NextHop: &aftpb.Afts_NextHopGroup_NextHop{
							Weight: &wpb.UintValue{Value: 1},
						},
					}},
					BackupNextHopGroup: &wpb.UintValue{Value: 2},
				},
			}, false)
			if err != nil {
				t.Fatalf("cannot add next-hop-group, %v", err)
			}

			_, _, err = r.AddNextHopGroup(&aftpb.Afts_NextHopGroupKey{
				Id: 2,
				NextHopGroup: &aftpb.Afts_NextHopGroup{
					NextHop: []*aftpb.Afts_NextHopGroup_NextHopKey{{
						Index: 1,
						NextHop: &aftpb.Afts_NextHopGroup_NextHop{
							Weight: &wpb.UintValue{Value: 1},
						},
					}},
					BackupNextHopGroup: &wpb.UintValue{Value: 1},
				},
			}, false)
			if err != nil {
				t.Fatalf("cannot add next-hop-group, %v", err)
			}

			return r
		}(),
	}, {
		desc: "ipv4 + nhg + nh",
		inRIB: func() *RIBHolder {
			r := NewRIBHolder("DEFAULT")

			_, _, err := r.AddNextHop(&aftpb.Afts_NextHopKey{
				Index: 1,
				NextHop: &aftpb.Afts_NextHop{
					IpAddress: &wpb.StringValue{Value: "1.2.3.4"},
				},
			}, false)
			if err != nil {
				t.Fatalf("cannot add next-hop, %v", err)
			}

			_, _, err = r.AddNextHopGroup(&aftpb.Afts_NextHopGroupKey{
				Id: 1,
				NextHopGroup: &aftpb.Afts_NextHopGroup{
					NextHop: []*aftpb.Afts_NextHopGroup_NextHopKey{{
						Index: 1,
						NextHop: &aftpb.Afts_NextHopGroup_NextHop{
							Weight: &wpb.UintValue{Value: 1},
						},
					}},
				},
			}, false)
			if err != nil {
				t.Fatalf("cannot add next-hop-group, %v", err)
			}

			_, _, err = r.AddIPv4(&aftpb.Afts_Ipv4EntryKey{
				Prefix: "192.0.2.1/32",
				Ipv4Entry: &aftpb.Afts_Ipv4Entry{
					NextHopGroup: &wpb.UintValue{Value: 1},
				},
			}, false)
			if err != nil {
				t.Fatalf("cannot add ipv4 entry, %v", err)
			}

			return r
		}(),
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got := tt.inRIB.Flush()
			if diff := errdiff.Substring(got, tt.wantErrSubstring); diff != "" {
				t.Fatalf("did not get expected error when flushing RIB, %s", diff)
			}
		})

		if l := len(tt.inRIB.r.Afts.Ipv4Entry); l != 0 {
			t.Fatalf("did not remove all IPv4 entries, got: %d, want: 0", l)
		}

		if l := len(tt.inRIB.r.Afts.NextHopGroup); l != 0 {
			t.Fatalf("did not remove all IPv4 entries, got: %d, want: 0", l)
		}

		if l := len(tt.inRIB.r.Afts.NextHop); l != 0 {
			t.Fatalf("did not remove all IPv4 entries, got: %d, want: 0", l)
		}
	}
}

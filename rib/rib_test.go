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
	"fmt"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/openconfig/gnmi/errdiff"
	"github.com/openconfig/gnmi/value"
	"github.com/openconfig/gribigo/aft"
	"github.com/openconfig/gribigo/constants"
	"github.com/openconfig/ygot/testutil"
	"github.com/openconfig/ygot/ygot"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	aftpb "github.com/openconfig/gribi/v1/proto/gribi_aft"
	spb "github.com/openconfig/gribi/v1/proto/service"
	wpb "github.com/openconfig/ygot/proto/ywrapper"
)

type ribType int64

const (
	_ ribType = iota
	ipv4
	nhg
	nh
)

func TestAdd(t *testing.T) {
	tests := []struct {
		desc          string
		inRIBHolder   *RIBHolder
		inType        ribType
		inEntry       proto.Message
		wantInstalled bool
		wantRIB       *aft.RIB
		wantErr       bool
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
				Metadata: &wpb.BytesValue{Value: []byte{0, 1, 2, 3, 4, 5, 6, 7}},
			},
		},
		wantInstalled: true,
		wantRIB: &aft.RIB{
			Afts: &aft.Afts{
				Ipv4Entry: map[string]*aft.Afts_Ipv4Entry{
					"1.0.0.0/24": {
						Prefix:   ygot.String("1.0.0.0/24"),
						Metadata: aft.Binary{0, 1, 2, 3, 4, 5, 6, 7},
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
			if _, err := r.AddIPv4(&aftpb.Afts_Ipv4EntryKey{
				Prefix: "8.8.8.8/32",
				Ipv4Entry: &aftpb.Afts_Ipv4Entry{
					Metadata: &wpb.BytesValue{Value: []byte{0, 1, 2, 3, 4, 5, 6, 7}},
				},
			}); err != nil {
				panic(err)
			}
			return r
		}(),
		inType: ipv4,
		inEntry: &aftpb.Afts_Ipv4EntryKey{
			Prefix: "8.8.8.8/32",
			Ipv4Entry: &aftpb.Afts_Ipv4Entry{
				Metadata: &wpb.BytesValue{Value: []byte{10, 11, 12, 13, 14, 15, 16, 17}},
			},
		},
		wantInstalled: true,
		wantRIB: &aft.RIB{
			Afts: &aft.Afts{
				Ipv4Entry: map[string]*aft.Afts_Ipv4Entry{
					"8.8.8.8/32": {
						Prefix:   ygot.String("8.8.8.8/32"),
						Metadata: aft.Binary{10, 11, 12, 13, 14, 15, 16, 17},
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
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			r := tt.inRIBHolder
			switch tt.inType {
			case ipv4:
				// special case for nil test
				if tt.inEntry == nil {
					_, err := r.AddIPv4(nil)
					if (err != nil) != tt.wantErr {
						t.Fatalf("got unexpected error adding IPv4 entry, got: %v, wantErr? %v", err, tt.wantErr)
					}
					return
				}
				gotInstalled, err := r.AddIPv4(tt.inEntry.(*aftpb.Afts_Ipv4EntryKey))
				if (err != nil) != tt.wantErr {
					t.Fatalf("got unexpected error adding IPv4 entry, got: %v, wantErr? %v", err, tt.wantErr)
				}
				if gotInstalled != tt.wantInstalled {
					t.Fatalf("did not get expected installed IPv4 bool, got: %v, want: %v", gotInstalled, tt.wantInstalled)
				}
			case nhg:
				gotInstalled, err := r.AddNextHopGroup(tt.inEntry.(*aftpb.Afts_NextHopGroupKey))
				if (err != nil) != tt.wantErr {
					t.Fatalf("got unexpected error adding NHG entry, got: %v, wantErr? %v", err, tt.wantErr)
				}
				if gotInstalled != tt.wantInstalled {
					t.Fatalf("did not get expected installed NHG bool, got: %v, want: %v", gotInstalled, tt.wantInstalled)
				}
			case nh:
				gotInstalled, err := r.AddNextHop(tt.inEntry.(*aftpb.Afts_NextHopKey))
				if (err != nil) != tt.wantErr {
					t.Fatalf("got unexpected error adding NH entry, got: %v, wantErr? %v", err, tt.wantErr)
				}
				if gotInstalled != tt.wantInstalled {
					t.Fatalf("did not get expected installed NH bool, got: %v, want: %v", gotInstalled, tt.wantInstalled)
				}
			default:
				t.Fatalf("unsupported test type in Add, %v", tt.inType)
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

func TestDeleteIPv4Entry(t *testing.T) {
	type entry struct {
		prefix   string
		metadata []byte
		nhg      uint64
		nhgni    string
	}
	ribEntries := func(e []*entry) *aft.RIB {
		a := &aft.RIB{}
		for _, en := range e {
			p := a.GetOrCreateAfts().GetOrCreateIpv4Entry(en.prefix)
			if en.metadata != nil {
				p.Metadata = en.metadata
			}
			if en.nhg != 0 {
				p.NextHopGroup = ygot.Uint64(en.nhg)
			}
			if en.nhgni != "" {
				p.NextHopGroupNetworkInstance = ygot.String(en.nhgni)
			}
		}
		return a
	}

	tests := []struct {
		desc        string
		inRIB       *RIBHolder
		inEntry     *aftpb.Afts_Ipv4EntryKey
		wantErr     bool
		wantPostRIB *RIBHolder
	}{{
		desc:    "nil input",
		inRIB:   &RIBHolder{},
		wantErr: true,
	}, {
		desc: "delete entry, no payload",
		inRIB: &RIBHolder{
			r: ribEntries([]*entry{
				{prefix: "1.1.1.1/32"},
			}),
		},
		inEntry: &aftpb.Afts_Ipv4EntryKey{
			Prefix: "1.1.1.1/32",
		},
		wantPostRIB: &RIBHolder{
			r: ribEntries([]*entry{}),
		},
	}, {
		desc: "delete entry, matching payload",
		inRIB: &RIBHolder{
			r: ribEntries([]*entry{
				{prefix: "1.1.1.1/32", metadata: []byte{0, 1, 2, 3, 4, 5, 6, 7}},
			}),
		},
		inEntry: &aftpb.Afts_Ipv4EntryKey{
			Prefix: "1.1.1.1/32",
			Ipv4Entry: &aftpb.Afts_Ipv4Entry{
				Metadata: &wpb.BytesValue{Value: []byte{0, 1, 2, 3, 4, 5, 6, 7}},
			},
		},
		wantPostRIB: &RIBHolder{
			r: ribEntries([]*entry{}),
		},
	}, {
		desc: "delete entry, mismatched payload",
		inRIB: &RIBHolder{
			r: ribEntries([]*entry{
				{prefix: "1.1.1.1/32", metadata: []byte{1, 2, 3, 4}},
			}),
		},
		inEntry: &aftpb.Afts_Ipv4EntryKey{
			Prefix: "1.1.1.1/32",
			Ipv4Entry: &aftpb.Afts_Ipv4Entry{
				Metadata: &wpb.BytesValue{Value: []byte{2, 3, 4, 5}},
			},
		},
		wantErr: true,
		wantPostRIB: &RIBHolder{
			r: ribEntries([]*entry{
				{prefix: "1.1.1.1/32", metadata: []byte{1, 2, 3, 4}},
			}),
		},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			if err := tt.inRIB.DeleteIPv4(tt.inEntry); (err != nil) != tt.wantErr {
				t.Fatalf("did not get expected error, got; %v, wantErr? %v", err, tt.wantErr)
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
			Prefix:   ygot.String("8.8.8.8/32"),
			Metadata: []byte{1, 2, 3, 4},
		},
		want: &aftpb.Afts_Ipv4EntryKey{
			Prefix: "8.8.8.8/32",
			Ipv4Entry: &aftpb.Afts_Ipv4Entry{
				Metadata: &wpb.BytesValue{
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
			Do:  constants.ADD,
			IP4: "8.8.8.8/32",
		}, {
			Do:  constants.ADD,
			NHG: 42,
		}, {
			Do: constants.ADD,
			NH: 84,
		}},
		storeFn: true,
		want: []interface{}{
			&op{Do: constants.ADD, TS: 0, IP4: "8.8.8.8/32"},
			&op{Do: constants.ADD, TS: 1, NHG: 42},
			&op{Do: constants.ADD, TS: 2, NH: 84},
		},
	}, {
		desc: "store delete hooks",
		inOps: []*op{{
			Do:  constants.DELETE,
			IP4: "8.8.8.8/32",
		}, {
			Do:  constants.DELETE,
			IP4: "1.1.1.1/32",
		}},
		storeFn: true,
		want: []interface{}{
			&op{Do: constants.DELETE, TS: 0, IP4: "8.8.8.8/32"},
			&op{Do: constants.DELETE, TS: 1, IP4: "1.1.1.1/32"},
		},
	}, {
		desc: "store add and delete",
		inOps: []*op{{
			Do:  constants.ADD,
			IP4: "1.1.1.1/32",
		}, {
			Do:  constants.DELETE,
			IP4: "2.2.2.2/32",
		}},
		want: []interface{}{
			&op{Do: constants.DELETE, TS: 0, IP4: "1.1.1.1/32"},
			&op{Do: constants.DELETE, TS: 1, IP4: "2.2.2.2/32"},
		},
	}, {
		desc: "gnmi add and delete",
		inOps: []*op{{
			Do:  constants.ADD,
			IP4: "1.2.3.4/32",
		}, {
			Do:  constants.DELETE,
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
			store := func(o constants.OpType, _ int64, _ string, gs ygot.GoStruct) {
				switch t := gs.(type) {
				case *aft.Afts_Ipv4Entry:
					got = append(got, &op{Do: o, TS: tsFn(), IP4: t.GetPrefix()})
				case *aft.Afts_NextHopGroup:
					got = append(got, &op{Do: o, TS: tsFn(), NHG: t.GetId()})
				case *aft.Afts_NextHop:
					got = append(got, &op{Do: o, TS: tsFn(), NH: t.GetIndex()})
				}
			}

			gnmiNoti := func(o constants.OpType, _ int64, ni string, gs ygot.GoStruct) {
				if o == constants.DELETE {
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
					case constants.REPLACE, constants.DELETE:
						if _, err := r.niRIB[r.defaultName].AddIPv4(&aftpb.Afts_Ipv4EntryKey{
							Prefix:    o.IP4,
							Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
						}); err != nil {
							t.Fatalf("cannot pre-add IPv4 entry %s: %v", o.IP4, err)
						}
					}
				case o.NHG != 0:
					switch o.Do {
					case constants.REPLACE, constants.DELETE:
						if _, err := r.niRIB[r.defaultName].AddNextHopGroup(&aftpb.Afts_NextHopGroupKey{
							Id:           o.NHG,
							NextHopGroup: &aftpb.Afts_NextHopGroup{},
						}); err != nil {
							t.Fatalf("cannot add NHG entry %d, %v", o.NHG, err)
						}
					}
				case o.NH != 0:
					switch o.Do {
					case constants.REPLACE, constants.DELETE:
						if _, err := r.niRIB[r.defaultName].AddNextHop(&aftpb.Afts_NextHopKey{
							Index:   o.NH,
							NextHop: &aftpb.Afts_NextHop{},
						}); err != nil {
							t.Fatalf("cannot add NH entry %d, %v", o.NH, err)
						}
					}
				}
			}

			if tt.storeFn {
				r.SetHook(store)
			}

			if tt.gnmiFn {
				r.SetHook(gnmiNoti)
			}

			for _, o := range tt.inOps {
				switch {
				case o.IP4 != "":
					switch o.Do {
					case constants.ADD:
						if _, err := r.niRIB[r.defaultName].AddIPv4(&aftpb.Afts_Ipv4EntryKey{
							Prefix: o.IP4,
							Ipv4Entry: &aftpb.Afts_Ipv4Entry{
								NextHopGroup: &wpb.UintValue{Value: 1},
							},
						}); err != nil {
							t.Fatalf("cannot add IPv4 entry %s: %v", o.IP4, err)
						}
					case constants.DELETE:
						if err := r.niRIB[r.defaultName].DeleteIPv4(&aftpb.Afts_Ipv4EntryKey{
							Prefix: o.IP4,
						}); err != nil {
							t.Fatalf("cannote delete IPv4 entry %s: %v", o.IP4, err)
						}
					}
				case o.NHG != 0:
					switch o.Do {
					case constants.ADD:
						if _, err := r.niRIB[r.defaultName].AddNextHopGroup(&aftpb.Afts_NextHopGroupKey{
							Id:           o.NHG,
							NextHopGroup: &aftpb.Afts_NextHopGroup{},
						}); err != nil {
							t.Fatalf("cannot add NHG entry %d, %v", o.NHG, err)
						}
					}
				case o.NH != 0:
					switch o.Do {
					case constants.ADD:
						if _, err := r.niRIB[r.defaultName].AddNextHop(&aftpb.Afts_NextHopKey{
							Index:   o.NH,
							NextHop: &aftpb.Afts_NextHop{},
						}); err != nil {
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
	defName := "DEFAULT"
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
		desc:  "MPLS - invalid",
		inRIB: New(defName),
		inNI:  defName,
		inCand: func() *aft.RIB {
			r := &aft.RIB{}
			r.GetOrCreateAfts().GetOrCreateLabelEntry(aft.UnionUint32(42))
			return r
		}(),
		wantErrSubstring: "MPLS label entries are unsupported",
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
		wantErrSubstring: "invalid unknown network-instance for IPv4Entry",
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
	defName := "default"
	tests := []struct {
		desc      string
		inRIB     *RIB
		inNI      string
		inOp      *spb.AFTOperation
		wantOKs   []*OpResult
		wantFails []*OpResult
		wantErr   bool
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
		desc:  "unsupported type - pbr",
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
				t.Fatalf("did not get expected OK IDs, diff(-got,+want):\n%s", diff)
			}

		})
	}
}

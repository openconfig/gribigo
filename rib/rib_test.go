package rib

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/openconfig/gribigo/aft"
	"github.com/openconfig/ygot/testutil"
	"github.com/openconfig/ygot/ygot"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/openconfig/gnmi/value"
	aftpb "github.com/openconfig/gribi/v1/proto/gribi_aft"
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
		desc        string
		inRIBHolder *RIBHolder
		inType      ribType
		inEntry     proto.Message
		wantRIB     *aft.RIB
		wantErr     bool
	}{{
		desc:        "ipv4 prefix only",
		inRIBHolder: newRIBHolder("DEFAULT"),
		inType:      ipv4,
		inEntry: &aftpb.Afts_Ipv4EntryKey{
			Prefix:    "1.0.0.0/24",
			Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
		},
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
		inRIBHolder: newRIBHolder("DEFAULT"),
		inType:      ipv4,
		inEntry: &aftpb.Afts_Ipv4EntryKey{
			Prefix: "1.0.0.0/24",
			Ipv4Entry: &aftpb.Afts_Ipv4Entry{
				Metadata: &wpb.BytesValue{Value: []byte{0, 1, 2, 3, 4, 5, 6, 7}},
			},
		},
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
		inRIBHolder: newRIBHolder("DEFAULT"),
		inType:      ipv4,
		inEntry:     nil,
		wantErr:     true,
	}, {
		desc:        "nil IPv4 prefix value",
		inRIBHolder: newRIBHolder("DEFAULT"),
		inType:      ipv4,
		inEntry:     &aftpb.Afts_Ipv4EntryKey{},
		wantErr:     true,
	}, {
		desc:        "nil IPv4 value",
		inRIBHolder: newRIBHolder("DEFAULT"),
		inType:      ipv4,
		inEntry: &aftpb.Afts_Ipv4EntryKey{
			Prefix: "8.8.8.8/32",
		},
		wantErr: true,
	}, {
		desc: "implicit ipv4 replace",
		inRIBHolder: func() *RIBHolder {
			r := newRIBHolder("DEFAULT")
			if err := r.AddIPv4(&aftpb.Afts_Ipv4EntryKey{
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
		inRIBHolder: newRIBHolder("DEFAULT"),
		inType:      ipv4,
		inEntry: &aftpb.Afts_Ipv4EntryKey{
			Prefix:    "NOT AN IPV$ PREFIX",
			Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
		},
		wantErr: true,
	}, {
		desc:        "nhg ID only",
		inRIBHolder: newRIBHolder("DEFAULT"),
		inType:      nhg,
		inEntry: &aftpb.Afts_NextHopGroupKey{
			Id:           1,
			NextHopGroup: &aftpb.Afts_NextHopGroup{},
		},
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
		inRIBHolder: newRIBHolder("DEFAULT"),
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
		inRIBHolder: newRIBHolder("DEFAULT"),
		inType:      nh,
		inEntry: &aftpb.Afts_NextHopKey{
			Index:   1,
			NextHop: &aftpb.Afts_NextHop{},
		},
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
		inRIBHolder: newRIBHolder("DEFAULT"),
		inType:      nh,
		inEntry: &aftpb.Afts_NextHopKey{
			Index: 1,
			NextHop: &aftpb.Afts_NextHop{
				IpAddress: &wpb.StringValue{Value: "8.8.4.4"},
			},
		},
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
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			r := tt.inRIBHolder
			switch tt.inType {
			case ipv4:
				// special case for nil test
				if tt.inEntry == nil {
					if err := r.AddIPv4(nil); (err != nil) != tt.wantErr {
						t.Fatalf("got unexpected error adding IPv4 entry, got: %v, wantErr? %v", err, tt.wantErr)
					}
					return
				}
				if err := r.AddIPv4(tt.inEntry.(*aftpb.Afts_Ipv4EntryKey)); (err != nil) != tt.wantErr {
					t.Fatalf("got unexpected error adding IPv4 entry, got: %v, wantErr? %v", err, tt.wantErr)
				}
			case nhg:
				if err := r.AddNextHopGroup(tt.inEntry.(*aftpb.Afts_NextHopGroupKey)); (err != nil) != tt.wantErr {
					t.Fatalf("got unexpected error adding NHG entry, got: %v, wantErr? %v", err, tt.wantErr)
				}
			case nh:
				if err := r.AddNextHop(tt.inEntry.(*aftpb.Afts_NextHopKey)); (err != nil) != tt.wantErr {
					t.Fatalf("got unexpected error adding NH entry, got: %v, wantErr? %v", err, tt.wantErr)
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
		Do  OpType
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
			Do:  ADD,
			IP4: "8.8.8.8/32",
		}, {
			Do:  ADD,
			NHG: 42,
		}, {
			Do: ADD,
			NH: 84,
		}},
		storeFn: true,
		want: []interface{}{
			&op{Do: ADD, TS: 0, IP4: "8.8.8.8/32"},
			&op{Do: ADD, TS: 1, NHG: 42},
			&op{Do: ADD, TS: 2, NH: 84},
		},
	}, {
		desc: "store delete hooks",
		inOps: []*op{{
			Do:  DELETE,
			IP4: "8.8.8.8/32",
		}, {
			Do:  DELETE,
			IP4: "1.1.1.1/32",
		}},
		storeFn: true,
		want: []interface{}{
			&op{Do: DELETE, TS: 0, IP4: "8.8.8.8/32"},
			&op{Do: DELETE, TS: 1, IP4: "1.1.1.1/32"},
		},
	}, {
		desc: "store add and delete",
		inOps: []*op{{
			Do:  ADD,
			IP4: "1.1.1.1/32",
		}, {
			Do:  DELETE,
			IP4: "2.2.2.2/32",
		}},
		want: []interface{}{
			&op{Do: DELETE, TS: 0, IP4: "1.1.1.1/32"},
			&op{Do: DELETE, TS: 1, IP4: "2.2.2.2/32"},
		},
	}, {
		desc: "gnmi add and delete",
		inOps: []*op{{
			Do:  ADD,
			IP4: "1.2.3.4/32",
		}, {
			Do:  DELETE,
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
			store := func(o OpType, _ int64, _ string, gs ygot.GoStruct) {
				switch t := gs.(type) {
				case *aft.Afts_Ipv4Entry:
					got = append(got, &op{Do: o, TS: tsFn(), IP4: t.GetPrefix()})
				case *aft.Afts_NextHopGroup:
					got = append(got, &op{Do: o, TS: tsFn(), NHG: t.GetId()})
				case *aft.Afts_NextHop:
					got = append(got, &op{Do: o, TS: tsFn(), NH: t.GetIndex()})
				}
			}

			gnmiNoti := func(o OpType, _ int64, ni string, gs ygot.GoStruct) {
				if o == DELETE {
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

			// do setup before we start, we can't delete or modify entries
			// that don't exist, so remove them.
			for _, o := range tt.inOps {
				switch {
				case o.IP4 != "":
					switch o.Do {
					case MODIFY, DELETE:
						if err := r.niRIB[r.defaultName].AddIPv4(&aftpb.Afts_Ipv4EntryKey{
							Prefix:    o.IP4,
							Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
						}); err != nil {
							t.Fatalf("cannot pre-add IPv4 entry %s: %v", o.IP4, err)
						}
					}
				case o.NHG != 0:
					switch o.Do {
					case MODIFY, DELETE:
						if err := r.niRIB[r.defaultName].AddNextHopGroup(&aftpb.Afts_NextHopGroupKey{
							Id:           o.NHG,
							NextHopGroup: &aftpb.Afts_NextHopGroup{},
						}); err != nil {
							t.Fatalf("cannot add NHG entry %d, %v", o.NHG, err)
						}
					}
				case o.NH != 0:
					switch o.Do {
					case MODIFY, DELETE:
						if err := r.niRIB[r.defaultName].AddNextHop(&aftpb.Afts_NextHopKey{
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
					case ADD:
						if err := r.niRIB[r.defaultName].AddIPv4(&aftpb.Afts_Ipv4EntryKey{
							Prefix:    o.IP4,
							Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
						}); err != nil {
							t.Fatalf("cannot add IPv4 entry %s: %v", o.IP4, err)
						}
					case DELETE:
						if err := r.niRIB[r.defaultName].DeleteIPv4(&aftpb.Afts_Ipv4EntryKey{
							Prefix: o.IP4,
						}); err != nil {
							t.Fatalf("cannote delete IPv4 entry %s: %v", o.IP4, err)
						}
					}
				case o.NHG != 0:
					switch o.Do {
					case ADD:
						if err := r.niRIB[r.defaultName].AddNextHopGroup(&aftpb.Afts_NextHopGroupKey{
							Id:           o.NHG,
							NextHopGroup: &aftpb.Afts_NextHopGroup{},
						}); err != nil {
							t.Fatalf("cannot add NHG entry %d, %v", o.NHG, err)
						}
					}
				case o.NH != 0:
					switch o.Do {
					case ADD:
						if err := r.niRIB[r.defaultName].AddNextHop(&aftpb.Afts_NextHopKey{
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

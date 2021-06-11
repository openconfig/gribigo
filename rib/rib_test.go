package rib

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/openconfig/gribigo/aft"
	"github.com/openconfig/ygot/ygot"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"

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
		inRIBHolder *ribHolder
		inType      ribType
		inEntry     proto.Message
		wantRIB     *aft.RIB
		wantErr     bool
	}{{
		desc:        "ipv4 prefix only",
		inRIBHolder: newRIBHolder(),
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
		inRIBHolder: newRIBHolder(),
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
		inRIBHolder: newRIBHolder(),
		inType:      ipv4,
		inEntry:     nil,
		wantErr:     true,
	}, {
		desc:        "nil IPv4 prefix value",
		inRIBHolder: newRIBHolder(),
		inType:      ipv4,
		inEntry:     &aftpb.Afts_Ipv4EntryKey{},
		wantErr:     true,
	}, {
		desc:        "nil IPv4 value",
		inRIBHolder: newRIBHolder(),
		inType:      ipv4,
		inEntry: &aftpb.Afts_Ipv4EntryKey{
			Prefix: "8.8.8.8/32",
		},
		wantErr: true,
	}, {
		desc: "implicit ipv4 replace",
		inRIBHolder: func() *ribHolder {
			r := newRIBHolder()
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
		inRIBHolder: newRIBHolder(),
		inType:      ipv4,
		inEntry: &aftpb.Afts_Ipv4EntryKey{
			Prefix:    "NOT AN IPV$ PREFIX",
			Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
		},
		wantErr: true,
	}, {
		desc:        "nhg ID only",
		inRIBHolder: newRIBHolder(),
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
		inRIBHolder: newRIBHolder(),
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
		inRIBHolder: newRIBHolder(),
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
		inRIBHolder: newRIBHolder(),
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
			r := newRIBHolder()
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
		inRIB       *ribHolder
		inEntry     *aftpb.Afts_Ipv4EntryKey
		wantErr     bool
		wantPostRIB *ribHolder
	}{{
		desc:    "nil input",
		inRIB:   &ribHolder{},
		wantErr: true,
	}, {
		desc: "delete entry, no payload",
		inRIB: &ribHolder{
			r: ribEntries([]*entry{
				{prefix: "1.1.1.1/32"},
			}),
		},
		inEntry: &aftpb.Afts_Ipv4EntryKey{
			Prefix: "1.1.1.1/32",
		},
		wantPostRIB: &ribHolder{
			r: ribEntries([]*entry{}),
		},
	}, {
		desc: "delete entry, matching payload",
		inRIB: &ribHolder{
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
		wantPostRIB: &ribHolder{
			r: ribEntries([]*entry{}),
		},
	}, {
		desc: "delete entry, mismatched payload",
		inRIB: &ribHolder{
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
		wantPostRIB: &ribHolder{
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

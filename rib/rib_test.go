package rib

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/openconfig/gribigo/aft"
	"github.com/openconfig/ygot/ygot"
	"google.golang.org/protobuf/testing/protocmp"

	aftpb "github.com/openconfig/gribi/v1/proto/gribi_aft"
	wpb "github.com/openconfig/ygot/proto/ywrapper"
)

func TestAddIPv4(t *testing.T) {
	tests := []struct {
		desc    string
		inIPv4  *aftpb.Afts_Ipv4EntryKey
		wantRIB *aft.RIB
		wantErr bool
	}{{
		desc: "prefix only",
		inIPv4: &aftpb.Afts_Ipv4EntryKey{
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
		desc:    "nil update",
		inIPv4:  nil,
		wantErr: true,
	}, {
		desc:    "nil IPv4 prefix value",
		inIPv4:  &aftpb.Afts_Ipv4EntryKey{},
		wantErr: true,
	}, {
		desc: "nil IPv4 value",
		inIPv4: &aftpb.Afts_Ipv4EntryKey{
			Prefix: "8.8.8.8/32",
		},
		wantErr: true,
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			r := newRIBHolder()
			if err := r.AddIPv4(tt.inIPv4); (err != nil) != tt.wantErr {
				t.Fatalf("got unexpected error adding to entry, got: %v, wantErr? %v", err, tt.wantErr)
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
				{prefix: "1.1.1.1/32", metadata: []byte{1, 2, 3, 4}},
			}),
		},
		inEntry: &aftpb.Afts_Ipv4EntryKey{
			Prefix: "1.1.1.1/32",
			Ipv4Entry: &aftpb.Afts_Ipv4Entry{
				Metadata: &wpb.BytesValue{Value: []byte{1, 2, 3, 4}},
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

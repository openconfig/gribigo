package rib

import (
	"testing"

	aftpb "github.com/openconfig/gribi/v1/proto/gribi_aft"
	"github.com/openconfig/gribigo/aft"
	"github.com/openconfig/ygot/ygot"
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
			r := New()
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

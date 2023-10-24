package reconciler

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/openconfig/gribigo/rib"
	"google.golang.org/protobuf/testing/protocmp"

	aftpb "github.com/openconfig/gribi/v1/proto/gribi_aft"
	spb "github.com/openconfig/gribi/v1/proto/service"
	wpb "github.com/openconfig/ygot/proto/ywrapper"
)

func TestLocalRIB(t *testing.T) {
	t.Run("get", func(t *testing.T) {
		l := &LocalRIB{
			r: rib.New("DEFAULT"),
		}

		want := rib.New("DEFAULT")
		got, err := l.Get(context.Background())
		if err != nil {
			t.Fatalf("(*LocalRIB).Get(): did not get expected error, got: %v, want: nil", err)
		}

		if diff := cmp.Diff(got, want,
			cmpopts.EquateEmpty(), cmp.AllowUnexported(rib.RIB{}),
			cmpopts.IgnoreFields(rib.RIB{}, "nrMu", "pendMu", "ribCheck"),
			cmp.AllowUnexported(rib.RIBHolder{}),
			cmpopts.IgnoreFields(rib.RIBHolder{}, "mu", "refCounts", "checkFn"),
		); diff != "" {
			t.Fatalf("(*LocalRIB).Get(): did not get expected results, diff(-got,+want):\n%s", diff)
		}
	})
}

func TestDiff(t *testing.T) {
	dn := "DEFAULT"
	tests := []struct {
		desc    string
		inSrc   *rib.RIB
		inDst   *rib.RIB
		wantOps []*spb.AFTOperation
		wantErr bool
	}{{
		desc: "VRF NI in src, but not in dst",
		inSrc: func() *rib.RIB {
			r := rib.NewFake(dn)
			vrf := "FOO"
			if err := r.RIB().AddNetworkInstance(vrf); err != nil {
				t.Fatalf("cannot create NI, %v", err)
			}
			if err := r.InjectNH(vrf, 1); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			if err := r.InjectNHG(vrf, 1, map[uint64]uint64{1: 1}); err != nil {
				t.Fatalf("cannot add NHG, %v", err)
			}
			if err := r.InjectIPv4(vrf, "1.0.0.0/24", 1); err != nil {
				t.Fatalf("cannot add IPv4, %v", err)
			}
			return r.RIB()
		}(),
		inDst: rib.NewFake(dn).RIB(),
		wantOps: []*spb.AFTOperation{{
			Id:              1,
			NetworkInstance: "FOO",
			Op:              spb.AFTOperation_ADD,
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix: "1.0.0.0/24",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{
						NextHopGroup: &wpb.UintValue{Value: 1},
					},
				},
			},
		}},
	}, {
		desc: "default NI with differing IPv4 entries",
		inSrc: func() *rib.RIB {
			r := rib.NewFake(dn)
			if err := r.InjectNH(dn, 1); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			if err := r.InjectNHG(dn, 1, map[uint64]uint64{1: 1}); err != nil {
				t.Fatalf("cannot add NHG, %v", err)
			}
			if err := r.InjectIPv4(dn, "1.0.0.0/24", 1); err != nil {
				t.Fatalf("cannot add IPv4, %v", err)
			}
			return r.RIB()
		}(),
		inDst: func() *rib.RIB {
			r := rib.NewFake(dn)
			if err := r.InjectNH(dn, 1); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			if err := r.InjectNHG(dn, 1, map[uint64]uint64{1: 1}); err != nil {
				t.Fatalf("cannot add NHG, %v", err)
			}
			if err := r.InjectIPv4(dn, "2.0.0.0/24", 1); err != nil {
				t.Fatalf("cannot add IPv4, %v", err)
			}
			return r.RIB()
		}(),
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got, err := diff(tt.inSrc, tt.inDst)
			if (err != nil) != tt.wantErr {
				t.Fatalf("did not get expected error, got: %v, wantErr? %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(got, tt.wantOps, protocmp.Transform()); diff != "" {
				t.Fatalf("diff(%s, %s): did not get expected operations, diff(-got,+want):\n%s", tt.inSrc, tt.inDst, diff)
			}
		})
	}
}

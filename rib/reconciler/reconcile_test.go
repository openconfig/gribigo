package reconciler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

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
		desc              string
		inSrc             *rib.RIB
		inDst             *rib.RIB
		inExplicitReplace map[spb.AFTType]bool
		wantOps           *ReconcileOps
		wantErr           bool
	}{{
		desc: "VRF NI in src, but not in dst",
		inSrc: func() *rib.RIB {
			r := rib.NewFake(dn)
			vrf := "FOO"
			if err := r.RIB().AddNetworkInstance(vrf); err != nil {
				t.Fatalf("cannot create NI, %v", err)
			}
			if err := r.InjectNH(vrf, 1, "int42"); err != nil {
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
		wantOps: &ReconcileOps{
			Add: &Ops{
				NH: []*spb.AFTOperation{{
					Id:              3,
					NetworkInstance: "FOO",
					Op:              spb.AFTOperation_ADD,
					Entry: &spb.AFTOperation_NextHop{
						NextHop: &aftpb.Afts_NextHopKey{
							Index: 1,
							NextHop: &aftpb.Afts_NextHop{
								InterfaceRef: &aftpb.Afts_NextHop_InterfaceRef{
									Interface: &wpb.StringValue{Value: "int42"},
								},
							},
						},
					},
				}},
				NHG: []*spb.AFTOperation{{
					Id:              2,
					NetworkInstance: "FOO",
					Op:              spb.AFTOperation_ADD,
					Entry: &spb.AFTOperation_NextHopGroup{
						NextHopGroup: &aftpb.Afts_NextHopGroupKey{
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
					},
				}},
				TopLevel: []*spb.AFTOperation{{
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
			},
		},
	}, {
		desc: "default NI with added and removed IPv4 entries",
		inSrc: func() *rib.RIB {
			r := rib.NewFake(dn)
			if err := r.InjectNH(dn, 1, "int42"); err != nil {
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
			if err := r.InjectNH(dn, 1, "int42"); err != nil {
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
		wantOps: &ReconcileOps{
			Add: &Ops{
				TopLevel: []*spb.AFTOperation{{
					Id:              1,
					NetworkInstance: dn,
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
			},
			Delete: &Ops{
				TopLevel: []*spb.AFTOperation{{
					Id:              2,
					NetworkInstance: dn,
					Op:              spb.AFTOperation_DELETE,
					Entry: &spb.AFTOperation_Ipv4{
						Ipv4: &aftpb.Afts_Ipv4EntryKey{
							Prefix: "2.0.0.0/24",
							Ipv4Entry: &aftpb.Afts_Ipv4Entry{
								NextHopGroup: &wpb.UintValue{Value: 1},
							},
						},
					},
				}},
			},
		},
	}, {
		desc: "default NI with IPv4 entry with different contents",
		inSrc: func() *rib.RIB {
			r := rib.NewFake(dn)
			if err := r.InjectNH(dn, 1, "int42"); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			if err := r.InjectNHG(dn, 1, map[uint64]uint64{1: 1}); err != nil {
				t.Fatalf("cannot add NHG 1, %v", err)
			}
			if err := r.InjectNHG(dn, 2, map[uint64]uint64{1: 2}); err != nil {
				t.Fatalf("cannot add NHG 2, %v", err)
			}
			if err := r.InjectIPv4(dn, "1.0.0.0/24", 2); err != nil {
				t.Fatalf("cannot add IPv4, %v", err)
			}
			return r.RIB()
		}(),
		inDst: func() *rib.RIB {
			r := rib.NewFake(dn)
			if err := r.InjectNH(dn, 1, "int42"); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			if err := r.InjectNHG(dn, 1, map[uint64]uint64{1: 1}); err != nil {
				t.Fatalf("cannot add NHG 1, %v", err)
			}
			if err := r.InjectIPv4(dn, "1.0.0.0/24", 1); err != nil {
				t.Fatalf("cannot add IPv4, %v", err)
			}
			return r.RIB()
		}(),
		wantOps: &ReconcileOps{
			Add: &Ops{
				NHG: []*spb.AFTOperation{{
					Id:              2,
					NetworkInstance: dn,
					Op:              spb.AFTOperation_ADD,
					Entry: &spb.AFTOperation_NextHopGroup{
						NextHopGroup: &aftpb.Afts_NextHopGroupKey{
							Id: 2,
							NextHopGroup: &aftpb.Afts_NextHopGroup{
								NextHop: []*aftpb.Afts_NextHopGroup_NextHopKey{{
									Index: 1,
									NextHop: &aftpb.Afts_NextHopGroup_NextHop{
										Weight: &wpb.UintValue{Value: 2},
									},
								}},
							},
						},
					},
				}},
			},
			Replace: &Ops{
				TopLevel: []*spb.AFTOperation{{
					Id:              1,
					NetworkInstance: dn,
					Op:              spb.AFTOperation_ADD,
					Entry: &spb.AFTOperation_Ipv4{
						Ipv4: &aftpb.Afts_Ipv4EntryKey{
							Prefix: "1.0.0.0/24",
							Ipv4Entry: &aftpb.Afts_Ipv4Entry{
								NextHopGroup: &wpb.UintValue{Value: 2},
							},
						},
					},
				}},
			},
		},
	}, {
		desc: "default NI with IPv4 entry with different contents, explicit replace",
		inSrc: func() *rib.RIB {
			r := rib.NewFake(dn)
			if err := r.InjectNH(dn, 1, "int42"); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			if err := r.InjectNHG(dn, 1, map[uint64]uint64{1: 1}); err != nil {
				t.Fatalf("cannot add NHG 1, %v", err)
			}
			if err := r.InjectNHG(dn, 2, map[uint64]uint64{1: 2}); err != nil {
				t.Fatalf("cannot add NHG 2, %v", err)
			}
			if err := r.InjectIPv4(dn, "1.0.0.0/24", 2); err != nil {
				t.Fatalf("cannot add IPv4, %v", err)
			}
			return r.RIB()
		}(),
		inDst: func() *rib.RIB {
			r := rib.NewFake(dn)
			if err := r.InjectNH(dn, 1, "int42"); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			if err := r.InjectNHG(dn, 1, map[uint64]uint64{1: 1}); err != nil {
				t.Fatalf("cannot add NHG 1, %v", err)
			}
			if err := r.InjectIPv4(dn, "1.0.0.0/24", 1); err != nil {
				t.Fatalf("cannot add IPv4, %v", err)
			}
			return r.RIB()
		}(),
		inExplicitReplace: map[spb.AFTType]bool{
			spb.AFTType_IPV4: true,
		},
		wantOps: &ReconcileOps{
			Add: &Ops{
				NHG: []*spb.AFTOperation{{
					Id:              2,
					NetworkInstance: dn,
					Op:              spb.AFTOperation_ADD,
					Entry: &spb.AFTOperation_NextHopGroup{
						NextHopGroup: &aftpb.Afts_NextHopGroupKey{
							Id: 2,
							NextHopGroup: &aftpb.Afts_NextHopGroup{
								NextHop: []*aftpb.Afts_NextHopGroup_NextHopKey{{
									Index: 1,
									NextHop: &aftpb.Afts_NextHopGroup_NextHop{
										Weight: &wpb.UintValue{Value: 2},
									},
								}},
							},
						},
					},
				}},
			},
			Replace: &Ops{
				TopLevel: []*spb.AFTOperation{{
					Id:              1,
					NetworkInstance: dn,
					Op:              spb.AFTOperation_REPLACE,
					Entry: &spb.AFTOperation_Ipv4{
						Ipv4: &aftpb.Afts_Ipv4EntryKey{
							Prefix: "1.0.0.0/24",
							Ipv4Entry: &aftpb.Afts_Ipv4Entry{
								NextHopGroup: &wpb.UintValue{Value: 2},
							},
						},
					},
				}},
			},
		},
	}, {
		desc: "NHG installed",
		inSrc: func() *rib.RIB {
			r := rib.NewFake(dn)
			if err := r.InjectNH(dn, 1, "int42"); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			if err := r.InjectNHG(dn, 1, map[uint64]uint64{1: 1}); err != nil {
				t.Fatalf("cannot add NHG 1, %v", err)
			}
			return r.RIB()
		}(),
		inDst: func() *rib.RIB {
			r := rib.NewFake(dn)
			if err := r.InjectNH(dn, 1, "int42"); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			return r.RIB()
		}(),
		wantOps: &ReconcileOps{
			Add: &Ops{
				NHG: []*spb.AFTOperation{{
					Id:              1,
					NetworkInstance: dn,
					Op:              spb.AFTOperation_ADD,
					Entry: &spb.AFTOperation_NextHopGroup{
						NextHopGroup: &aftpb.Afts_NextHopGroupKey{
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
					},
				}},
			},
		},
	}, {
		desc: "NHG deleted",
		inSrc: func() *rib.RIB {
			r := rib.NewFake(dn)
			if err := r.InjectNH(dn, 1, "int42"); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			return r.RIB()
		}(),
		inDst: func() *rib.RIB {
			r := rib.NewFake(dn)
			if err := r.InjectNH(dn, 1, "int42"); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			if err := r.InjectNHG(dn, 1, map[uint64]uint64{1: 1}); err != nil {
				t.Fatalf("cannot add NHG 1, %v", err)
			}
			return r.RIB()
		}(),
		wantOps: &ReconcileOps{
			Delete: &Ops{
				NHG: []*spb.AFTOperation{{
					Id:              1,
					NetworkInstance: dn,
					Op:              spb.AFTOperation_DELETE,
					Entry: &spb.AFTOperation_NextHopGroup{
						NextHopGroup: &aftpb.Afts_NextHopGroupKey{
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
					},
				}},
			},
		},
	}, {
		desc: "NHG implicitly replaced",
		inSrc: func() *rib.RIB {
			r := rib.NewFake(dn)
			if err := r.InjectNH(dn, 1, "int42"); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			if err := r.InjectNH(dn, 2, "int42"); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			if err := r.InjectNHG(dn, 1, map[uint64]uint64{
				1: 2,
				2: 4,
			}); err != nil {
				t.Fatalf("cannot add NHG 1, %v", err)
			}
			return r.RIB()
		}(),
		inDst: func() *rib.RIB {
			r := rib.NewFake(dn)
			if err := r.InjectNH(dn, 1, "int42"); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			if err := r.InjectNH(dn, 2, "int42"); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			if err := r.InjectNHG(dn, 1, map[uint64]uint64{1: 1}); err != nil {
				t.Fatalf("cannot add NHG 1, %v", err)
			}
			return r.RIB()
		}(),
		wantOps: &ReconcileOps{
			Replace: &Ops{
				NHG: []*spb.AFTOperation{{
					Id:              1,
					NetworkInstance: dn,
					Op:              spb.AFTOperation_ADD,
					Entry: &spb.AFTOperation_NextHopGroup{
						NextHopGroup: &aftpb.Afts_NextHopGroupKey{
							Id: 1,
							NextHopGroup: &aftpb.Afts_NextHopGroup{
								NextHop: []*aftpb.Afts_NextHopGroup_NextHopKey{{
									Index: 1,
									NextHop: &aftpb.Afts_NextHopGroup_NextHop{
										Weight: &wpb.UintValue{Value: 2},
									},
								}, {
									Index: 2,
									NextHop: &aftpb.Afts_NextHopGroup_NextHop{
										Weight: &wpb.UintValue{Value: 4},
									},
								}},
							},
						},
					},
				}},
			},
		},
	}, {
		desc: "NHG explicitly replaced",
		inSrc: func() *rib.RIB {
			r := rib.NewFake(dn)
			if err := r.InjectNH(dn, 1, "int42"); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			if err := r.InjectNH(dn, 2, "int42"); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			if err := r.InjectNHG(dn, 1, map[uint64]uint64{
				1: 2,
				2: 4,
			}); err != nil {
				t.Fatalf("cannot add NHG 1, %v", err)
			}
			return r.RIB()
		}(),
		inDst: func() *rib.RIB {
			r := rib.NewFake(dn)
			if err := r.InjectNH(dn, 1, "int42"); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			if err := r.InjectNH(dn, 2, "int42"); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			if err := r.InjectNHG(dn, 1, map[uint64]uint64{1: 1}); err != nil {
				t.Fatalf("cannot add NHG 1, %v", err)
			}
			return r.RIB()
		}(),
		inExplicitReplace: map[spb.AFTType]bool{
			spb.AFTType_NEXTHOP_GROUP: true,
		},
		wantOps: &ReconcileOps{
			Replace: &Ops{
				NHG: []*spb.AFTOperation{{
					Id:              1,
					NetworkInstance: dn,
					Op:              spb.AFTOperation_REPLACE,
					Entry: &spb.AFTOperation_NextHopGroup{
						NextHopGroup: &aftpb.Afts_NextHopGroupKey{
							Id: 1,
							NextHopGroup: &aftpb.Afts_NextHopGroup{
								NextHop: []*aftpb.Afts_NextHopGroup_NextHopKey{{
									Index: 1,
									NextHop: &aftpb.Afts_NextHopGroup_NextHop{
										Weight: &wpb.UintValue{Value: 2},
									},
								}, {
									Index: 2,
									NextHop: &aftpb.Afts_NextHopGroup_NextHop{
										Weight: &wpb.UintValue{Value: 4},
									},
								}},
							},
						},
					},
				}},
			},
		},
	}, {
		desc: "NH added",
		inSrc: func() *rib.RIB {
			r := rib.NewFake(dn)
			if err := r.InjectNH(dn, 1, "int42"); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			if err := r.InjectNH(dn, 2, "int42"); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			return r.RIB()
		}(),
		inDst: func() *rib.RIB {
			r := rib.NewFake(dn)
			if err := r.InjectNH(dn, 1, "int42"); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			return r.RIB()
		}(),
		wantOps: &ReconcileOps{
			Add: &Ops{
				NH: []*spb.AFTOperation{{
					Id:              1,
					NetworkInstance: dn,
					Op:              spb.AFTOperation_ADD,
					Entry: &spb.AFTOperation_NextHop{
						NextHop: &aftpb.Afts_NextHopKey{
							Index: 2,
							NextHop: &aftpb.Afts_NextHop{
								InterfaceRef: &aftpb.Afts_NextHop_InterfaceRef{
									Interface: &wpb.StringValue{Value: "int42"},
								},
							},
						},
					},
				}},
			},
		},
	}, {
		desc: "NH deleted",
		inSrc: func() *rib.RIB {
			r := rib.NewFake(dn)
			if err := r.InjectNH(dn, 1, "int42"); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			return r.RIB()
		}(),
		inDst: func() *rib.RIB {
			r := rib.NewFake(dn)
			if err := r.InjectNH(dn, 1, "int42"); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			if err := r.InjectNH(dn, 2, "int42"); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			return r.RIB()
		}(),
		wantOps: &ReconcileOps{
			Delete: &Ops{
				NH: []*spb.AFTOperation{{
					Id:              1,
					NetworkInstance: dn,
					Op:              spb.AFTOperation_DELETE,
					Entry: &spb.AFTOperation_NextHop{
						NextHop: &aftpb.Afts_NextHopKey{
							Index: 2,
							NextHop: &aftpb.Afts_NextHop{
								InterfaceRef: &aftpb.Afts_NextHop_InterfaceRef{
									Interface: &wpb.StringValue{Value: "int42"},
								},
							},
						},
					},
				}},
			},
		},
	}, {
		desc: "NH replaced",
		inSrc: func() *rib.RIB {
			r := rib.NewFake(dn)
			if err := r.InjectNH(dn, 1, "INTERFACE1"); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			return r.RIB()
		}(),
		inDst: func() *rib.RIB {
			r := rib.NewFake(dn)
			if err := r.InjectNH(dn, 1, "INTERFACE42"); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			return r.RIB()
		}(),
		wantOps: &ReconcileOps{
			Replace: &Ops{
				NH: []*spb.AFTOperation{{
					Id:              1,
					NetworkInstance: dn,
					Op:              spb.AFTOperation_ADD,
					Entry: &spb.AFTOperation_NextHop{
						NextHop: &aftpb.Afts_NextHopKey{
							Index: 1,
							NextHop: &aftpb.Afts_NextHop{
								InterfaceRef: &aftpb.Afts_NextHop_InterfaceRef{
									Interface: &wpb.StringValue{Value: "INTERFACE1"},
								},
							},
						},
					},
				}},
			},
		},
	}, {
		desc: "NH replaced - explicitly",
		inSrc: func() *rib.RIB {
			r := rib.NewFake(dn)
			if err := r.InjectNH(dn, 1, "INTERFACE1"); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			return r.RIB()
		}(),
		inDst: func() *rib.RIB {
			r := rib.NewFake(dn)
			if err := r.InjectNH(dn, 1, "INTERFACE42"); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			return r.RIB()
		}(),
		inExplicitReplace: map[spb.AFTType]bool{
			spb.AFTType_NEXTHOP: true,
		},
		wantOps: &ReconcileOps{
			Replace: &Ops{
				NH: []*spb.AFTOperation{{
					Id:              1,
					NetworkInstance: dn,
					Op:              spb.AFTOperation_REPLACE,
					Entry: &spb.AFTOperation_NextHop{
						NextHop: &aftpb.Afts_NextHopKey{
							Index: 1,
							NextHop: &aftpb.Afts_NextHop{
								InterfaceRef: &aftpb.Afts_NextHop_InterfaceRef{
									Interface: &wpb.StringValue{Value: "INTERFACE1"},
								},
							},
						},
					},
				}},
			},
		},
	}, {
		desc:    "nil input",
		wantErr: true,
	}, {
		desc: "MPLS added",
		inSrc: func() *rib.RIB {
			r := rib.NewFake(dn)
			if err := r.InjectNH(dn, 1, "int42"); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			if err := r.InjectNHG(dn, 1, map[uint64]uint64{1: 1}); err != nil {
				t.Fatalf("cannot add NHG, %v", err)
			}
			if err := r.InjectMPLS(dn, 42, 1); err != nil {
				t.Fatalf("cannot add label entry, %v", err)
			}
			return r.RIB()
		}(),
		inDst: func() *rib.RIB {
			r := rib.NewFake(dn)
			if err := r.InjectNH(dn, 1, "int42"); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			if err := r.InjectNHG(dn, 1, map[uint64]uint64{1: 1}); err != nil {
				t.Fatalf("cannot add NHG, %v", err)
			}
			return r.RIB()
		}(),
		wantOps: &ReconcileOps{
			Add: &Ops{
				TopLevel: []*spb.AFTOperation{{
					Id:              1,
					NetworkInstance: dn,
					Op:              spb.AFTOperation_ADD,
					Entry: &spb.AFTOperation_Mpls{
						Mpls: &aftpb.Afts_LabelEntryKey{
							Label: &aftpb.Afts_LabelEntryKey_LabelUint64{
								LabelUint64: 42,
							},
							LabelEntry: &aftpb.Afts_LabelEntry{
								NextHopGroup: &wpb.UintValue{Value: 1},
							},
						},
					},
				}},
			},
		},
	}, {
		desc: "MPLS delete",
		inSrc: func() *rib.RIB {
			r := rib.NewFake(dn)
			if err := r.InjectNH(dn, 1, "int42"); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			if err := r.InjectNHG(dn, 1, map[uint64]uint64{1: 1}); err != nil {
				t.Fatalf("cannot add NHG, %v", err)
			}
			return r.RIB()
		}(),
		inDst: func() *rib.RIB {
			r := rib.NewFake(dn)
			if err := r.InjectNH(dn, 1, "int42"); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			if err := r.InjectNHG(dn, 1, map[uint64]uint64{1: 1}); err != nil {
				t.Fatalf("cannot add NHG, %v", err)
			}
			if err := r.InjectMPLS(dn, 42, 1); err != nil {
				t.Fatalf("cannot add label entry, %v", err)
			}
			return r.RIB()
		}(),
		wantOps: &ReconcileOps{
			Delete: &Ops{
				TopLevel: []*spb.AFTOperation{{
					Id:              1,
					NetworkInstance: dn,
					Op:              spb.AFTOperation_DELETE,
					Entry: &spb.AFTOperation_Mpls{
						Mpls: &aftpb.Afts_LabelEntryKey{
							Label: &aftpb.Afts_LabelEntryKey_LabelUint64{
								LabelUint64: 42,
							},
							LabelEntry: &aftpb.Afts_LabelEntry{
								NextHopGroup: &wpb.UintValue{Value: 1},
							},
						},
					},
				}},
			},
		},
	}, {
		desc: "MPLS implicit replace",
		inSrc: func() *rib.RIB {
			r := rib.NewFake(dn)
			if err := r.InjectNH(dn, 1, "int42"); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			if err := r.InjectNHG(dn, 1, map[uint64]uint64{1: 1}); err != nil {
				t.Fatalf("cannot add NHG, %v", err)
			}
			if err := r.InjectNHG(dn, 2, map[uint64]uint64{1: 1}); err != nil {
				t.Fatalf("cannot add NHG, %v", err)
			}
			if err := r.InjectMPLS(dn, 42, 2); err != nil {
				t.Fatalf("cannot add label entry, %v", err)
			}
			return r.RIB()
		}(),
		inDst: func() *rib.RIB {
			r := rib.NewFake(dn)
			if err := r.InjectNH(dn, 1, "int42"); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			if err := r.InjectNHG(dn, 2, map[uint64]uint64{1: 1}); err != nil {
				t.Fatalf("cannot add NHG, %v", err)
			}
			if err := r.InjectNHG(dn, 1, map[uint64]uint64{1: 1}); err != nil {
				t.Fatalf("cannot add NHG, %v", err)
			}
			if err := r.InjectMPLS(dn, 42, 1); err != nil {
				t.Fatalf("cannot add label entry, %v", err)
			}
			return r.RIB()
		}(),
		wantOps: &ReconcileOps{
			Replace: &Ops{
				TopLevel: []*spb.AFTOperation{{
					Id:              1,
					NetworkInstance: dn,
					Op:              spb.AFTOperation_ADD,
					Entry: &spb.AFTOperation_Mpls{
						Mpls: &aftpb.Afts_LabelEntryKey{
							Label: &aftpb.Afts_LabelEntryKey_LabelUint64{
								LabelUint64: 42,
							},
							LabelEntry: &aftpb.Afts_LabelEntry{
								NextHopGroup: &wpb.UintValue{Value: 2},
							},
						},
					},
				}},
			},
		},
	}, {
		desc: "MPLS explicit replace",
		inSrc: func() *rib.RIB {
			r := rib.NewFake(dn)
			if err := r.InjectNH(dn, 1, "int42"); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			if err := r.InjectNHG(dn, 1, map[uint64]uint64{1: 1}); err != nil {
				t.Fatalf("cannot add NHG, %v", err)
			}
			if err := r.InjectNHG(dn, 2, map[uint64]uint64{1: 1}); err != nil {
				t.Fatalf("cannot add NHG, %v", err)
			}
			if err := r.InjectMPLS(dn, 42, 2); err != nil {
				t.Fatalf("cannot add label entry, %v", err)
			}
			return r.RIB()
		}(),
		inDst: func() *rib.RIB {
			r := rib.NewFake(dn)
			if err := r.InjectNH(dn, 1, "int42"); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			if err := r.InjectNHG(dn, 1, map[uint64]uint64{1: 1}); err != nil {
				t.Fatalf("cannot add NHG, %v", err)
			}
			if err := r.InjectNHG(dn, 2, map[uint64]uint64{1: 1}); err != nil {
				t.Fatalf("cannot add NHG, %v", err)
			}
			if err := r.InjectMPLS(dn, 42, 1); err != nil {
				t.Fatalf("cannot add label entry, %v", err)
			}
			return r.RIB()
		}(),
		inExplicitReplace: map[spb.AFTType]bool{
			spb.AFTType_MPLS: true,
		},
		wantOps: &ReconcileOps{
			Replace: &Ops{
				TopLevel: []*spb.AFTOperation{{
					Id:              1,
					NetworkInstance: dn,
					Op:              spb.AFTOperation_REPLACE,
					Entry: &spb.AFTOperation_Mpls{
						Mpls: &aftpb.Afts_LabelEntryKey{
							Label: &aftpb.Afts_LabelEntryKey_LabelUint64{
								LabelUint64: 42,
							},
							LabelEntry: &aftpb.Afts_LabelEntry{
								NextHopGroup: &wpb.UintValue{Value: 2},
							},
						},
					},
				}},
			},
		},
	}, {
		desc: "explicit replace for all types",
		inSrc: func() *rib.RIB {
			r := rib.NewFake(dn)
			if err := r.InjectNH(dn, 1, "int42"); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			// NHG ID = 1 is unchanged.
			if err := r.InjectNHG(dn, 1, map[uint64]uint64{1: 1}); err != nil {
				t.Fatalf("cannot add NHG, %v", err)
			}
			if err := r.InjectNHG(dn, 2, map[uint64]uint64{1: 64}); err != nil {
				t.Fatalf("cannot add NHG, %v", err)
			}
			if err := r.InjectMPLS(dn, 42, 2); err != nil {
				t.Fatalf("cannot add label entry, %v", err)
			}
			if err := r.InjectIPv4(dn, "1.0.0.0/24", 2); err != nil {
				t.Fatalf("cannot add IPv4 entry, %v", err)
			}
			return r.RIB()
		}(),
		inDst: func() *rib.RIB {
			r := rib.NewFake(dn)
			if err := r.InjectNH(dn, 1, "int10"); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			if err := r.InjectNHG(dn, 1, map[uint64]uint64{1: 1}); err != nil {
				t.Fatalf("cannot add NHG, %v", err)
			}
			if err := r.InjectNHG(dn, 2, map[uint64]uint64{1: 1}); err != nil {
				t.Fatalf("cannot add NHG, %v", err)
			}
			if err := r.InjectMPLS(dn, 42, 1); err != nil {
				t.Fatalf("cannot add label entry, %v", err)
			}
			if err := r.InjectIPv4(dn, "1.0.0.0/24", 1); err != nil {
				t.Fatalf("cannot add IPv4 entry, %v", err)
			}
			return r.RIB()
		}(),
		inExplicitReplace: map[spb.AFTType]bool{
			spb.AFTType_ALL: true,
		},
		wantOps: &ReconcileOps{
			Replace: &Ops{
				TopLevel: []*spb.AFTOperation{{
					Id:              1,
					NetworkInstance: dn,
					Op:              spb.AFTOperation_REPLACE,
					Entry: &spb.AFTOperation_Ipv4{
						Ipv4: &aftpb.Afts_Ipv4EntryKey{
							Prefix: "1.0.0.0/24",
							Ipv4Entry: &aftpb.Afts_Ipv4Entry{
								NextHopGroup: &wpb.UintValue{Value: 2},
							},
						},
					},
				}, {
					Id:              2,
					NetworkInstance: dn,
					Op:              spb.AFTOperation_REPLACE,
					Entry: &spb.AFTOperation_Mpls{
						Mpls: &aftpb.Afts_LabelEntryKey{
							Label: &aftpb.Afts_LabelEntryKey_LabelUint64{
								LabelUint64: 42,
							},
							LabelEntry: &aftpb.Afts_LabelEntry{
								NextHopGroup: &wpb.UintValue{Value: 2},
							},
						},
					},
				}},
				NHG: []*spb.AFTOperation{{
					Id:              3,
					NetworkInstance: dn,
					Op:              spb.AFTOperation_REPLACE,
					Entry: &spb.AFTOperation_NextHopGroup{
						NextHopGroup: &aftpb.Afts_NextHopGroupKey{
							Id: 2,
							NextHopGroup: &aftpb.Afts_NextHopGroup{
								NextHop: []*aftpb.Afts_NextHopGroup_NextHopKey{{
									Index: 1,
									NextHop: &aftpb.Afts_NextHopGroup_NextHop{
										Weight: &wpb.UintValue{Value: 64},
									},
								}},
							},
						},
					},
				}},
				NH: []*spb.AFTOperation{{
					Id:              4,
					NetworkInstance: dn,
					Op:              spb.AFTOperation_REPLACE,
					Entry: &spb.AFTOperation_NextHop{
						NextHop: &aftpb.Afts_NextHopKey{
							Index: 1,
							NextHop: &aftpb.Afts_NextHop{
								InterfaceRef: &aftpb.Afts_NextHop_InterfaceRef{
									Interface: &wpb.StringValue{Value: "int42"},
								},
							},
						},
					},
				}},
			},
		},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			id := &atomic.Uint64{}
			got, err := diff(tt.inSrc, tt.inDst, tt.inExplicitReplace, id)
			if (err != nil) != tt.wantErr {
				t.Fatalf("did not get expected error, got: %v, wantErr? %v", err, tt.wantErr)
			}

			if err != nil {
				return
			}

			// Keep the test input as pithy as possible whilst ensuring safe adds
			// in the real implementation.
			if tt.wantOps.Add == nil {
				tt.wantOps.Add = &Ops{}
			}
			if tt.wantOps.Replace == nil {
				tt.wantOps.Replace = &Ops{}
			}
			if tt.wantOps.Delete == nil {
				tt.wantOps.Delete = &Ops{}
			}

			if diff := cmp.Diff(got, tt.wantOps,
				cmpopts.EquateEmpty(),
				protocmp.Transform(),
				protocmp.SortRepeatedFields(&aftpb.Afts_NextHopGroup{}, "next_hop"),
			); diff != "" {
				t.Fatalf("diff(%s, %s): did not get expected operations, diff(-got,+want):\n%s", tt.inSrc, tt.inDst, diff)
			}
		})
	}
}

func TestReconcile(t *testing.T) {
	dn := "DEFAULT"
	tests := []struct {
		desc       string
		inIntended RIBTarget
		inTarget   RIBTarget
		inID       *atomic.Uint64
		wantOps    *ReconcileOps
		wantErr    bool
	}{{
		desc: "local-local: one entry in intended, not in target",
		inIntended: func() RIBTarget {
			r := rib.NewFake(dn, rib.DisableRIBCheckFn())
			if err := r.InjectIPv4(dn, "1.0.0.0/24", 1); err != nil {
				t.Fatalf("cannot add prefix to intended, %v", err)
			}
			return NewLocalRIB(r.RIB())
		}(),
		inTarget: func() RIBTarget {
			r := rib.NewFake(dn, rib.DisableRIBCheckFn())
			return NewLocalRIB(r.RIB())
		}(),
		inID: &atomic.Uint64{},
		wantOps: &ReconcileOps{
			Add: &Ops{
				TopLevel: []*spb.AFTOperation{{
					Id:              1,
					NetworkInstance: dn,
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
			},
			Replace: &Ops{},
			Delete:  &Ops{},
		},
	}, {
		desc: "local-local: no differences between intended and target",
		inIntended: func() RIBTarget {
			r := rib.NewFake(dn, rib.DisableRIBCheckFn())
			if err := r.InjectNHG(dn, 100, map[uint64]uint64{
				1: 10,
				2: 20,
				3: 30,
			}); err != nil {
				t.Fatalf("cannot add NHG, err: %v", err)
			}
			return NewLocalRIB(r.RIB())
		}(),
		inTarget: func() RIBTarget {
			r := rib.NewFake(dn, rib.DisableRIBCheckFn())
			if err := r.InjectNHG(dn, 100, map[uint64]uint64{
				1: 10,
				2: 20,
				3: 30,
			}); err != nil {
				t.Fatalf("cannot add NHG, err: %v", err)
			}
			return NewLocalRIB(r.RIB())
		}(),
		inID: &atomic.Uint64{},
		wantOps: &ReconcileOps{
			Add:     &Ops{},
			Replace: &Ops{},
			Delete:  &Ops{},
		},
	}, {
		desc:       "local-local: entry in target that is not in intended",
		inIntended: NewLocalRIB(rib.NewFake(dn, rib.DisableRIBCheckFn()).RIB()),
		inTarget: func() RIBTarget {
			r := rib.NewFake(dn, rib.DisableRIBCheckFn())
			if err := r.InjectNH(dn, 1, "eth0"); err != nil {
				t.Fatalf("cannot add NH, err: %v", err)
			}
			return NewLocalRIB(r.RIB())
		}(),
		inID: &atomic.Uint64{},
		wantOps: &ReconcileOps{
			Add:     &Ops{},
			Replace: &Ops{},
			Delete: &Ops{
				NH: []*spb.AFTOperation{{
					Id:              1,
					NetworkInstance: dn,
					Op:              spb.AFTOperation_DELETE,
					Entry: &spb.AFTOperation_NextHop{
						NextHop: &aftpb.Afts_NextHopKey{
							Index: 1,
							NextHop: &aftpb.Afts_NextHop{
								InterfaceRef: &aftpb.Afts_NextHop_InterfaceRef{
									Interface: &wpb.StringValue{Value: "eth0"},
								},
							},
						},
					},
				}},
			},
		},
	}, {
		desc: "local-local: non-zero starting ID",
		inIntended: func() RIBTarget {
			r := rib.NewFake(dn, rib.DisableRIBCheckFn())
			if err := r.InjectIPv4(dn, "1.0.0.0/24", 1); err != nil {
				t.Fatalf("cannot add prefix to intended, %v", err)
			}
			return NewLocalRIB(r.RIB())
		}(),
		inTarget: func() RIBTarget {
			r := rib.NewFake(dn, rib.DisableRIBCheckFn())
			return NewLocalRIB(r.RIB())
		}(),
		inID: func() *atomic.Uint64 {
			u := &atomic.Uint64{}
			u.Store(42)
			return u
		}(),
		wantOps: &ReconcileOps{
			Add: &Ops{
				TopLevel: []*spb.AFTOperation{{
					Id:              43,
					NetworkInstance: dn,
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
			},
			Replace: &Ops{},
			Delete:  &Ops{},
		},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			rr := New(tt.inIntended, tt.inTarget)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			got, err := rr.Reconcile(ctx, tt.inID)

			if (err != nil) != tt.wantErr {
				t.Fatalf("did not get expected error, got: %v, wantErr? %v", err, tt.wantErr)
			}

			if diff := cmp.Diff(got, tt.wantOps,
				protocmp.Transform(),
				cmpopts.EquateEmpty(),
			); diff != "" {
				t.Fatalf("did not get expected RIB, diff(-got,+want):\n%s", diff)
			}
		})
	}
}

func TestReconcileRemote(t *testing.T) {
	dn := "DEFAULT"
	tests := []struct {
		desc                  string
		inIntendedServer      func(*testing.T, *rib.RIB) (string, func())
		inTargetServer        func(*testing.T, *rib.RIB) (string, func())
		inInjectedIntendedRIB *rib.RIB
		inInjectedRemoteRIB   *rib.RIB
		inID                  *atomic.Uint64
		wantOps               *ReconcileOps
		wantErr               bool
	}{{
		desc:                  "no routes to add",
		inIntendedServer:      newServer,
		inTargetServer:        newServer,
		inInjectedIntendedRIB: rib.New(dn, rib.DisableRIBCheckFn()),
		inInjectedRemoteRIB:   rib.New(dn, rib.DisableRIBCheckFn()),
		inID:                  &atomic.Uint64{},
		wantOps: &ReconcileOps{
			Add:     &Ops{},
			Delete:  &Ops{},
			Replace: &Ops{},
		},
	}, {
		desc:             "route to add from intended to target",
		inIntendedServer: newServer,
		inTargetServer:   newServer,
		inInjectedIntendedRIB: func() *rib.RIB {
			r := rib.NewFake(dn, rib.DisableRIBCheckFn())
			for i, a := range []string{"84.18.210.139/32", "142.250.72.174/32"} {
				if err := r.InjectIPv4(dn, a, uint64(i+1)); err != nil {
					t.Fatalf("cannot inject %s to intended, %v", a, err)
				}
			}
			return r.RIB()
		}(),
		inInjectedRemoteRIB: func() *rib.RIB {
			r := rib.NewFake(dn, rib.DisableRIBCheckFn())
			if err := r.InjectIPv4(dn, "84.18.210.139/32", 1); err != nil {
				t.Fatalf("cannot inject IPV4 prefix to intended, %v", err)
			}
			return r.RIB()
		}(),
		inID: func() *atomic.Uint64 {
			u := &atomic.Uint64{}
			u.Store(41)
			return u
		}(),
		wantOps: &ReconcileOps{
			Add: &Ops{
				TopLevel: []*spb.AFTOperation{{
					Id:              42,
					NetworkInstance: dn,
					Op:              spb.AFTOperation_ADD,
					Entry: &spb.AFTOperation_Ipv4{
						Ipv4: &aftpb.Afts_Ipv4EntryKey{
							Prefix: "142.250.72.174/32",
							Ipv4Entry: &aftpb.Afts_Ipv4Entry{
								NextHopGroup: &wpb.UintValue{Value: 2},
							},
						},
					},
				}},
			},
			Replace: &Ops{},
			Delete:  &Ops{},
		},
	}, {
		desc:                  "delete from target",
		inIntendedServer:      newServer,
		inTargetServer:        newServer,
		inInjectedIntendedRIB: rib.New(dn, rib.DisableRIBCheckFn()),
		inInjectedRemoteRIB: func() *rib.RIB {
			r := rib.NewFake(dn, rib.DisableRIBCheckFn())
			if err := r.InjectIPv4(dn, "84.18.210.139/32", 1); err != nil {
				t.Fatalf("cannot inject IPV4 prefix to intended, %v", err)
			}
			return r.RIB()
		}(),
		inID: func() *atomic.Uint64 {
			u := &atomic.Uint64{}
			u.Store(41)
			return u
		}(),
		wantOps: &ReconcileOps{
			Add:     &Ops{},
			Replace: &Ops{},
			Delete: &Ops{
				TopLevel: []*spb.AFTOperation{{
					Id:              42,
					NetworkInstance: dn,
					Op:              spb.AFTOperation_DELETE,
					Entry: &spb.AFTOperation_Ipv4{
						Ipv4: &aftpb.Afts_Ipv4EntryKey{
							Prefix: "84.18.210.139/32",
							Ipv4Entry: &aftpb.Afts_Ipv4Entry{
								NextHopGroup: &wpb.UintValue{Value: 1},
							},
						},
					},
				}},
			},
		},
	}, {
		desc:                  "badly behaving target",
		inIntendedServer:      newServer,
		inTargetServer:        newBadServer,
		inInjectedIntendedRIB: rib.New(dn, rib.DisableRIBCheckFn()),
		inInjectedRemoteRIB:   rib.New(dn, rib.DisableRIBCheckFn()),
		inID:                  &atomic.Uint64{},
		wantErr:               true,
	}, {
		desc:                  "sleepy intended server",
		inIntendedServer:      newHangingServer,
		inTargetServer:        newServer,
		inInjectedIntendedRIB: rib.New(dn, rib.DisableRIBCheckFn()),
		inInjectedRemoteRIB:   rib.New(dn, rib.DisableRIBCheckFn()),
		inID:                  &atomic.Uint64{},
		wantErr:               true,
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			intendedAddr, intendedStop := tt.inIntendedServer(t, tt.inInjectedIntendedRIB)
			defer intendedStop()

			targetAddr, targetStop := tt.inTargetServer(t, tt.inInjectedRemoteRIB)
			defer targetStop()

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()

			intendedTarget, err := NewRemoteRIB(ctx, dn, intendedAddr)
			if err != nil {
				t.Fatalf("cannot create intended RIBTarget, %v", err)
			}

			targetTarget, err := NewRemoteRIB(ctx, dn, targetAddr)
			if err != nil {
				t.Fatalf("cannot create target RIBTarget, %v", err)
			}

			rr := New(intendedTarget, targetTarget)

			got, err := rr.Reconcile(ctx, tt.inID)
			if (err != nil) != tt.wantErr {
				t.Fatalf("(reconciler.Reconcile()): cannot calculate difference, got unexpected err: %v", err)
			}

			if diff := cmp.Diff(got, tt.wantOps, protocmp.Transform()); diff != "" {
				t.Fatalf("reconciler.Reconcile(): did not get expected ops, diff(-got,+want):\n%s", diff)
			}
		})
	}
}

func TestMergeOps(t *testing.T) {
	tests := []struct {
		desc       string
		inOriginal *Ops
		inInput    *Ops
		want       *Ops
	}{{
		desc:       "merging to empty",
		inOriginal: &Ops{},
		inInput: &Ops{
			TopLevel: []*spb.AFTOperation{{
				Id: 1,
			}},
			NHG: []*spb.AFTOperation{{
				Id: 2,
			}},
			NH: []*spb.AFTOperation{{
				Id: 3,
			}},
		},
		want: &Ops{
			TopLevel: []*spb.AFTOperation{{
				Id: 1,
			}},
			NHG: []*spb.AFTOperation{{
				Id: 2,
			}},
			NH: []*spb.AFTOperation{{
				Id: 3,
			}},
		},
	}, {
		desc: "merging to populated",
		inOriginal: &Ops{
			TopLevel: []*spb.AFTOperation{{
				Id: 10,
			}},
			NHG: []*spb.AFTOperation{{
				Id: 20,
			}},
			NH: []*spb.AFTOperation{{
				Id: 30,
			}},
		},
		inInput: &Ops{
			TopLevel: []*spb.AFTOperation{{
				Id: 1,
			}},
			NHG: []*spb.AFTOperation{{
				Id: 2,
			}},
			NH: []*spb.AFTOperation{{
				Id: 3,
			}},
		},
		want: &Ops{
			TopLevel: []*spb.AFTOperation{{
				Id: 10,
			}, {
				Id: 1,
			}},
			NHG: []*spb.AFTOperation{{
				Id: 20,
			}, {
				Id: 2,
			}},
			NH: []*spb.AFTOperation{{
				Id: 30,
			}, {
				Id: 3,
			}},
		},
	}, {
		desc: "nil merged in",
		inOriginal: &Ops{
			TopLevel: []*spb.AFTOperation{{
				Id: 10,
			}},
			NHG: []*spb.AFTOperation{{
				Id: 20,
			}},
			NH: []*spb.AFTOperation{{
				Id: 30,
			}},
		},
		inInput: nil,
		want: &Ops{
			TopLevel: []*spb.AFTOperation{{
				Id: 10,
			}},
			NHG: []*spb.AFTOperation{{
				Id: 20,
			}},
			NH: []*spb.AFTOperation{{
				Id: 30,
			}},
		},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			tt.inOriginal.Merge(tt.inInput)
			if diff := cmp.Diff(tt.inOriginal, tt.want, protocmp.Transform()); diff != "" {
				t.Fatalf("&Ops{}.Merge(): did not get expected result, diff(-got,+want):\n%s", diff)
			}
		})
	}
}

func TestMergeReconcileOps(t *testing.T) {
	tests := []struct {
		desc       string
		inOriginal *ReconcileOps
		inInput    *ReconcileOps
		want       *ReconcileOps
	}{{
		desc:       "merging to empty",
		inOriginal: NewReconcileOps(),
		inInput: &ReconcileOps{
			Add: &Ops{
				TopLevel: []*spb.AFTOperation{{
					Id: 1,
				}},
			},
			Delete: &Ops{
				TopLevel: []*spb.AFTOperation{{
					Id: 1,
				}},
			},
			Replace: &Ops{
				TopLevel: []*spb.AFTOperation{{
					Id: 1,
				}},
			},
		},
		want: &ReconcileOps{
			Add: &Ops{
				TopLevel: []*spb.AFTOperation{{
					Id: 1,
				}},
			},
			Delete: &Ops{
				TopLevel: []*spb.AFTOperation{{
					Id: 1,
				}},
			},
			Replace: &Ops{
				TopLevel: []*spb.AFTOperation{{
					Id: 1,
				}},
			},
		},
	}, {
		desc: "merging to populated",
		inOriginal: &ReconcileOps{
			Add: &Ops{
				TopLevel: []*spb.AFTOperation{{
					Id: 10,
				}},
			},
			Delete: &Ops{
				TopLevel: []*spb.AFTOperation{{
					Id: 101,
				}},
			},
			Replace: &Ops{
				TopLevel: []*spb.AFTOperation{{
					Id: 1001,
				}},
			},
		},
		inInput: &ReconcileOps{
			Add: &Ops{
				TopLevel: []*spb.AFTOperation{{
					Id: 1,
				}},
			},
			Delete: &Ops{
				TopLevel: []*spb.AFTOperation{{
					Id: 1,
				}},
			},
			Replace: &Ops{
				TopLevel: []*spb.AFTOperation{{
					Id: 1,
				}},
			},
		},
		want: &ReconcileOps{
			Add: &Ops{
				TopLevel: []*spb.AFTOperation{{
					Id: 10,
				}, {
					Id: 1,
				}},
			},
			Delete: &Ops{
				TopLevel: []*spb.AFTOperation{{
					Id: 101,
				}, {
					Id: 1,
				}},
			},
			Replace: &Ops{
				TopLevel: []*spb.AFTOperation{{
					Id: 1001,
				}, {
					Id: 1,
				}},
			},
		},
	}, {
		desc: "merging nil in",
		inOriginal: &ReconcileOps{
			Add: &Ops{
				TopLevel: []*spb.AFTOperation{{
					Id: 10,
				}},
			},
			Delete: &Ops{
				TopLevel: []*spb.AFTOperation{{
					Id: 101,
				}},
			},
			Replace: &Ops{
				TopLevel: []*spb.AFTOperation{{
					Id: 1001,
				}},
			},
		},
		want: &ReconcileOps{
			Add: &Ops{
				TopLevel: []*spb.AFTOperation{{
					Id: 10,
				}},
			},
			Delete: &Ops{
				TopLevel: []*spb.AFTOperation{{
					Id: 101,
				}},
			},
			Replace: &Ops{
				TopLevel: []*spb.AFTOperation{{
					Id: 1001,
				}},
			},
		},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			tt.inOriginal.Merge(tt.inInput)
			if diff := cmp.Diff(tt.inOriginal, tt.want, protocmp.Transform()); diff != "" {
				t.Fatalf("&ReconcileOps.Merge(): did not get expected result, diff(-got,want):\n%s", diff)
			}
		})
	}
}

func TestReconcileOpsIsEmpty(t *testing.T) {
	tests := []struct {
		desc string
		in   *ReconcileOps
		want bool
	}{{
		desc: "nil",
		want: true,
	}, {
		desc: "empty",
		in:   NewReconcileOps(),
		want: true,
	}, {
		desc: "not empty: add",
		in: &ReconcileOps{
			Add: &Ops{
				NH:       []*spb.AFTOperation{{}},
				NHG:      []*spb.AFTOperation{{}},
				TopLevel: []*spb.AFTOperation{{}},
			},
			Delete:  &Ops{},
			Replace: &Ops{},
		},
		want: false,
	}, {
		desc: "not empty: delete",
		in: &ReconcileOps{
			Add: &Ops{},
			Delete: &Ops{
				NH:       []*spb.AFTOperation{{}},
				NHG:      []*spb.AFTOperation{{}},
				TopLevel: []*spb.AFTOperation{{}},
			},
			Replace: &Ops{},
		},
		want: false,
	}, {
		desc: "not empty: replace",
		in: &ReconcileOps{
			Add:    &Ops{},
			Delete: &Ops{},
			Replace: &Ops{
				NH:       []*spb.AFTOperation{{}},
				NHG:      []*spb.AFTOperation{{}},
				TopLevel: []*spb.AFTOperation{{}},
			},
		},
		want: false,
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			if got := tt.in.IsEmpty(); got != tt.want {
				t.Fatalf("(%v).IsEmpty(): did not get expected result, got: %v, want: %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestOpsIsEmpty(t *testing.T) {
	tests := []struct {
		desc string
		in   *Ops
		want bool
	}{{
		desc: "empty",
		in:   &Ops{},
		want: true,
	}, {
		desc: "nil",
		want: true,
	}, {
		desc: "not empty: top-level",
		in: &Ops{
			TopLevel: []*spb.AFTOperation{{}},
		},
		want: false,
	}, {
		desc: "not empty: nhg",
		in: &Ops{
			NHG: []*spb.AFTOperation{{}},
		},
		want: false,
	}, {
		desc: "not empty: nh",
		in: &Ops{
			NH: []*spb.AFTOperation{{}},
		},
		want: false,
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			if got := tt.in.IsEmpty(); got != tt.want {
				t.Fatalf("(%v).IsEmpty(): did not get expected result, got: %v, want: %v", tt.in, got, tt.want)
			}
		})
	}
}

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
		desc              string
		inSrc             *rib.RIB
		inDst             *rib.RIB
		inExplicitReplace map[spb.AFTType]bool
		wantOps           *reconcileOps
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
		wantOps: &reconcileOps{
			Add: &ops{
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
		wantOps: &reconcileOps{
			Add: &ops{
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
			Delete: &ops{
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
		wantOps: &reconcileOps{
			Add: &ops{
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
			Replace: &ops{
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
		wantOps: &reconcileOps{
			Add: &ops{
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
			Replace: &ops{
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
		wantOps: &reconcileOps{
			Add: &ops{
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
		wantOps: &reconcileOps{
			Delete: &ops{
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
		wantOps: &reconcileOps{
			Replace: &ops{
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
		wantOps: &reconcileOps{
			Replace: &ops{
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
		wantOps: &reconcileOps{
			Add: &ops{
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
		wantOps: &reconcileOps{
			Delete: &ops{
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
		wantOps: &reconcileOps{
			Replace: &ops{
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
		wantOps: &reconcileOps{
			Replace: &ops{
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
		wantOps: &reconcileOps{
			Add: &ops{
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
		wantOps: &reconcileOps{
			Delete: &ops{
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
		wantOps: &reconcileOps{
			Replace: &ops{
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
		wantOps: &reconcileOps{
			Replace: &ops{
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
		wantOps: &reconcileOps{
			Replace: &ops{
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
			got, err := diff(tt.inSrc, tt.inDst, tt.inExplicitReplace)
			if (err != nil) != tt.wantErr {
				t.Fatalf("did not get expected error, got: %v, wantErr? %v", err, tt.wantErr)
			}

			if err != nil {
				return
			}

			// Keep the test input as pithy as possible whilst ensuring safe adds
			// in the real implementation.
			if tt.wantOps.Add == nil {
				tt.wantOps.Add = &ops{}
			}
			if tt.wantOps.Replace == nil {
				tt.wantOps.Replace = &ops{}
			}
			if tt.wantOps.Delete == nil {
				tt.wantOps.Delete = &ops{}
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

// Copyright 2026 Google LLC
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

package protorib

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/openconfig/gnmi/errdiff"
	"github.com/openconfig/gribigo/constants"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"

	aftpb "github.com/openconfig/gribi/v1/proto/gribi_aft"
	enumpb "github.com/openconfig/gribi/v1/proto/gribi_aft/enums"
	spb "github.com/openconfig/gribi/v1/proto/service"
	wpb "github.com/openconfig/ygot/proto/ywrapper"
)

// ribType enumerates the AFT entry types exercised in table-driven RIBHolder tests.
type ribType int64

// ribType values identify the target AFT table in TestAdd.
const (
	_ ribType = iota
	ipv4
	nhg
	nh
	mpls
	ipv6
)

// defName is the default network instance name used in tests.
const defName = "DEFAULT"

// TestAdd verifies that AddIPv4, AddIPv6, AddMPLS, AddNextHopGroup, and AddNextHop validate,
// clone, and install entries into a RIBHolder as expected.
func TestAdd(t *testing.T) {
	tests := []struct {
		// desc is the human-readable test case description.
		desc string
		// inRIBHolder is the target RIBHolder under test.
		inRIBHolder *RIBHolder
		// inType specifies which Add* method to invoke.
		inType ribType
		// inEntry is the input protobuf entry message.
		inEntry proto.Message
		// inExplicitReplace specifies whether explicitReplace is passed to Add*.
		inExplicitReplace bool
		// wantInstalled is the expected installed boolean result.
		wantInstalled bool
		// wantReplace is the expected replaced protobuf entry message, if any.
		wantReplace proto.Message
		// wantAFTs is the expected AFTs state after the operation.
		wantAFTs *AFTs
		// wantErr indicates whether an error is expected.
		wantErr bool
	}{{
		desc:        "ipv4 prefix only",
		inRIBHolder: NewRIBHolder("DEFAULT"),
		inType:      ipv4,
		inEntry: &aftpb.Afts_Ipv4EntryKey{
			Prefix:    "1.0.0.0/24",
			Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
		},
		wantInstalled: true,
		wantAFTs: &AFTs{
			IPv4Entry: map[string]*aftpb.Afts_Ipv4EntryKey{
				"1.0.0.0/24": {
					Prefix:    "1.0.0.0/24",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
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
		wantAFTs: &AFTs{
			IPv4Entry: map[string]*aftpb.Afts_Ipv4EntryKey{
				"1.0.0.0/24": {
					Prefix: "1.0.0.0/24",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{
						EntryMetadata: &wpb.BytesValue{Value: []byte{0, 1, 2, 3, 4, 5, 6, 7}},
					},
				},
			},
		},
	}, {
		desc:        "ipv4 metadata exceeds 8 bytes - error",
		inRIBHolder: NewRIBHolder("DEFAULT"),
		inType:      ipv4,
		inEntry: &aftpb.Afts_Ipv4EntryKey{
			Prefix: "1.0.0.0/24",
			Ipv4Entry: &aftpb.Afts_Ipv4Entry{
				EntryMetadata: &wpb.BytesValue{Value: []byte{0, 1, 2, 3, 4, 5, 6, 7, 8}},
			},
		},
		wantErr: true,
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
		wantReplace: &aftpb.Afts_Ipv4EntryKey{
			Prefix: "8.8.8.8/32",
			Ipv4Entry: &aftpb.Afts_Ipv4Entry{
				EntryMetadata: &wpb.BytesValue{Value: []byte{0, 1, 2, 3, 4, 5, 6, 7}},
			},
		},
		wantAFTs: &AFTs{
			IPv4Entry: map[string]*aftpb.Afts_Ipv4EntryKey{
				"8.8.8.8/32": {
					Prefix: "8.8.8.8/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{
						EntryMetadata: &wpb.BytesValue{Value: []byte{10, 11, 12, 13, 14, 15, 16, 17}},
					},
				},
			},
		},
	}, {
		desc:        "ipv4 replace with invalid data",
		inRIBHolder: NewRIBHolder("DEFAULT"),
		inType:      ipv4,
		inEntry: &aftpb.Afts_Ipv4EntryKey{
			Prefix:    "NOT AN IPV4 PREFIX",
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
		wantAFTs: &AFTs{
			NextHopGroup: map[uint64]*aftpb.Afts_NextHopGroupKey{
				1: {
					Id:           1,
					NextHopGroup: &aftpb.Afts_NextHopGroup{},
				},
			},
		},
	}, {
		desc:        "nhg with duplicate next-hop index - error",
		inRIBHolder: NewRIBHolder("DEFAULT"),
		inType:      nhg,
		inEntry: &aftpb.Afts_NextHopGroupKey{
			Id: 1,
			NextHopGroup: &aftpb.Afts_NextHopGroup{
				NextHop: []*aftpb.Afts_NextHopGroup_NextHopKey{{
					Index:   1,
					NextHop: &aftpb.Afts_NextHopGroup_NextHop{},
				}, {
					Index:   1,
					NextHop: &aftpb.Afts_NextHopGroup_NextHop{},
				}},
			},
		},
		wantErr: true,
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
		wantReplace: &aftpb.Afts_NextHopGroupKey{
			Id:           1,
			NextHopGroup: &aftpb.Afts_NextHopGroup{},
		},
		wantAFTs: &AFTs{
			NextHopGroup: map[uint64]*aftpb.Afts_NextHopGroupKey{
				1: {
					Id:           1,
					NextHopGroup: &aftpb.Afts_NextHopGroup{},
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
		wantReplace: &aftpb.Afts_NextHopKey{
			Index:   1,
			NextHop: &aftpb.Afts_NextHop{},
		},
		wantAFTs: &AFTs{
			NextHop: map[uint64]*aftpb.Afts_NextHopKey{
				1: {
					Index:   1,
					NextHop: &aftpb.Afts_NextHop{},
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
		wantReplace: &aftpb.Afts_NextHopGroupKey{
			Id:           1,
			NextHopGroup: &aftpb.Afts_NextHopGroup{},
		},
		wantAFTs: &AFTs{
			NextHopGroup: map[uint64]*aftpb.Afts_NextHopGroupKey{
				1: {
					Id: 1,
					NextHopGroup: &aftpb.Afts_NextHopGroup{
						Color: &wpb.UintValue{Value: 42},
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
		wantReplace: &aftpb.Afts_NextHopKey{
			Index:   1,
			NextHop: &aftpb.Afts_NextHop{},
		},
		wantAFTs: &AFTs{
			NextHop: map[uint64]*aftpb.Afts_NextHopKey{
				1: {
					Index: 1,
					NextHop: &aftpb.Afts_NextHop{
						IpAddress: &wpb.StringValue{Value: "1.2.3.4"},
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
		wantReplace: &aftpb.Afts_Ipv4EntryKey{
			Prefix: "22.32.42.52/32",
			Ipv4Entry: &aftpb.Afts_Ipv4Entry{
				EntryMetadata: &wpb.BytesValue{Value: []byte{0, 1, 2, 3, 4, 5, 6, 7}},
			},
		},
		wantAFTs: &AFTs{
			IPv4Entry: map[string]*aftpb.Afts_Ipv4EntryKey{
				"22.32.42.52/32": {
					Prefix:    "22.32.42.52/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
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
		wantAFTs: &AFTs{
			NextHopGroup: map[uint64]*aftpb.Afts_NextHopGroupKey{
				1: {
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
		wantAFTs: &AFTs{
			NextHop: map[uint64]*aftpb.Afts_NextHopKey{
				1: {
					Index:   1,
					NextHop: &aftpb.Afts_NextHop{},
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
				IpAddress:  &wpb.StringValue{Value: "8.8.4.4"},
				MacAddress: &wpb.StringValue{Value: "aa:bb:cc:dd:ee:ff"},
				VniLabel:   &wpb.UintValue{Value: 100},
			},
		},
		wantInstalled: true,
		wantAFTs: &AFTs{
			NextHop: map[uint64]*aftpb.Afts_NextHopKey{
				1: {
					Index: 1,
					NextHop: &aftpb.Afts_NextHop{
						IpAddress:  &wpb.StringValue{Value: "8.8.4.4"},
						MacAddress: &wpb.StringValue{Value: "aa:bb:cc:dd:ee:ff"},
						VniLabel:   &wpb.UintValue{Value: 100},
					},
				},
			},
		},
	}, {
		desc:        "nh invalid MAC address - error",
		inRIBHolder: NewRIBHolder("DEFAULT"),
		inType:      nh,
		inEntry: &aftpb.Afts_NextHopKey{
			Index: 1,
			NextHop: &aftpb.Afts_NextHop{
				MacAddress: &wpb.StringValue{Value: "not-a-mac"},
			},
		},
		wantErr: true,
	}, {
		desc:        "nh invalid IP address - error",
		inRIBHolder: NewRIBHolder("DEFAULT"),
		inType:      nh,
		inEntry: &aftpb.Afts_NextHopKey{
			Index: 1,
			NextHop: &aftpb.Afts_NextHop{
				IpAddress: &wpb.StringValue{Value: "not-an-ip"},
			},
		},
		wantErr: true,
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
		wantAFTs: &AFTs{
			LabelEntry: map[MPLSLabelKey]*aftpb.Afts_LabelEntryKey{
				{Label: 42}: {
					Label: &aftpb.Afts_LabelEntryKey_LabelUint64{
						LabelUint64: 42,
					},
					LabelEntry: &aftpb.Afts_LabelEntry{},
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
		desc: "MPLS - implicit replace",
		inRIBHolder: func() *RIBHolder {
			r := NewRIBHolder("DEFAULT")
			r.r.LabelEntry[MPLSLabelKey{Label: 42}] = &aftpb.Afts_LabelEntryKey{
				Label: &aftpb.Afts_LabelEntryKey_LabelUint64{
					LabelUint64: 42,
				},
				LabelEntry: &aftpb.Afts_LabelEntry{},
			}
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
		wantReplace: &aftpb.Afts_LabelEntryKey{
			Label: &aftpb.Afts_LabelEntryKey_LabelUint64{
				LabelUint64: 42,
			},
			LabelEntry: &aftpb.Afts_LabelEntry{},
		},
		wantAFTs: &AFTs{
			LabelEntry: map[MPLSLabelKey]*aftpb.Afts_LabelEntryKey{
				{Label: 42}: {
					Label: &aftpb.Afts_LabelEntryKey_LabelUint64{
						LabelUint64: 42,
					},
					LabelEntry: &aftpb.Afts_LabelEntry{},
				},
			},
		},
	}, {
		desc: "MPLS - explicit replace",
		inRIBHolder: func() *RIBHolder {
			r := NewRIBHolder("DEFAULT")
			r.r.LabelEntry[MPLSLabelKey{Label: 42}] = &aftpb.Afts_LabelEntryKey{
				Label: &aftpb.Afts_LabelEntryKey_LabelUint64{
					LabelUint64: 42,
				},
				LabelEntry: &aftpb.Afts_LabelEntry{},
			}
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
		wantReplace: &aftpb.Afts_LabelEntryKey{
			Label: &aftpb.Afts_LabelEntryKey_LabelUint64{
				LabelUint64: 42,
			},
			LabelEntry: &aftpb.Afts_LabelEntry{},
		},
		wantAFTs: &AFTs{
			LabelEntry: map[MPLSLabelKey]*aftpb.Afts_LabelEntryKey{
				{Label: 42}: {
					Label: &aftpb.Afts_LabelEntryKey_LabelUint64{
						LabelUint64: 42,
					},
					LabelEntry: &aftpb.Afts_LabelEntry{},
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
	}, {
		desc:        "IPv6 - valid entry",
		inRIBHolder: NewRIBHolder("DEFAULT"),
		inType:      ipv6,
		inEntry: &aftpb.Afts_Ipv6EntryKey{
			Prefix:    "2001:db8::/32",
			Ipv6Entry: &aftpb.Afts_Ipv6Entry{},
		},
		wantInstalled: true,
		wantAFTs: &AFTs{
			IPv6Entry: map[string]*aftpb.Afts_Ipv6EntryKey{
				"2001:db8::/32": {
					Prefix:    "2001:db8::/32",
					Ipv6Entry: &aftpb.Afts_Ipv6Entry{},
				},
			},
		},
	}, {
		desc:        "IPv6 - invalid entry",
		inRIBHolder: NewRIBHolder("DEFAULT"),
		inType:      ipv6,
		inEntry: &aftpb.Afts_Ipv6EntryKey{
			Prefix:    "",
			Ipv6Entry: &aftpb.Afts_Ipv6Entry{},
		},
		wantErr: true,
	}, {
		desc: "IPv6 - implicit replace",
		inRIBHolder: func() *RIBHolder {
			r := NewRIBHolder("DEFAULT")
			r.r.IPv6Entry["2001:db8::/32"] = &aftpb.Afts_Ipv6EntryKey{
				Prefix: "2001:db8::/32",
				Ipv6Entry: &aftpb.Afts_Ipv6Entry{
					NextHopGroup: &wpb.UintValue{Value: 42},
				},
			}
			return r
		}(),
		inType: ipv6,
		inEntry: &aftpb.Afts_Ipv6EntryKey{
			Prefix:    "2001:db8::/32",
			Ipv6Entry: &aftpb.Afts_Ipv6Entry{},
		},
		wantInstalled: true,
		wantReplace: &aftpb.Afts_Ipv6EntryKey{
			Prefix: "2001:db8::/32",
			Ipv6Entry: &aftpb.Afts_Ipv6Entry{
				NextHopGroup: &wpb.UintValue{Value: 42},
			},
		},
		wantAFTs: &AFTs{
			IPv6Entry: map[string]*aftpb.Afts_Ipv6EntryKey{
				"2001:db8::/32": {
					Prefix:    "2001:db8::/32",
					Ipv6Entry: &aftpb.Afts_Ipv6Entry{},
				},
			},
		},
	}, {
		desc: "IPv6 - explicit replace",
		inRIBHolder: func() *RIBHolder {
			r := NewRIBHolder("DEFAULT")
			r.r.IPv6Entry["2001:db8::/32"] = &aftpb.Afts_Ipv6EntryKey{
				Prefix: "2001:db8::/32",
				Ipv6Entry: &aftpb.Afts_Ipv6Entry{
					NextHopGroup: &wpb.UintValue{Value: 42},
				},
			}
			return r
		}(),
		inType:            ipv6,
		inExplicitReplace: true,
		inEntry: &aftpb.Afts_Ipv6EntryKey{
			Prefix:    "2001:db8::/32",
			Ipv6Entry: &aftpb.Afts_Ipv6Entry{},
		},
		wantInstalled: true,
		wantReplace: &aftpb.Afts_Ipv6EntryKey{
			Prefix: "2001:db8::/32",
			Ipv6Entry: &aftpb.Afts_Ipv6Entry{
				NextHopGroup: &wpb.UintValue{Value: 42},
			},
		},
		wantAFTs: &AFTs{
			IPv6Entry: map[string]*aftpb.Afts_Ipv6EntryKey{
				"2001:db8::/32": {
					Prefix:    "2001:db8::/32",
					Ipv6Entry: &aftpb.Afts_Ipv6Entry{},
				},
			},
		},
	}, {
		desc:              "IPv6 - explicit replace - does not exist",
		inRIBHolder:       NewRIBHolder("DEFAULT"),
		inType:            ipv6,
		inExplicitReplace: true,
		inEntry: &aftpb.Afts_Ipv6EntryKey{
			Prefix:    "2001:db8::/32",
			Ipv6Entry: &aftpb.Afts_Ipv6Entry{},
		},
		wantErr: true,
	}, {
		desc: "nh with label stack",
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
				PushedMplsLabelStack: []*aftpb.Afts_NextHop_PushedMplsLabelStackUnion{{
					PushedMplsLabelStackUint64: 42,
				}, {
					PushedMplsLabelStackUint64: 84,
				}},
			},
		},
		wantInstalled: true,
		wantReplace: &aftpb.Afts_NextHopKey{
			Index:   1,
			NextHop: &aftpb.Afts_NextHop{},
		},
		wantAFTs: &AFTs{
			NextHop: map[uint64]*aftpb.Afts_NextHopKey{
				1: {
					Index: 1,
					NextHop: &aftpb.Afts_NextHop{
						PushedMplsLabelStack: []*aftpb.Afts_NextHop_PushedMplsLabelStackUnion{{
							PushedMplsLabelStackUint64: 42,
						}, {
							PushedMplsLabelStackUint64: 84,
						}},
					},
				},
			},
		},
	}, {
		desc:        "nh with interface reference",
		inRIBHolder: NewRIBHolder("DEFAULT"),
		inType:      nh,
		inEntry: &aftpb.Afts_NextHopKey{
			Index: 1,
			NextHop: &aftpb.Afts_NextHop{
				InterfaceRef: &aftpb.Afts_NextHop_InterfaceRef{
					Interface:    &wpb.StringValue{Value: "eth0"},
					Subinterface: &wpb.UintValue{Value: 0},
				},
			},
		},
		wantInstalled: true,
		wantAFTs: &AFTs{
			NextHop: map[uint64]*aftpb.Afts_NextHopKey{
				1: {
					Index: 1,
					NextHop: &aftpb.Afts_NextHop{
						InterfaceRef: &aftpb.Afts_NextHop_InterfaceRef{
							Interface:    &wpb.StringValue{Value: "eth0"},
							Subinterface: &wpb.UintValue{Value: 0},
						},
					},
				},
			},
		},
	}, {
		desc:        "nh with encap headers",
		inRIBHolder: NewRIBHolder("DEFAULT"),
		inType:      nh,
		inEntry: &aftpb.Afts_NextHopKey{
			Index: 1,
			NextHop: &aftpb.Afts_NextHop{
				EncapHeader: []*aftpb.Afts_NextHop_EncapHeaderKey{{
					Index: 11,
					EncapHeader: &aftpb.Afts_NextHop_EncapHeader{
						Mpls: &aftpb.Afts_NextHop_EncapHeader_Mpls{
							MplsLabelStack: []*aftpb.Afts_NextHop_EncapHeader_Mpls_MplsLabelStackUnion{{
								MplsLabelStackUint64: 42,
							}},
							TrafficClass: &wpb.UintValue{Value: 1},
						},
					},
				}},
			},
		},
		wantInstalled: true,
		wantAFTs: &AFTs{
			NextHop: map[uint64]*aftpb.Afts_NextHopKey{
				1: {
					Index: 1,
					NextHop: &aftpb.Afts_NextHop{
						EncapHeader: []*aftpb.Afts_NextHop_EncapHeaderKey{{
							Index: 11,
							EncapHeader: &aftpb.Afts_NextHop_EncapHeader{
								Mpls: &aftpb.Afts_NextHop_EncapHeader_Mpls{
									MplsLabelStack: []*aftpb.Afts_NextHop_EncapHeader_Mpls_MplsLabelStackUnion{{
										MplsLabelStackUint64: 42,
									}},
									TrafficClass: &wpb.UintValue{Value: 1},
								},
							},
						}},
					},
				},
			},
		},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			r := tt.inRIBHolder

			var (
				gotInstalled bool
				gotReplaced  proto.Message
				err          error
			)

			switch tt.inType {
			case ipv4:
				if tt.inEntry == nil {
					_, _, err := r.AddIPv4(nil, false)
					if (err != nil) != tt.wantErr {
						t.Fatalf("AddIPv4(nil): did not get expected error, got: %v, wantErr? %v", err, tt.wantErr)
					}
					return
				}
				gotReplaced, gotInstalled, err = r.AddIPv4(tt.inEntry.(*aftpb.Afts_Ipv4EntryKey), tt.inExplicitReplace)
			case nhg:
				gotReplaced, gotInstalled, err = r.AddNextHopGroup(tt.inEntry.(*aftpb.Afts_NextHopGroupKey), tt.inExplicitReplace)
			case nh:
				gotReplaced, gotInstalled, err = r.AddNextHop(tt.inEntry.(*aftpb.Afts_NextHopKey), tt.inExplicitReplace)
			case mpls:
				gotReplaced, gotInstalled, err = r.AddMPLS(tt.inEntry.(*aftpb.Afts_LabelEntryKey), tt.inExplicitReplace)
			case ipv6:
				gotReplaced, gotInstalled, err = r.AddIPv6(tt.inEntry.(*aftpb.Afts_Ipv6EntryKey), tt.inExplicitReplace)
			default:
				t.Fatalf("unsupported test type in Add, %v", tt.inType)
			}

			if (err != nil) != tt.wantErr {
				t.Fatalf("Add(...): did not get expected error, got: %v, wantErr? %v", err, tt.wantErr)
			}
			if gotInstalled != tt.wantInstalled {
				t.Fatalf("Add(...): did not get expected installed bool, got: %v, want: %v", gotInstalled, tt.wantInstalled)
			}
			if tt.wantReplace != nil {
				if diff := cmp.Diff(gotReplaced, tt.wantReplace, protocmp.Transform(), cmpopts.EquateEmpty()); diff != "" {
					t.Fatalf("Add(...): did not get expected replaced value, diff(-got,+want):\n%s", diff)
				}
			}
			if tt.wantAFTs != nil {
				if diff := cmp.Diff(r.r, tt.wantAFTs, protocmp.Transform(), cmpopts.EquateEmpty()); diff != "" {
					t.Fatalf("Add(...): did not get expected AFTs, diff(-got,+want):\n%s", diff)
				}
			}
		})
	}
}

// TestIndividualDeleteEntryFunctions verifies that DeleteIPv4, DeleteIPv6, DeleteMPLS,
// DeleteNextHopGroup, and DeleteNextHop remove entries and return the original entry when present.
func TestIndividualDeleteEntryFunctions(t *testing.T) {
	tests := []struct {
		// desc is the human-readable test case description.
		desc string
		// inRIB is the target RIBHolder under test.
		inRIB *RIBHolder
		// inEntry is the protobuf key message to delete.
		inEntry proto.Message
		// wantOK indicates whether the delete operation is expected to succeed.
		wantOK bool
		// wantOrig is the expected removed entry protobuf message.
		wantOrig proto.Message
		// wantErr indicates whether an error is expected.
		wantErr bool
		// wantPostAFTs is the expected AFTs state after deletion.
		wantPostAFTs *AFTs
	}{{
		desc:    "nil input",
		inRIB:   &RIBHolder{},
		inEntry: (*aftpb.Afts_Ipv4EntryKey)(nil),
		wantErr: true,
	}, {
		desc: "delete ipv4 entry",
		inRIB: &RIBHolder{
			r: &AFTs{
				IPv4Entry: map[string]*aftpb.Afts_Ipv4EntryKey{
					"1.1.1.1/32": {
						Prefix:    "1.1.1.1/32",
						Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
					},
				},
			},
		},
		inEntry: &aftpb.Afts_Ipv4EntryKey{
			Prefix: "1.1.1.1/32",
		},
		wantOK: true,
		wantOrig: &aftpb.Afts_Ipv4EntryKey{
			Prefix:    "1.1.1.1/32",
			Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
		},
		wantPostAFTs: &AFTs{},
	}, {
		desc:  "delete ipv4 entry, no such entry",
		inRIB: NewRIBHolder("foo"),
		inEntry: &aftpb.Afts_Ipv4EntryKey{
			Prefix: "1.1.1.1/32",
		},
		wantOrig: (*aftpb.Afts_Ipv4EntryKey)(nil),
		wantOK:   true,
	}, {
		desc: "delete ipv6 entry",
		inRIB: &RIBHolder{
			r: &AFTs{
				IPv6Entry: map[string]*aftpb.Afts_Ipv6EntryKey{
					"2001:db8::/32": {
						Prefix:    "2001:db8::/32",
						Ipv6Entry: &aftpb.Afts_Ipv6Entry{},
					},
				},
			},
		},
		inEntry: &aftpb.Afts_Ipv6EntryKey{
			Prefix: "2001:db8::/32",
		},
		wantOK: true,
		wantOrig: &aftpb.Afts_Ipv6EntryKey{
			Prefix:    "2001:db8::/32",
			Ipv6Entry: &aftpb.Afts_Ipv6Entry{},
		},
		wantPostAFTs: &AFTs{},
	}, {
		desc:  "delete ipv6 entry, no such entry",
		inRIB: NewRIBHolder("foo"),
		inEntry: &aftpb.Afts_Ipv6EntryKey{
			Prefix: "2001:db8::/32",
		},
		wantOrig: (*aftpb.Afts_Ipv6EntryKey)(nil),
		wantOK:   true,
	}, {
		desc: "delete mpls entry",
		inRIB: &RIBHolder{
			r: &AFTs{
				LabelEntry: map[MPLSLabelKey]*aftpb.Afts_LabelEntryKey{
					{Label: 42}: {
						Label: &aftpb.Afts_LabelEntryKey_LabelUint64{
							LabelUint64: 42,
						},
						LabelEntry: &aftpb.Afts_LabelEntry{},
					},
				},
			},
		},
		inEntry: &aftpb.Afts_LabelEntryKey{
			Label: &aftpb.Afts_LabelEntryKey_LabelUint64{
				LabelUint64: 42,
			},
			LabelEntry: &aftpb.Afts_LabelEntry{},
		},
		wantOK: true,
		wantOrig: &aftpb.Afts_LabelEntryKey{
			Label: &aftpb.Afts_LabelEntryKey_LabelUint64{
				LabelUint64: 42,
			},
			LabelEntry: &aftpb.Afts_LabelEntry{},
		},
		wantPostAFTs: &AFTs{},
	}, {
		desc:  "delete mpls entry, no such entry",
		inRIB: NewRIBHolder("foo"),
		inEntry: &aftpb.Afts_LabelEntryKey{
			Label: &aftpb.Afts_LabelEntryKey_LabelUint64{
				LabelUint64: 42,
			},
			LabelEntry: &aftpb.Afts_LabelEntry{},
		},
		wantOrig: (*aftpb.Afts_LabelEntryKey)(nil),
		wantOK:   true,
	}, {
		desc: "delete nhg",
		inRIB: &RIBHolder{
			r: &AFTs{
				NextHopGroup: map[uint64]*aftpb.Afts_NextHopGroupKey{
					42: {Id: 42, NextHopGroup: &aftpb.Afts_NextHopGroup{}},
					44: {Id: 44, NextHopGroup: &aftpb.Afts_NextHopGroup{}},
				},
			},
		},
		inEntry: &aftpb.Afts_NextHopGroupKey{
			Id:           42,
			NextHopGroup: &aftpb.Afts_NextHopGroup{},
		},
		wantOK: true,
		wantOrig: &aftpb.Afts_NextHopGroupKey{
			Id:           42,
			NextHopGroup: &aftpb.Afts_NextHopGroup{},
		},
		wantPostAFTs: &AFTs{
			NextHopGroup: map[uint64]*aftpb.Afts_NextHopGroupKey{
				44: {Id: 44, NextHopGroup: &aftpb.Afts_NextHopGroup{}},
			},
		},
	}, {
		desc: "delete nh",
		inRIB: &RIBHolder{
			r: &AFTs{
				NextHop: map[uint64]*aftpb.Afts_NextHopKey{
					1: {Index: 1, NextHop: &aftpb.Afts_NextHop{}},
					2: {Index: 2, NextHop: &aftpb.Afts_NextHop{}},
				},
			},
		},
		inEntry: &aftpb.Afts_NextHopKey{
			Index: 2,
		},
		wantOK: true,
		wantOrig: &aftpb.Afts_NextHopKey{
			Index:   2,
			NextHop: &aftpb.Afts_NextHop{},
		},
		wantPostAFTs: &AFTs{
			NextHop: map[uint64]*aftpb.Afts_NextHopKey{
				1: {Index: 1, NextHop: &aftpb.Afts_NextHop{}},
			},
		},
	}, {
		desc:  "delete nhg entry, no such entry",
		inRIB: NewRIBHolder("foo"),
		inEntry: &aftpb.Afts_NextHopGroupKey{
			Id: 1,
		},
		wantOrig: (*aftpb.Afts_NextHopGroupKey)(nil),
		wantOK:   true,
	}, {
		desc:  "delete nh entry, no such entry",
		inRIB: NewRIBHolder("foo"),
		inEntry: &aftpb.Afts_NextHopKey{
			Index: 42,
		},
		wantOrig: (*aftpb.Afts_NextHopKey)(nil),
		wantOK:   true,
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			var (
				ok      bool
				err     error
				gotOrig proto.Message
			)
			switch v := tt.inEntry.(type) {
			case *aftpb.Afts_Ipv4EntryKey:
				gotOrig, ok, err = tt.inRIB.DeleteIPv4(v)
			case *aftpb.Afts_NextHopGroupKey:
				gotOrig, ok, err = tt.inRIB.DeleteNextHopGroup(v)
			case *aftpb.Afts_NextHopKey:
				gotOrig, ok, err = tt.inRIB.DeleteNextHop(v)
			case *aftpb.Afts_Ipv6EntryKey:
				gotOrig, ok, err = tt.inRIB.DeleteIPv6(v)
			case *aftpb.Afts_LabelEntryKey:
				gotOrig, ok, err = tt.inRIB.DeleteMPLS(v)
			default:
				t.Fatalf("unknown input entry type, %T", v)
			}
			if (err != nil) != tt.wantErr {
				t.Fatalf("Delete*(...): did not get expected error, got: %v, wantErr? %v", err, tt.wantErr)
			}
			if ok != tt.wantOK {
				t.Fatalf("Delete*(...): did not get expected ok status, got: %v, want: %v", ok, tt.wantOK)
			}
			if ok {
				if diff := cmp.Diff(gotOrig, tt.wantOrig, protocmp.Transform(), cmpopts.EquateEmpty()); diff != "" {
					t.Fatalf("Delete*(...): did not get expected original entry, diff(-got,+want):\n%s", diff)
				}
			}
			if tt.wantPostAFTs != nil {
				if diff := cmp.Diff(tt.inRIB.r, tt.wantPostAFTs, protocmp.Transform(), cmpopts.EquateEmpty()); diff != "" {
					t.Fatalf("Delete*(...): did not get expected post-delete AFTs, diff(-got,+want):\n%s", diff)
				}
			}
		})
	}
}

// TestHooks verifies that SetPostChangeHook invokes the registered hook on Add and Delete operations.
func TestHooks(t *testing.T) {
	type op struct {
		// Do is the operation type recorded by the hook.
		Do constants.OpType
		// TS is the sequence timestamp recorded by the hook.
		TS int64
		// NH is the NextHop index when a NextHop entry is modified.
		NH uint64
		// NHG is the NextHopGroup ID when a NextHopGroup entry is modified.
		NHG uint64
		// IP4 is the IPv4 prefix when an IPv4 entry is modified.
		IP4 string
		// MPLS is the MPLS label when an MPLS entry is modified.
		MPLS uint32
		// IP6 is the IPv6 prefix when an IPv6 entry is modified.
		IP6 string
	}

	tests := []struct {
		// desc is the human-readable test case description.
		desc string
		// inOps is the sequence of operations to execute.
		inOps []*op
		// want is the expected sequence of recorded hook events.
		want []*op
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
		want: []*op{
			{Do: constants.Add, TS: 0, IP4: "8.8.8.8/32"},
			{Do: constants.Add, TS: 1, NHG: 42},
			{Do: constants.Add, TS: 2, NH: 84},
		},
	}, {
		desc: "store add IPv6 and MPLS hooks",
		inOps: []*op{{
			Do:  constants.Add,
			IP6: "2001:DB8::/32",
		}, {
			Do:  constants.Add,
			NHG: 42,
		}, {
			Do: constants.Add,
			NH: 84,
		}, {
			Do:   constants.Add,
			MPLS: 42,
		}},
		want: []*op{
			{Do: constants.Add, TS: 0, IP6: "2001:DB8::/32"},
			{Do: constants.Add, TS: 1, NHG: 42},
			{Do: constants.Add, TS: 2, NH: 84},
			{Do: constants.Add, TS: 3, MPLS: 42},
		},
	}, {
		desc: "store delete hooks",
		inOps: []*op{{
			Do:  constants.Delete,
			IP4: "8.8.8.8/32",
		}, {
			Do:  constants.Delete,
			IP4: "1.1.1.1/32",
		}, {
			Do:   constants.Delete,
			MPLS: 42,
		}, {
			Do: constants.Delete,
			NH: 10,
		}, {
			Do:  constants.Delete,
			NHG: 20,
		}},
		want: []*op{
			{Do: constants.Delete, TS: 0, IP4: "8.8.8.8/32"},
			{Do: constants.Delete, TS: 1, IP4: "1.1.1.1/32"},
			{Do: constants.Delete, TS: 2, MPLS: 42},
			{Do: constants.Delete, TS: 3, NH: 10},
			{Do: constants.Delete, TS: 4, NHG: 20},
		},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			var got []*op

			tsFn := func() int64 {
				return int64(len(got))
			}
			store := func(o constants.OpType, _ int64, _ string, msg proto.Message) {
				switch et := msg.(type) {
				case *aftpb.Afts_Ipv4EntryKey:
					got = append(got, &op{Do: o, TS: tsFn(), IP4: et.GetPrefix()})
				case *aftpb.Afts_NextHopGroupKey:
					got = append(got, &op{Do: o, TS: tsFn(), NHG: et.GetId()})
				case *aftpb.Afts_NextHopKey:
					got = append(got, &op{Do: o, TS: tsFn(), NH: et.GetIndex()})
				case *aftpb.Afts_Ipv6EntryKey:
					got = append(got, &op{Do: o, TS: tsFn(), IP6: et.GetPrefix()})
				case *aftpb.Afts_LabelEntryKey:
					got = append(got, &op{Do: o, TS: tsFn(), MPLS: uint32(et.GetLabelUint64())})
				}
			}

			r := New("DEFAULT")
			r.niRIB["DEFAULT"].checkFn = nil

			for _, o := range tt.inOps {
				switch {
				case o.IP4 != "":
					if o.Do == constants.Delete {
						if _, _, err := r.niRIB[r.defaultName].AddIPv4(&aftpb.Afts_Ipv4EntryKey{
							Prefix:    o.IP4,
							Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
						}, false); err != nil {
							t.Fatalf("cannot pre-add IPv4 entry %s: %v", o.IP4, err)
						}
					}
				case o.IP6 != "":
					if o.Do == constants.Delete {
						if _, _, err := r.niRIB[r.defaultName].AddIPv6(&aftpb.Afts_Ipv6EntryKey{
							Prefix:    o.IP6,
							Ipv6Entry: &aftpb.Afts_Ipv6Entry{},
						}, false); err != nil {
							t.Fatalf("cannot pre-add IPv6 entry %s: %v", o.IP6, err)
						}
					}
				case o.MPLS != 0:
					if o.Do == constants.Delete {
						if _, _, err := r.niRIB[r.defaultName].AddMPLS(&aftpb.Afts_LabelEntryKey{
							Label: &aftpb.Afts_LabelEntryKey_LabelUint64{
								LabelUint64: uint64(o.MPLS),
							},
							LabelEntry: &aftpb.Afts_LabelEntry{},
						}, false); err != nil {
							t.Fatalf("cannot pre-add MPLS entry %d: %v", o.MPLS, err)
						}
					}
				case o.NHG != 0:
					if o.Do == constants.Delete {
						if _, _, err := r.niRIB[r.defaultName].AddNextHopGroup(&aftpb.Afts_NextHopGroupKey{
							Id:           o.NHG,
							NextHopGroup: &aftpb.Afts_NextHopGroup{},
						}, false); err != nil {
							t.Fatalf("cannot pre-add NHG entry %d: %v", o.NHG, err)
						}
					}
				case o.NH != 0:
					if o.Do == constants.Delete {
						if _, _, err := r.niRIB[r.defaultName].AddNextHop(&aftpb.Afts_NextHopKey{
							Index:   o.NH,
							NextHop: &aftpb.Afts_NextHop{},
						}, false); err != nil {
							t.Fatalf("cannot pre-add NH entry %d: %v", o.NH, err)
						}
					}
				}
			}

			r.SetPostChangeHook(store)

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
							t.Fatalf("cannot delete IPv4 entry %s: %v", o.IP4, err)
						}
					}
				case o.IP6 != "":
					switch o.Do {
					case constants.Add:
						if _, _, err := r.niRIB[r.defaultName].AddIPv6(&aftpb.Afts_Ipv6EntryKey{
							Prefix: o.IP6,
							Ipv6Entry: &aftpb.Afts_Ipv6Entry{
								NextHopGroup: &wpb.UintValue{Value: 1},
							},
						}, false); err != nil {
							t.Fatalf("cannot add IPv6 entry %s: %v", o.IP6, err)
						}
					case constants.Delete:
						if _, _, err := r.niRIB[r.defaultName].DeleteIPv6(&aftpb.Afts_Ipv6EntryKey{
							Prefix: o.IP6,
						}); err != nil {
							t.Fatalf("cannot delete IPv6 entry %s: %v", o.IP6, err)
						}
					}
				case o.MPLS != 0:
					switch o.Do {
					case constants.Add:
						if _, _, err := r.niRIB[r.defaultName].AddMPLS(&aftpb.Afts_LabelEntryKey{
							Label: &aftpb.Afts_LabelEntryKey_LabelUint64{
								LabelUint64: uint64(o.MPLS),
							},
							LabelEntry: &aftpb.Afts_LabelEntry{
								NextHopGroup: &wpb.UintValue{Value: 1},
							},
						}, false); err != nil {
							t.Fatalf("cannot add MPLS entry %d: %v", o.MPLS, err)
						}
					case constants.Delete:
						if _, _, err := r.niRIB[r.defaultName].DeleteMPLS(&aftpb.Afts_LabelEntryKey{
							Label: &aftpb.Afts_LabelEntryKey_LabelUint64{
								LabelUint64: uint64(o.MPLS),
							},
						}); err != nil {
							t.Fatalf("cannot delete MPLS entry %d: %v", o.MPLS, err)
						}
					}
				case o.NHG != 0:
					switch o.Do {
					case constants.Add:
						if _, _, err := r.niRIB[r.defaultName].AddNextHopGroup(&aftpb.Afts_NextHopGroupKey{
							Id:           o.NHG,
							NextHopGroup: &aftpb.Afts_NextHopGroup{},
						}, false); err != nil {
							t.Fatalf("cannot add NHG entry %d: %v", o.NHG, err)
						}
					case constants.Delete:
						if _, _, err := r.niRIB[r.defaultName].DeleteNextHopGroup(&aftpb.Afts_NextHopGroupKey{
							Id: o.NHG,
						}); err != nil {
							t.Fatalf("cannot delete NHG entry %d: %v", o.NHG, err)
						}
					}
				case o.NH != 0:
					switch o.Do {
					case constants.Add:
						if _, _, err := r.niRIB[r.defaultName].AddNextHop(&aftpb.Afts_NextHopKey{
							Index:   o.NH,
							NextHop: &aftpb.Afts_NextHop{},
						}, false); err != nil {
							t.Fatalf("cannot add NH entry %d: %v", o.NH, err)
						}
					case constants.Delete:
						if _, _, err := r.niRIB[r.defaultName].DeleteNextHop(&aftpb.Afts_NextHopKey{
							Index: o.NH,
						}); err != nil {
							t.Fatalf("cannot delete NH entry %d: %v", o.NH, err)
						}
					}
				}
			}

			if diff := cmp.Diff(got, tt.want, cmpopts.IgnoreUnexported(op{})); diff != "" {
				t.Fatalf("TestHooks: did not get expected queue, diff(-got,+want):\n%s", diff)
			}
		})
	}
}

// TestCanResolve verifies that canResolve checks candidate entry dependencies against the RIB.
func TestCanResolve(t *testing.T) {
	tests := []struct {
		// desc is the human-readable test case description.
		desc string
		// inRIB is the RIB state against which resolution is evaluated.
		inRIB *RIB
		// inNI is the target network instance name.
		inNI string
		// inCand is the candidate AFTs containing the entry to resolve.
		inCand *AFTs
		// want is the expected resolvability result.
		want bool
		// wantErrSubstring is the expected error substring, if any.
		wantErrSubstring string
	}{{
		desc:             "nil input",
		inRIB:            New(defName),
		want:             false,
		wantErrSubstring: "invalid nil candidate",
	}, {
		desc:             "empty candidate - invalid",
		inRIB:            New(defName),
		inNI:             defName,
		inCand:           &AFTs{},
		wantErrSubstring: "no entries in specified candidate",
	}, {
		desc:  "multiple entries in candidate - invalid",
		inRIB: New(defName),
		inNI:  defName,
		inCand: &AFTs{
			IPv4Entry: map[string]*aftpb.Afts_Ipv4EntryKey{
				"1.1.1.1/32": {Prefix: "1.1.1.1/32", Ipv4Entry: &aftpb.Afts_Ipv4Entry{}},
			},
			NextHop: map[uint64]*aftpb.Afts_NextHopKey{
				1: {Index: 1, NextHop: &aftpb.Afts_NextHop{}},
			},
		},
		wantErrSubstring: "multiple entries are unsupported",
	}, {
		desc:  "next-hop can be successfully resolved",
		inRIB: New(defName),
		inNI:  defName,
		inCand: &AFTs{
			NextHop: map[uint64]*aftpb.Afts_NextHopKey{
				1: {
					Index: 1,
					NextHop: &aftpb.Afts_NextHop{
						IpAddress: &wpb.StringValue{Value: "1.1.1.1"},
					},
				},
			},
		},
		want: true,
	}, {
		desc:  "next-hop with index 0 - invalid",
		inRIB: New(defName),
		inNI:  defName,
		inCand: &AFTs{
			NextHop: map[uint64]*aftpb.Afts_NextHopKey{
				0: {
					Index: 0,
					NextHop: &aftpb.Afts_NextHop{
						IpAddress: &wpb.StringValue{Value: "1.1.1.1"},
					},
				},
			},
		},
		wantErrSubstring: "invalid index zero for next-hop",
	}, {
		desc:  "resolve in invalid network-instance",
		inRIB: New(defName),
		inNI:  "does-not-exist",
		inCand: &AFTs{
			NextHopGroup: map[uint64]*aftpb.Afts_NextHopGroupKey{
				42: {Id: 42, NextHopGroup: &aftpb.Afts_NextHopGroup{}},
			},
		},
		wantErrSubstring: "invalid network-instance does-not-exist",
	}, {
		desc:  "nhg with index 0 - invalid",
		inRIB: New(defName),
		inNI:  defName,
		inCand: &AFTs{
			NextHopGroup: map[uint64]*aftpb.Afts_NextHopGroupKey{
				0: {Id: 0, NextHopGroup: &aftpb.Afts_NextHopGroup{}},
			},
		},
		wantErrSubstring: "invalid zero-index NHG",
	}, {
		desc:  "nhg with nh with index 0 - invalid",
		inRIB: New(defName),
		inNI:  defName,
		inCand: &AFTs{
			NextHopGroup: map[uint64]*aftpb.Afts_NextHopGroupKey{
				1: {
					Id: 1,
					NextHopGroup: &aftpb.Afts_NextHopGroup{
						NextHop: []*aftpb.Afts_NextHopGroup_NextHopKey{{
							Index:   0,
							NextHop: &aftpb.Afts_NextHopGroup_NextHop{},
						}},
					},
				},
			},
		},
		wantErrSubstring: "invalid zero index NH in NHG 1",
	}, {
		desc:  "nhg refers to missing nh",
		inRIB: New(defName),
		inNI:  defName,
		inCand: &AFTs{
			NextHopGroup: map[uint64]*aftpb.Afts_NextHopGroupKey{
				1: {
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
		want: false,
	}, {
		desc: "nhg refers to valid nhs",
		inRIB: func() *RIB {
			r := New(defName)
			r.niRIB[defName].r.NextHop[1] = &aftpb.Afts_NextHopKey{Index: 1, NextHop: &aftpb.Afts_NextHop{}}
			r.niRIB[defName].r.NextHop[2] = &aftpb.Afts_NextHopKey{Index: 2, NextHop: &aftpb.Afts_NextHop{}}
			return r
		}(),
		inCand: &AFTs{
			NextHopGroup: map[uint64]*aftpb.Afts_NextHopGroupKey{
				1: {
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
		},
		want: true,
	}, {
		desc:  "zero index nhg in ipv4 entry - invalid",
		inRIB: New(defName),
		inCand: &AFTs{
			IPv4Entry: map[string]*aftpb.Afts_Ipv4EntryKey{
				"1.1.1.1/32": {
					Prefix: "1.1.1.1/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{
						NextHopGroup: &wpb.UintValue{Value: 0},
					},
				},
			},
		},
		wantErrSubstring: "invalid zero-index NHG in IPv4Entry",
	}, {
		desc:  "resolve ipv4 entry in nil NI - invalid",
		inRIB: New(defName),
		inCand: &AFTs{
			IPv4Entry: map[string]*aftpb.Afts_Ipv4EntryKey{
				"1.1.1.1/32": {
					Prefix: "1.1.1.1/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{
						NextHopGroup:                &wpb.UintValue{Value: 1},
						NextHopGroupNetworkInstance: &wpb.StringValue{Value: "404"},
					},
				},
			},
		},
		wantErrSubstring: "invalid unknown network-instance for entry",
	}, {
		desc: "resolve ipv4 entry in default NI - implicit name",
		inRIB: func() *RIB {
			r := New(defName)
			r.niRIB[defName].r.NextHopGroup[1] = &aftpb.Afts_NextHopGroupKey{Id: 1, NextHopGroup: &aftpb.Afts_NextHopGroup{}}
			return r
		}(),
		inCand: &AFTs{
			IPv4Entry: map[string]*aftpb.Afts_Ipv4EntryKey{
				"1.1.1.1/32": {
					Prefix: "1.1.1.1/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{
						NextHopGroup: &wpb.UintValue{Value: 1},
					},
				},
			},
		},
		want: true,
	}, {
		desc: "resolve ipv4 entry in non-default NI",
		inRIB: func() *RIB {
			r := New(defName)
			r.niRIB["vrf-42"] = NewRIBHolder("vrf-42")
			r.niRIB["vrf-42"].r.NextHopGroup[1] = &aftpb.Afts_NextHopGroupKey{Id: 1, NextHopGroup: &aftpb.Afts_NextHopGroup{}}
			return r
		}(),
		inCand: &AFTs{
			IPv4Entry: map[string]*aftpb.Afts_Ipv4EntryKey{
				"1.1.1.1/32": {
					Prefix: "1.1.1.1/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{
						NextHopGroup:                &wpb.UintValue{Value: 1},
						NextHopGroupNetworkInstance: &wpb.StringValue{Value: "vrf-42"},
					},
				},
			},
		},
		want: true,
	}, {
		desc: "MPLS - found in default NI",
		inNI: defName,
		inRIB: func() *RIB {
			r := New(defName)
			r.niRIB[defName].r.NextHopGroup[1] = &aftpb.Afts_NextHopGroupKey{Id: 1, NextHopGroup: &aftpb.Afts_NextHopGroup{}}
			return r
		}(),
		inCand: &AFTs{
			LabelEntry: map[MPLSLabelKey]*aftpb.Afts_LabelEntryKey{
				{Label: 42}: {
					Label: &aftpb.Afts_LabelEntryKey_LabelUint64{LabelUint64: 42},
					LabelEntry: &aftpb.Afts_LabelEntry{
						NextHopGroup:                &wpb.UintValue{Value: 1},
						NextHopGroupNetworkInstance: &wpb.StringValue{Value: defName},
					},
				},
			},
		},
		want: true,
	}, {
		desc:  "zero index nhg in IPv6 entry - invalid",
		inRIB: New(defName),
		inCand: &AFTs{
			IPv6Entry: map[string]*aftpb.Afts_Ipv6EntryKey{
				"2001:db8::1/128": {
					Prefix: "2001:db8::1/128",
					Ipv6Entry: &aftpb.Afts_Ipv6Entry{
						NextHopGroup: &wpb.UintValue{Value: 0},
					},
				},
			},
		},
		wantErrSubstring: "invalid zero-index NHG in IPv6Entry",
	}, {
		desc: "resolve ipv6 entry in default NI",
		inRIB: func() *RIB {
			r := New(defName)
			r.niRIB[defName].r.NextHopGroup[1] = &aftpb.Afts_NextHopGroupKey{Id: 1, NextHopGroup: &aftpb.Afts_NextHopGroup{}}
			return r
		}(),
		inCand: &AFTs{
			IPv6Entry: map[string]*aftpb.Afts_Ipv6EntryKey{
				"2001:db8::1/128": {
					Prefix: "2001:db8::1/128",
					Ipv6Entry: &aftpb.Afts_Ipv6Entry{
						NextHopGroup: &wpb.UintValue{Value: 1},
					},
				},
			},
		},
		want: true,
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got, err := tt.inRIB.canResolve(tt.inNI, tt.inCand)
			if err != nil {
				if diff := errdiff.Substring(err, tt.wantErrSubstring); diff != "" {
					t.Fatalf("canResolve(...): did not get expected error, %s", diff)
				}
				return
			}
			if got != tt.want {
				t.Fatalf("canResolve(...): did not get expected result, got: %v, want: %v", got, tt.want)
			}
		})
	}
}

// sortResultSlice sorts a slice of OpResult pointers in ascending order of operation ID.
func sortResultSlice(s []*OpResult) {
	sort.Slice(s, func(i, j int) bool { return s[i].ID < s[j].ID })
}

// TestRIBAddEntry verifies that AddEntry installs entries, queues unresolved forward references,
// and resolves pending entries when their dependencies arrive.
func TestRIBAddEntry(t *testing.T) {
	tests := []struct {
		// desc is the human-readable test case description.
		desc string
		// inRIB is the RIB instance under test.
		inRIB *RIB
		// inNI is the target network instance name.
		inNI string
		// inOp is the AFTOperation to execute.
		inOp *spb.AFTOperation
		// wantOKs is the expected slice of successful operation results.
		wantOKs []*OpResult
		// wantFails is the expected slice of failed operation results.
		wantFails []*OpResult
		// wantErr indicates whether a top-level error is expected.
		wantErr bool
		// wantReferenceCheck validates post-operation reference counts when non-nil.
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
			Error: "invalid NextHop, could not parse a field within the list gribi_aft.Afts.next_hop , nil list member in field gribi_aft.Afts.NextHopKey.next_hop, <nil>",
		}},
	}, {
		desc: "nh makes nhg makes ipv4 resolvable",
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
			if got, want := ni.refCounts.NextHop[1], uint64(1); got != want {
				return fmt.Errorf("got invalid reference count for next-hop 1, got: %d, want: %d", got, want)
			}
			if got, want := ni.refCounts.NextHopGroup[1], uint64(1); got != want {
				return fmt.Errorf("got invalid reference count for next-hop-group 1, got: %d, want: %d", got, want)
			}
			return nil
		},
	}, {
		desc: "check function that always returns false",
		inRIB: func() *RIB {
			r := New(defName)
			r.niRIB[defName].checkFn = func(_ constants.OpType, _ *AFTs) (bool, error) {
				return false, nil
			}
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
	}, {
		desc: "check function that always returns an error",
		inRIB: func() *RIB {
			r := New(defName)
			r.niRIB[defName].checkFn = func(_ constants.OpType, _ *AFTs) (bool, error) {
				return false, errors.New("cannot install")
			}
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
		desc:  "unresolved NHG - with no forward references",
		inRIB: New(defName, DisableForwardReferences()),
		inNI:  defName,
		inOp: &spb.AFTOperation{
			Id: 2,
			Entry: &spb.AFTOperation_NextHopGroup{
				NextHopGroup: &aftpb.Afts_NextHopGroupKey{
					Id: 1,
					NextHopGroup: &aftpb.Afts_NextHopGroup{
						NextHop: []*aftpb.Afts_NextHopGroup_NextHopKey{{
							Index: 1,
							NextHop: &aftpb.Afts_NextHopGroup_NextHop{
								Weight: &wpb.UintValue{Value: 42},
							},
						}},
					},
				},
			},
		},
		wantFails: []*OpResult{{
			ID: 2,
			Op: &spb.AFTOperation{
				Id: 2,
				Entry: &spb.AFTOperation_NextHopGroup{
					NextHopGroup: &aftpb.Afts_NextHopGroupKey{
						Id: 1,
						NextHopGroup: &aftpb.Afts_NextHopGroup{
							NextHop: []*aftpb.Afts_NextHopGroup_NextHopKey{{
								Index: 1,
								NextHop: &aftpb.Afts_NextHopGroup_NextHop{
									Weight: &wpb.UintValue{Value: 42},
								},
							}},
						},
					},
				},
			},
			Error: "operation 2 has unresolved dependencies",
		}},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			gotOKs, gotFails, err := tt.inRIB.AddEntry(tt.inNI, tt.inOp)
			if (err != nil) != tt.wantErr {
				t.Fatalf("AddEntry(...): did not get expected error, got: %v, wantErr? %v", err, tt.wantErr)
			}
			sortResultSlice(gotOKs)
			sortResultSlice(gotFails)
			sortResultSlice(tt.wantOKs)
			sortResultSlice(tt.wantFails)

			if diff := cmp.Diff(gotOKs, tt.wantOKs, protocmp.Transform(), cmpopts.EquateEmpty()); diff != "" {
				t.Fatalf("AddEntry(...): did not get expected OK IDs, diff(-got,+want):\n%s", diff)
			}
			if diff := cmp.Diff(gotFails, tt.wantFails, protocmp.Transform(), cmpopts.EquateEmpty()); diff != "" {
				t.Fatalf("AddEntry(...): did not get expected failed IDs, diff(-got,+want):\n%s", diff)
			}

			if tt.wantReferenceCheck != nil {
				if err := tt.wantReferenceCheck(tt.inRIB); err != nil {
					t.Fatalf("AddEntry(...): did not get expected references, %v", err)
				}
			}
		})
	}
}

// mustChainRIB returns a RIB populated with an IPv4 -> NHG -> NH chain and panics on error.
func mustChainRIB() *RIB {
	r := New(defName)
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
			panic(fmt.Sprintf("cannot build testcase, cannot add nh: %v", err))
		}
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
		panic(fmt.Sprintf("cannot build testcase, cannot add nhg: %v", err))
	}
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
		panic(fmt.Sprintf("cannot build testcase, cannot add ipv4: %v", err))
	}
	return r
}

// TestDeleteEntry verifies that DeleteEntry respects reference counts and updates them on removal.
func TestDeleteEntry(t *testing.T) {
	tests := []struct {
		// desc is the human-readable test case description.
		desc string
		// inRIB is the RIB instance under test.
		inRIB *RIB
		// inNetworkInstance is the target network instance name.
		inNetworkInstance string
		// inOp is the delete AFTOperation to execute.
		inOp *spb.AFTOperation
		// wantOKs is the expected slice of successful delete results.
		wantOKs []*OpResult
		// wantFails is the expected slice of failed delete results.
		wantFails []*OpResult
		// wantErr indicates whether a top-level error is expected.
		wantErr bool
	}{{
		desc: "delete unreferenced nexthop succeeds",
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
				t.Fatalf("cannot add next-hop: %v", err)
			}
			return r
		}(),
		inNetworkInstance: defName,
		inOp: &spb.AFTOperation{
			Id: 43,
			Entry: &spb.AFTOperation_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index:   42,
					NextHop: &aftpb.Afts_NextHop{},
				},
			},
		},
		wantOKs: []*OpResult{{
			ID: 43,
			Op: &spb.AFTOperation{
				Id: 43,
				Entry: &spb.AFTOperation_NextHop{
					NextHop: &aftpb.Afts_NextHopKey{
						Index:   42,
						NextHop: &aftpb.Afts_NextHop{},
					},
				},
			},
		}},
	}, {
		desc:              "cannot delete referenced NHG",
		inRIB:             mustChainRIB(),
		inNetworkInstance: defName,
		inOp: &spb.AFTOperation{
			Id: 10,
			Entry: &spb.AFTOperation_NextHopGroup{
				NextHopGroup: &aftpb.Afts_NextHopGroupKey{
					Id:           1,
					NextHopGroup: &aftpb.Afts_NextHopGroup{},
				},
			},
		},
		wantFails: []*OpResult{{
			ID: 10,
			Op: &spb.AFTOperation{
				Id: 10,
				Entry: &spb.AFTOperation_NextHopGroup{
					NextHopGroup: &aftpb.Afts_NextHopGroupKey{
						Id:           1,
						NextHopGroup: &aftpb.Afts_NextHopGroup{},
					},
				},
			},
		}},
	}, {
		desc:              "cannot delete referenced NH",
		inRIB:             mustChainRIB(),
		inNetworkInstance: defName,
		inOp: &spb.AFTOperation{
			Id: 11,
			Entry: &spb.AFTOperation_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index:   1,
					NextHop: &aftpb.Afts_NextHop{},
				},
			},
		},
		wantFails: []*OpResult{{
			ID: 11,
			Op: &spb.AFTOperation{
				Id: 11,
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
			gotOKs, gotFails, err := tt.inRIB.DeleteEntry(tt.inNetworkInstance, tt.inOp)
			if (err != nil) != tt.wantErr {
				t.Fatalf("DeleteEntry(...): did not get expected error, got: %v, wantErr? %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(gotOKs, tt.wantOKs, protocmp.Transform(), cmpopts.EquateEmpty()); diff != "" {
				t.Fatalf("DeleteEntry(...): did not get expected OKs, diff(-got,+want):\n%s", diff)
			}
			if diff := cmp.Diff(gotFails, tt.wantFails, protocmp.Transform(), cmpopts.EquateEmpty()); diff != "" {
				t.Fatalf("DeleteEntry(...): did not get expected fails, diff(-got,+want):\n%s", diff)
			}
		})
	}
}

// TestCanDelete verifies that canDelete enforces reference count constraints on NHGs and NHs.
func TestCanDelete(t *testing.T) {
	tests := []struct {
		// desc is the human-readable test case description.
		desc string
		// inRIB is the RIB state against which deletion is evaluated.
		inRIB *RIB
		// inNetInst is the target network instance name.
		inNetInst string
		// inDelCandidate is the candidate AFTs containing the entry to delete.
		inDelCandidate *AFTs
		// want is the expected deletion eligibility result.
		want bool
		// wantErr indicates whether an error is expected.
		wantErr bool
	}{{
		desc:      "can delete IPv4 - always OK",
		inRIB:     mustChainRIB(),
		inNetInst: defName,
		inDelCandidate: &AFTs{
			IPv4Entry: map[string]*aftpb.Afts_Ipv4EntryKey{
				"1.1.1.1/32": {Prefix: "1.1.1.1/32", Ipv4Entry: &aftpb.Afts_Ipv4Entry{}},
			},
		},
		want: true,
	}, {
		desc:      "cannot delete referenced NHG",
		inRIB:     mustChainRIB(),
		inNetInst: defName,
		inDelCandidate: &AFTs{
			NextHopGroup: map[uint64]*aftpb.Afts_NextHopGroupKey{
				1: {Id: 1, NextHopGroup: &aftpb.Afts_NextHopGroup{}},
			},
		},
		want: false,
	}, {
		desc:      "cannot delete referenced NH",
		inRIB:     mustChainRIB(),
		inNetInst: defName,
		inDelCandidate: &AFTs{
			NextHop: map[uint64]*aftpb.Afts_NextHopKey{
				1: {Index: 1, NextHop: &aftpb.Afts_NextHop{}},
			},
		},
		want: false,
	}, {
		desc:      "nil candidate",
		inRIB:     New(defName),
		inNetInst: defName,
		wantErr:   true,
	}, {
		desc:           "empty candidate",
		inRIB:          New(defName),
		inDelCandidate: &AFTs{},
		inNetInst:      defName,
		wantErr:        true,
	}, {
		desc:  "badly formed NHG",
		inRIB: New(defName),
		inDelCandidate: &AFTs{
			NextHopGroup: map[uint64]*aftpb.Afts_NextHopGroupKey{
				0: {Id: 0, NextHopGroup: &aftpb.Afts_NextHopGroup{}},
			},
		},
		wantErr: true,
	}, {
		desc:  "badly formed NH",
		inRIB: New(defName),
		inDelCandidate: &AFTs{
			NextHop: map[uint64]*aftpb.Afts_NextHopKey{
				0: {Index: 0, NextHop: &aftpb.Afts_NextHop{}},
			},
		},
		wantErr: true,
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got, err := tt.inRIB.canDelete(tt.inNetInst, tt.inDelCandidate)
			if (err != nil) != tt.wantErr {
				t.Fatalf("canDelete(...): did not get expected error, got: %v, wantErr? %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("canDelete(...): did not get expected result, got: %v, want: %v", got, tt.want)
			}
		})
	}
}

// TestGetRIB verifies that GetRIB streams filtered AFT entries from a RIBHolder.
func TestGetRIB(t *testing.T) {
	allPopRIB := func() *RIBHolder {
		r := NewRIBHolder("VRF-42")
		r.doAddNH(1, &aftpb.Afts_NextHopKey{
			Index: 1,
			NextHop: &aftpb.Afts_NextHop{
				IpAddress: &wpb.StringValue{Value: "1.1.1.1"},
			},
		})
		r.doAddNHG(42, &aftpb.Afts_NextHopGroupKey{
			Id: 42,
			NextHopGroup: &aftpb.Afts_NextHopGroup{
				NextHop: []*aftpb.Afts_NextHopGroup_NextHopKey{{
					Index: 1,
					NextHop: &aftpb.Afts_NextHopGroup_NextHop{
						Weight: &wpb.UintValue{Value: 1},
					},
				}},
			},
		})
		r.doAddIPv4("42.42.42.42/32", &aftpb.Afts_Ipv4EntryKey{
			Prefix: "42.42.42.42/32",
			Ipv4Entry: &aftpb.Afts_Ipv4Entry{
				NextHopGroup: &wpb.UintValue{Value: 42},
			},
		})
		r.doAddMPLS(MPLSLabelKey{Label: 42}, &aftpb.Afts_LabelEntryKey{
			Label: &aftpb.Afts_LabelEntryKey_LabelUint64{
				LabelUint64: 42,
			},
			LabelEntry: &aftpb.Afts_LabelEntry{
				NextHopGroup: &wpb.UintValue{Value: 42},
			},
		})
		r.doAddIPv6("2001:4c20::/32", &aftpb.Afts_Ipv6EntryKey{
			Prefix: "2001:4c20::/32",
			Ipv6Entry: &aftpb.Afts_Ipv6Entry{
				NextHopGroup: &wpb.UintValue{Value: 42},
			},
		})
		return r
	}()

	tests := []struct {
		// desc is the human-readable test case description.
		desc string
		// inRIB is the RIBHolder to stream from.
		inRIB *RIBHolder
		// inFilter specifies which AFT types to include.
		inFilter map[spb.AFTType]bool
		// wantResponses is the expected slice of streamed GetResponse messages.
		wantResponses []*spb.GetResponse
		// wantErr indicates whether an error is expected.
		wantErr bool
	}{{
		desc:  "empty RIB",
		inRIB: NewRIBHolder("VRF-1"),
		inFilter: map[spb.AFTType]bool{
			spb.AFTType_ALL: true,
		},
		wantResponses: []*spb.GetResponse{},
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
		desc:  "all tables populated but filtered to mpls",
		inRIB: allPopRIB,
		inFilter: map[spb.AFTType]bool{
			spb.AFTType_MPLS: true,
		},
		wantResponses: []*spb.GetResponse{{
			Entry: []*spb.AFTEntry{{
				NetworkInstance: "VRF-42",
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
						Id: 42,
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
							IpAddress: &wpb.StringValue{Value: "1.1.1.1"},
						},
					},
				},
			}},
		}},
	}, {
		desc:  "all tables populated but filtered to ipv6",
		inRIB: allPopRIB,
		inFilter: map[spb.AFTType]bool{
			spb.AFTType_IPV6: true,
		},
		wantResponses: []*spb.GetResponse{{
			Entry: []*spb.AFTEntry{{
				NetworkInstance: "VRF-42",
				Entry: &spb.AFTEntry_Ipv6{
					Ipv6: &aftpb.Afts_Ipv6EntryKey{
						Prefix: "2001:4c20::/32",
						Ipv6Entry: &aftpb.Afts_Ipv6Entry{
							NextHopGroup: &wpb.UintValue{Value: 42},
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
				t.Fatalf("GetRIB(...): did not get expected error, got: %v, wantErr? %v", err, tt.wantErr)
			}
			doneCh <- struct{}{}

			if diff := cmp.Diff(got, tt.wantResponses, protocmp.Transform()); diff != "" {
				t.Fatalf("GetRIB(...): did not get expected responses, diff(-got,+want):\n%s", diff)
			}
		})
	}
}

// TestResolvedEntryHook verifies that SetResolvedEntryHook is invoked on Add, Replace, and Delete.
func TestResolvedEntryHook(t *testing.T) {
	var (
		mu       sync.Mutex
		gotEvent string
		ch       = make(chan struct{}, 1)
	)
	hook := func(_ map[string]*AFTs, optype constants.OpType, netinst string, aft constants.AFT, key any, _ ...ResolvedDetails) {
		mu.Lock()
		gotEvent = fmt.Sprintf("%s %s:%s->%v", optype, aft, netinst, key)
		mu.Unlock()
		ch <- struct{}{}
	}

	r := mustChainRIB()
	r.SetResolvedEntryHook(hook)

	if _, fails, err := r.AddEntry(defName, &spb.AFTOperation{
		Id: 99,
		Op: spb.AFTOperation_ADD,
		Entry: &spb.AFTOperation_Ipv4{
			Ipv4: &aftpb.Afts_Ipv4EntryKey{
				Prefix: "10.0.0.0/8",
				Ipv4Entry: &aftpb.Afts_Ipv4Entry{
					NextHopGroup: &wpb.UintValue{Value: 1},
				},
			},
		},
	}); err != nil || len(fails) != 0 {
		t.Fatalf("AddEntry(...): unexpected error %v or fails %v", err, fails)
	}
	<-ch
	mu.Lock()
	if want := "Add IPv4:DEFAULT->10.0.0.0/8"; gotEvent != want {
		t.Fatalf("SetResolvedEntryHook: got %q, want %q", gotEvent, want)
	}
	mu.Unlock()
}

// TestFlush verifies that Flush removes all entries and resets reference counts across network instances.
func TestFlush(t *testing.T) {
	dn := "DEFAULT"
	nd := "NON-DEFAULT"
	rr := New(dn)
	if err := rr.AddNetworkInstance(nd); err != nil {
		t.Fatalf("AddNetworkInstance(%q): %v", nd, err)
	}

	defRIB := rr.niRIB[dn]
	nonDefRIB := rr.niRIB[nd]

	if _, _, err := defRIB.AddNextHop(&aftpb.Afts_NextHopKey{
		Index: 1,
		NextHop: &aftpb.Afts_NextHop{
			IpAddress: &wpb.StringValue{Value: "192.0.2.1"},
		},
	}, false); err != nil {
		t.Fatalf("AddNextHop: %v", err)
	}

	if _, _, err := defRIB.AddNextHopGroup(&aftpb.Afts_NextHopGroupKey{
		Id: 1,
		NextHopGroup: &aftpb.Afts_NextHopGroup{
			NextHop: []*aftpb.Afts_NextHopGroup_NextHopKey{{
				Index: 1,
				NextHop: &aftpb.Afts_NextHopGroup_NextHop{
					Weight: &wpb.UintValue{Value: 1},
				},
			}},
		},
	}, false); err != nil {
		t.Fatalf("AddNextHopGroup: %v", err)
	}

	if _, _, err := nonDefRIB.AddIPv4(&aftpb.Afts_Ipv4EntryKey{
		Prefix: "192.0.2.0/32",
		Ipv4Entry: &aftpb.Afts_Ipv4Entry{
			NextHopGroup:                &wpb.UintValue{Value: 1},
			NextHopGroupNetworkInstance: &wpb.StringValue{Value: dn},
		},
	}, false); err != nil {
		t.Fatalf("AddIPv4: %v", err)
	}

	if err := rr.Flush([]string{nd, dn}); err != nil {
		t.Fatalf("Flush: unexpected error: %v", err)
	}

	for _, niName := range []string{nd, dn} {
		niRIB, ok := rr.NetworkInstanceRIB(niName)
		if !ok {
			t.Fatalf("NetworkInstanceRIB(%q): not found", niName)
		}
		if niRIB.IPv4Count() != 0 || niRIB.IPv6Count() != 0 || niRIB.MPLSCount() != 0 || niRIB.NextHopGroupCount() != 0 || niRIB.NextHopCount() != 0 {
			t.Fatalf("NI %s: expected 0 entries after Flush, got %s", niName, niRIB)
		}
		for nhID, gotCount := range niRIB.refCounts.NextHop {
			if gotCount != 0 {
				t.Errorf("NI %s: non-zero refcount for NH %d: %d", niName, nhID, gotCount)
			}
		}
		for nhgID, gotCount := range niRIB.refCounts.NextHopGroup {
			if gotCount != 0 {
				t.Errorf("NI %s: non-zero refcount for NHG %d: %d", niName, nhgID, gotCount)
			}
		}
	}
}

// TestRIBContentsAndGetters verifies RIBContents, GetIPv4, GetIPv6, GetMPLS, GetNextHopGroup,
// GetNextHop, KnownNetworkInstances, and RangeNetworkInstances.
func TestRIBContentsAndGetters(t *testing.T) {
	r := mustChainRIB()
	rh, ok := r.NetworkInstanceRIB(defName)
	if !ok {
		t.Fatalf("NetworkInstanceRIB(%q): not found", defName)
	}
	if _, ok := rh.GetIPv4("1.1.1.1/32"); !ok {
		t.Fatalf("GetIPv4(1.1.1.1/32): expected true, got false")
	}
	if _, ok := rh.GetNextHopGroup(1); !ok {
		t.Fatalf("GetNextHopGroup(1): expected true, got false")
	}
	if _, ok := rh.GetNextHop(1); !ok {
		t.Fatalf("GetNextHop(1): expected true, got false")
	}
	if got := r.KnownNetworkInstances(); !cmp.Equal(got, []string{defName}) {
		t.Fatalf("KnownNetworkInstances(): got %v, want [%s]", got, defName)
	}

	contents, err := r.RIBContents()
	if err != nil {
		t.Fatalf("RIBContents(): unexpected error: %v", err)
	}
	delete(contents[defName].IPv4Entry, "1.1.1.1/32")
	if rh.IPv4Count() != 1 {
		t.Fatalf("IPv4Count(): mutating RIBContents map mutated live RIBHolder")
	}
}

// TestCloneAFTOpSlice verifies that CloneAFTOpSlice produces an independent deep copy of AFT operations.
func TestCloneAFTOpSlice(t *testing.T) {
	ops := []*spb.AFTOperation{{
		Id:              1,
		NetworkInstance: defName,
		Op:              spb.AFTOperation_ADD,
		Entry: &spb.AFTOperation_Ipv4{
			Ipv4: &aftpb.Afts_Ipv4EntryKey{
				Prefix: "10.0.0.0/24",
				Ipv4Entry: &aftpb.Afts_Ipv4Entry{
					NextHopGroup:  &wpb.UintValue{Value: 1},
					EntryMetadata: &wpb.BytesValue{Value: []byte{1, 2, 3, 4}},
				},
			},
		},
	}, {
		Id:              2,
		NetworkInstance: defName,
		Op:              spb.AFTOperation_ADD,
		Entry: &spb.AFTOperation_Ipv6{
			Ipv6: &aftpb.Afts_Ipv6EntryKey{
				Prefix: "2001:db8::/32",
				Ipv6Entry: &aftpb.Afts_Ipv6Entry{
					NextHopGroup: &wpb.UintValue{Value: 1},
				},
			},
		},
	}, {
		Id:              3,
		NetworkInstance: defName,
		Op:              spb.AFTOperation_ADD,
		Entry: &spb.AFTOperation_Mpls{
			Mpls: &aftpb.Afts_LabelEntryKey{
				Label: &aftpb.Afts_LabelEntryKey_LabelOpenconfigmplstypesmplslabelenum{
					LabelOpenconfigmplstypesmplslabelenum: enumpb.OpenconfigMplsTypesMplsLabelEnum_OPENCONFIGMPLSTYPESMPLSLABELENUM_IPV4_EXPLICIT_NULL,
				},
				LabelEntry: &aftpb.Afts_LabelEntry{
					NextHopGroup: &wpb.UintValue{Value: 1},
					PoppedMplsLabelStack: []*aftpb.Afts_LabelEntry_PoppedMplsLabelStackUnion{{
						PoppedMplsLabelStackUint64: 100,
					}},
				},
			},
		},
	}, {
		Id:              4,
		NetworkInstance: defName,
		Op:              spb.AFTOperation_ADD,
		Entry: &spb.AFTOperation_NextHopGroup{
			NextHopGroup: &aftpb.Afts_NextHopGroupKey{
				Id: 1,
				NextHopGroup: &aftpb.Afts_NextHopGroup{
					Color: &wpb.UintValue{Value: 10},
					NextHop: []*aftpb.Afts_NextHopGroup_NextHopKey{{
						Index: 1,
						NextHop: &aftpb.Afts_NextHopGroup_NextHop{
							Weight: &wpb.UintValue{Value: 1},
						},
					}},
				},
			},
		},
	}, {
		Id:              5,
		NetworkInstance: defName,
		Op:              spb.AFTOperation_ADD,
		Entry: &spb.AFTOperation_NextHop{
			NextHop: &aftpb.Afts_NextHopKey{
				Index: 1,
				NextHop: &aftpb.Afts_NextHop{
					IpAddress: &wpb.StringValue{Value: "192.0.2.1"},
					InterfaceRef: &aftpb.Afts_NextHop_InterfaceRef{
						Interface:    &wpb.StringValue{Value: "eth0"},
						Subinterface: &wpb.UintValue{Value: 0},
					},
					PushedMplsLabelStack: []*aftpb.Afts_NextHop_PushedMplsLabelStackUnion{{
						PushedMplsLabelStackUint64: 200,
					}},
				},
			},
		},
	}}

	cloned := CloneAFTOpSlice(ops, func(op *spb.AFTOperation) *spb.AFTOperation {
		return proto.Clone(op).(*spb.AFTOperation)
	})

	if diff := cmp.Diff(cloned, ops, protocmp.Transform()); diff != "" {
		t.Fatalf("CloneAFTOpSlice(...): diff(-got,+want):\n%s", diff)
	}

	// Mutate original to verify deep-copy isolation.
	ops[0].GetIpv4().Prefix = "192.168.0.0/16"
	if cloned[0].GetIpv4().GetPrefix() != "10.0.0.0/24" {
		t.Fatalf("CloneAFTOpSlice(...): mutating original mutated clone: %s", prototext.Format(cloned[0]))
	}
}

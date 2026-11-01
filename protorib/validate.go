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
	"math"
	"net/netip"

	"google.golang.org/protobuf/proto"

	aftpb "github.com/openconfig/gribi/v1/proto/gribi_aft"
	enumpb "github.com/openconfig/gribi/v1/proto/gribi_aft/enums"
	spb "github.com/openconfig/gribi/v1/proto/service"
	wpb "github.com/openconfig/ygot/proto/ywrapper"
)

// MPLSLabelKeyFromProto extracts and validates an MPLSLabelKey from a LabelEntryKey protobuf.
func MPLSLabelKeyFromProto(e *aftpb.Afts_LabelEntryKey) (MPLSLabelKey, error) {
	if e == nil {
		return MPLSLabelKey{}, errors.New("nil LabelEntryKey provided")
	}
	switch t := e.GetLabel().(type) {
	case *aftpb.Afts_LabelEntryKey_LabelUint64:
		if t == nil || t.LabelUint64 < 16 || t.LabelUint64 > 1048575 {
			return MPLSLabelKey{}, fmt.Errorf("invalid MPLS label %d out of range [16..1048575]", e.GetLabelUint64())
		}
		return MPLSLabelKey{Label: t.LabelUint64}, nil
	case *aftpb.Afts_LabelEntryKey_LabelOpenconfigmplstypesmplslabelenum:
		if t == nil {
			return MPLSLabelKey{}, errors.New("nil MPLS label enum wrapper")
		}
		v := int64(t.LabelOpenconfigmplstypesmplslabelenum)
		if v < 1 || v > 8 {
			return MPLSLabelKey{}, fmt.Errorf("invalid MPLS label enum %v", t.LabelOpenconfigmplstypesmplslabelenum)
		}
		return MPLSLabelKey{Enum: t.LabelOpenconfigmplstypesmplslabelenum}, nil
	default:
		return MPLSLabelKey{}, errors.New("missing or unsupported MPLS label key")
	}
}

// isValidIPv4Prefix reports whether s is a valid IPv4 prefix in CIDR notation.
func isValidIPv4Prefix(s string) bool {
	pfx, err := netip.ParsePrefix(s)
	return err == nil && pfx.Addr().Is4()
}

// isValidIPv6Prefix reports whether s is a valid IPv6 prefix in CIDR notation without a zone.
func isValidIPv6Prefix(s string) bool {
	pfx, err := netip.ParsePrefix(s)
	return err == nil && pfx.Addr().Is6() && pfx.Addr().Zone() == ""
}

// isValidIPv4Addr reports whether s is a valid IPv4 address.
func isValidIPv4Addr(s string) bool {
	addr, err := netip.ParseAddr(s)
	return err == nil && addr.Is4()
}

// isValidIPv6Addr reports whether s is a valid IPv6 address without a zone.
func isValidIPv6Addr(s string) bool {
	addr, err := netip.ParseAddr(s)
	return err == nil && addr.Is6() && addr.Zone() == ""
}

// isValidIPAddr reports whether s is a valid IPv4 or IPv6 address without a zone.
func isValidIPAddr(s string) bool {
	addr, err := netip.ParseAddr(s)
	return err == nil && addr.Zone() == ""
}

// isValidMACAddr reports whether s is a valid colon-separated 6-octet MAC address.
func isValidMACAddr(s string) bool {
	if len(s) != 17 {
		return false
	}
	for i := 0; i < 17; i++ {
		c := s[i]
		if i%3 == 2 {
			if c != ':' {
				return false
			}
		} else if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// checkUintRange validates that w, if non-nil, has a value within [minVal, maxVal].
func checkUintRange(w *wpb.UintValue, minVal, maxVal uint64, name string) error {
	if w == nil {
		return nil
	}
	if v := w.GetValue(); v < minVal || v > maxVal {
		return fmt.Errorf("invalid %s %d out of range [%d..%d]", name, v, minVal, maxVal)
	}
	return nil
}

// checkEncapHeaderType validates that e is within the defined OpenConfig encapsulation header enum range.
func checkEncapHeaderType(e enumpb.OpenconfigAftTypesEncapsulationHeaderType, name string) error {
	v := int64(e)
	if v < 0 || v > 8 {
		return fmt.Errorf("invalid %s %v", name, e)
	}
	return nil
}

// checkMPLSLabelUnion validates an MPLS label stack union entry that has either an enum or uint64 label.
func checkMPLSLabelUnion(enumVal enumpb.OpenconfigMplsTypesMplsLabelEnum, uintVal uint64, name string) error {
	if enumVal != enumpb.OpenconfigMplsTypesMplsLabelEnum_OPENCONFIGMPLSTYPESMPLSLABELENUM_UNSET {
		v := int64(enumVal)
		if v < 1 || v > 8 {
			return fmt.Errorf("invalid %s enum %v", name, enumVal)
		}
		return nil
	}
	if uintVal != 0 {
		if uintVal < 16 || uintVal > 1048575 {
			return fmt.Errorf("invalid %s label %d out of range [16..1048575]", name, uintVal)
		}
	}
	return nil
}

// clonedIPv4Proto co-locates an Afts_Ipv4EntryKey with its nested protobuf wrapper fields.
type clonedIPv4Proto struct {
	// key is the outer IPv4 entry key protobuf message.
	key aftpb.Afts_Ipv4EntryKey
	// entry is the inner IPv4 entry protobuf message.
	entry aftpb.Afts_Ipv4Entry
	// nhg holds the NextHopGroup wrapper value.
	nhg wpb.UintValue
	// nhgNI holds the NextHopGroupNetworkInstance wrapper value.
	nhgNI wpb.StringValue
	// meta holds the EntryMetadata wrapper value.
	meta wpb.BytesValue
	// metaBuf stores up to 8 bytes of inline EntryMetadata.
	metaBuf [8]byte
}

// validateAndCloneIPv4 validates k and returns an immutable deep copy of the IPv4 entry key.
func validateAndCloneIPv4(k *aftpb.Afts_Ipv4EntryKey) (*aftpb.Afts_Ipv4EntryKey, error) {
	if k == nil || k.Ipv4Entry == nil {
		return nil, errors.New("could not parse a field within the list gribi_aft.Afts.ipv4_entry , nil list member in field gribi_aft.Afts.Ipv4EntryKey.ipv4_entry, <nil>")
	}
	if !isValidIPv4Prefix(k.Prefix) {
		return nil, fmt.Errorf("invalid IPv4 prefix %q", k.Prefix)
	}
	p := k.Ipv4Entry
	if err := checkEncapHeaderType(p.DecapsulateHeader, "DecapsulateHeader"); err != nil {
		return nil, err
	}
	if p.EntryMetadata != nil && len(p.EntryMetadata.GetValue()) > 8 {
		return nil, fmt.Errorf("EntryMetadata length %d exceeds 8 bytes", len(p.EntryMetadata.GetValue()))
	}
	if len(k.ProtoReflect().GetUnknown()) > 0 || len(p.ProtoReflect().GetUnknown()) > 0 {
		return proto.Clone(k).(*aftpb.Afts_Ipv4EntryKey), nil
	}

	var c clonedIPv4Proto
	c.key.Prefix = k.Prefix
	c.entry.DecapsulateHeader = p.DecapsulateHeader
	if p.EntryMetadata != nil {
		if b := p.EntryMetadata.Value; b != nil {
			m := len(b)
			copy(c.metaBuf[:m], b)
			c.meta.Value = c.metaBuf[:m:m]
		}
		c.entry.EntryMetadata = &c.meta
	}
	if p.NextHopGroup != nil {
		c.nhg.Value = p.NextHopGroup.Value
		c.entry.NextHopGroup = &c.nhg
	}
	if p.NextHopGroupNetworkInstance != nil {
		c.nhgNI.Value = p.NextHopGroupNetworkInstance.Value
		c.entry.NextHopGroupNetworkInstance = &c.nhgNI
	}
	c.key.Ipv4Entry = &c.entry
	return &c.key, nil
}

// clonedIPv6Proto co-locates an Afts_Ipv6EntryKey with its nested protobuf wrapper fields.
type clonedIPv6Proto struct {
	// key is the outer IPv6 entry key protobuf message.
	key aftpb.Afts_Ipv6EntryKey
	// entry is the inner IPv6 entry protobuf message.
	entry aftpb.Afts_Ipv6Entry
	// nhg holds the NextHopGroup wrapper value.
	nhg wpb.UintValue
	// nhgNI holds the NextHopGroupNetworkInstance wrapper value.
	nhgNI wpb.StringValue
	// meta holds the EntryMetadata wrapper value.
	meta wpb.BytesValue
	// metaBuf stores up to 8 bytes of inline EntryMetadata.
	metaBuf [8]byte
}

// validateAndCloneIPv6 validates k and returns an immutable deep copy of the IPv6 entry key.
func validateAndCloneIPv6(k *aftpb.Afts_Ipv6EntryKey) (*aftpb.Afts_Ipv6EntryKey, error) {
	if k == nil || k.Ipv6Entry == nil {
		return nil, errors.New("could not parse a field within the list gribi_aft.Afts.ipv6_entry , nil list member in field gribi_aft.Afts.Ipv6EntryKey.ipv6_entry, <nil>")
	}
	if !isValidIPv6Prefix(k.Prefix) {
		return nil, fmt.Errorf("invalid IPv6 prefix %q", k.Prefix)
	}
	p := k.Ipv6Entry
	if err := checkEncapHeaderType(p.DecapsulateHeader, "DecapsulateHeader"); err != nil {
		return nil, err
	}
	if p.EntryMetadata != nil && len(p.EntryMetadata.GetValue()) > 8 {
		return nil, fmt.Errorf("EntryMetadata length %d exceeds 8 bytes", len(p.EntryMetadata.GetValue()))
	}
	if len(k.ProtoReflect().GetUnknown()) > 0 || len(p.ProtoReflect().GetUnknown()) > 0 {
		return proto.Clone(k).(*aftpb.Afts_Ipv6EntryKey), nil
	}

	var c clonedIPv6Proto
	c.key.Prefix = k.Prefix
	c.entry.DecapsulateHeader = p.DecapsulateHeader
	if p.EntryMetadata != nil {
		if b := p.EntryMetadata.Value; b != nil {
			m := len(b)
			copy(c.metaBuf[:m], b)
			c.meta.Value = c.metaBuf[:m:m]
		}
		c.entry.EntryMetadata = &c.meta
	}
	if p.NextHopGroup != nil {
		c.nhg.Value = p.NextHopGroup.Value
		c.entry.NextHopGroup = &c.nhg
	}
	if p.NextHopGroupNetworkInstance != nil {
		c.nhgNI.Value = p.NextHopGroupNetworkInstance.Value
		c.entry.NextHopGroupNetworkInstance = &c.nhgNI
	}
	c.key.Ipv6Entry = &c.entry
	return &c.key, nil
}

// clonedMPLSProto co-locates an Afts_LabelEntryKey with its nested protobuf wrapper fields.
type clonedMPLSProto struct {
	// key is the outer MPLS label entry key protobuf message.
	key aftpb.Afts_LabelEntryKey
	// labelUint holds the uint64 label oneof wrapper.
	labelUint aftpb.Afts_LabelEntryKey_LabelUint64
	// labelEnum holds the enum label oneof wrapper.
	labelEnum aftpb.Afts_LabelEntryKey_LabelOpenconfigmplstypesmplslabelenum
	// entry is the inner label entry protobuf message.
	entry aftpb.Afts_LabelEntry
	// nhg holds the NextHopGroup wrapper value.
	nhg wpb.UintValue
	// nhgNI holds the NextHopGroupNetworkInstance wrapper value.
	nhgNI wpb.StringValue
	// meta holds the EntryMetadata wrapper value.
	meta wpb.BytesValue
	// metaBuf stores up to 8 bytes of inline EntryMetadata.
	metaBuf [8]byte
	// poppedPtrs stores inline pointers for up to 2 popped MPLS labels.
	poppedPtrs [2]*aftpb.Afts_LabelEntry_PoppedMplsLabelStackUnion
	// poppedVals stores inline values for up to 2 popped MPLS labels.
	poppedVals [2]aftpb.Afts_LabelEntry_PoppedMplsLabelStackUnion
}

// validateAndCloneMPLS validates k and returns its MPLSLabelKey and an immutable deep copy.
func validateAndCloneMPLS(k *aftpb.Afts_LabelEntryKey) (MPLSLabelKey, *aftpb.Afts_LabelEntryKey, error) {
	if k == nil || k.LabelEntry == nil {
		return MPLSLabelKey{}, nil, errors.New("could not parse a field within the list gribi_aft.Afts.label_entry , nil list member in field gribi_aft.Afts.LabelEntryKey.label_entry, <nil>")
	}
	lk, err := MPLSLabelKeyFromProto(k)
	if err != nil {
		return MPLSLabelKey{}, nil, err
	}
	p := k.LabelEntry
	if p.EntryMetadata != nil && len(p.EntryMetadata.GetValue()) > 8 {
		return MPLSLabelKey{}, nil, fmt.Errorf("EntryMetadata length %d exceeds 8 bytes", len(p.EntryMetadata.GetValue()))
	}
	for _, u := range p.PoppedMplsLabelStack {
		if u == nil {
			continue
		}
		if err := checkMPLSLabelUnion(u.PoppedMplsLabelStackOpenconfigmplstypesmplslabelenum, u.PoppedMplsLabelStackUint64, "PoppedMplsLabelStack"); err != nil {
			return MPLSLabelKey{}, nil, err
		}
	}
	if len(k.ProtoReflect().GetUnknown()) > 0 || len(p.ProtoReflect().GetUnknown()) > 0 {
		return lk, proto.Clone(k).(*aftpb.Afts_LabelEntryKey), nil
	}

	var c clonedMPLSProto
	if lk.Enum != enumpb.OpenconfigMplsTypesMplsLabelEnum_OPENCONFIGMPLSTYPESMPLSLABELENUM_UNSET {
		c.labelEnum.LabelOpenconfigmplstypesmplslabelenum = lk.Enum
		c.key.Label = &c.labelEnum
	} else {
		c.labelUint.LabelUint64 = lk.Label
		c.key.Label = &c.labelUint
	}
	if p.EntryMetadata != nil {
		if b := p.EntryMetadata.Value; b != nil {
			m := len(b)
			copy(c.metaBuf[:m], b)
			c.meta.Value = c.metaBuf[:m:m]
		}
		c.entry.EntryMetadata = &c.meta
	}
	if p.NextHopGroup != nil {
		c.nhg.Value = p.NextHopGroup.Value
		c.entry.NextHopGroup = &c.nhg
	}
	if p.NextHopGroupNetworkInstance != nil {
		c.nhgNI.Value = p.NextHopGroupNetworkInstance.Value
		c.entry.NextHopGroupNetworkInstance = &c.nhgNI
	}
	if m := len(p.PoppedMplsLabelStack); m > 0 {
		var stack []*aftpb.Afts_LabelEntry_PoppedMplsLabelStackUnion
		var pool []aftpb.Afts_LabelEntry_PoppedMplsLabelStackUnion
		if m <= len(c.poppedPtrs) {
			stack = c.poppedPtrs[:m:m]
			pool = c.poppedVals[:m]
		} else {
			stack = make([]*aftpb.Afts_LabelEntry_PoppedMplsLabelStackUnion, m)
			pool = make([]aftpb.Afts_LabelEntry_PoppedMplsLabelStackUnion, m)
		}
		for i, u := range p.PoppedMplsLabelStack {
			if u != nil {
				pool[i].PoppedMplsLabelStackUint64 = u.PoppedMplsLabelStackUint64
				pool[i].PoppedMplsLabelStackOpenconfigmplstypesmplslabelenum = u.PoppedMplsLabelStackOpenconfigmplstypesmplslabelenum
				stack[i] = &pool[i]
			}
		}
		c.entry.PoppedMplsLabelStack = stack
	}
	c.key.LabelEntry = &c.entry
	return lk, &c.key, nil
}

// clonedNHGOpNH co-locates a NextHopGroup NextHopKey protobuf with its nested messages.
type clonedNHGOpNH struct {
	// key is the NextHopKey protobuf message inside a NextHopGroup.
	key aftpb.Afts_NextHopGroup_NextHopKey
	// entry is the inner NextHop protobuf message inside a NextHopGroup.
	entry aftpb.Afts_NextHopGroup_NextHop
	// weight holds the Weight wrapper value.
	weight wpb.UintValue
}

// clonedNHGProto co-locates an Afts_NextHopGroupKey with its nested protobuf wrapper fields.
type clonedNHGProto struct {
	// key is the outer NextHopGroupKey protobuf message.
	key aftpb.Afts_NextHopGroupKey
	// entry is the inner NextHopGroup protobuf message.
	entry aftpb.Afts_NextHopGroup
	// backup holds the BackupNextHopGroup wrapper value.
	backup wpb.UintValue
	// color holds the Color wrapper value.
	color wpb.UintValue
	// nhPtrs stores inline pointers for up to 4 member next-hops.
	nhPtrs [4]*aftpb.Afts_NextHopGroup_NextHopKey
	// nhNodes stores inline structs for up to 4 member next-hops.
	nhNodes [4]clonedNHGOpNH
}

// validateAndCloneNHG validates k and returns an immutable deep copy of the NextHopGroupKey.
func validateAndCloneNHG(k *aftpb.Afts_NextHopGroupKey) (*aftpb.Afts_NextHopGroupKey, error) {
	if k == nil || k.NextHopGroup == nil {
		return nil, errors.New("could not parse a field within the list gribi_aft.Afts.next_hop_group , nil list member in field gribi_aft.Afts.NextHopGroupKey.next_hop_group, <nil>")
	}
	p := k.NextHopGroup
	m := len(p.NextHop)
	if m > 8 {
		seen := make(map[uint64]struct{}, m)
		for _, nhk := range p.NextHop {
			if nhk == nil || nhk.NextHop == nil {
				return nil, errors.New("could not parse a field within the list gribi_aft.Afts.NextHopGroup.next_hop , nil list member in field gribi_aft.Afts.NextHopGroup.NextHopKey.next_hop, <nil>")
			}
			if _, exists := seen[nhk.Index]; exists {
				return nil, fmt.Errorf("duplicate NextHop index %d in NextHopGroup %d", nhk.Index, k.Id)
			}
			seen[nhk.Index] = struct{}{}
		}
	} else {
		for i, nhk := range p.NextHop {
			if nhk == nil || nhk.NextHop == nil {
				return nil, errors.New("could not parse a field within the list gribi_aft.Afts.NextHopGroup.next_hop , nil list member in field gribi_aft.Afts.NextHopGroup.NextHopKey.next_hop, <nil>")
			}
			for j := 0; j < i; j++ {
				if p.NextHop[j].Index == nhk.Index {
					return nil, fmt.Errorf("duplicate NextHop index %d in NextHopGroup %d", nhk.Index, k.Id)
				}
			}
		}
	}
	if len(k.ProtoReflect().GetUnknown()) > 0 || len(p.ProtoReflect().GetUnknown()) > 0 {
		return proto.Clone(k).(*aftpb.Afts_NextHopGroupKey), nil
	}

	var c clonedNHGProto
	c.key.Id = k.Id
	if p.BackupNextHopGroup != nil {
		c.backup.Value = p.BackupNextHopGroup.Value
		c.entry.BackupNextHopGroup = &c.backup
	}
	if p.Color != nil {
		c.color.Value = p.Color.Value
		c.entry.Color = &c.color
	}
	if m > 0 {
		var nhs []*aftpb.Afts_NextHopGroup_NextHopKey
		var pool []clonedNHGOpNH
		if m <= len(c.nhPtrs) {
			nhs = c.nhPtrs[:m:m]
			pool = c.nhNodes[:m]
		} else {
			nhs = make([]*aftpb.Afts_NextHopGroup_NextHopKey, m)
			pool = make([]clonedNHGOpNH, m)
		}
		for i, nhk := range p.NextHop {
			n := &pool[i]
			n.key.Index = nhk.Index
			if nhk.NextHop.Weight != nil {
				n.weight.Value = nhk.NextHop.Weight.Value
				n.entry.Weight = &n.weight
			}
			n.key.NextHop = &n.entry
			nhs[i] = &n.key
		}
		c.entry.NextHop = nhs
	}
	c.key.NextHopGroup = &c.entry
	return &c.key, nil
}

// clonedNHProto co-locates an Afts_NextHopKey with its common nested protobuf wrapper fields.
type clonedNHProto struct {
	// key is the outer NextHopKey protobuf message.
	key aftpb.Afts_NextHopKey
	// entry is the inner NextHop protobuf message.
	entry aftpb.Afts_NextHop
	// ip holds the IpAddress wrapper value.
	ip wpb.StringValue
	// mac holds the MacAddress wrapper value.
	mac wpb.StringValue
	// ni holds the NetworkInstance wrapper value.
	ni wpb.StringValue
	// pop holds the PopTopLabel wrapper value.
	pop wpb.BoolValue
	// tun holds the TunnelSrcIpAddress wrapper value.
	tun wpb.StringValue
	// vni holds the VniLabel wrapper value.
	vni wpb.UintValue
	// iref holds the InterfaceRef sub-message.
	iref aftpb.Afts_NextHop_InterfaceRef
	// iface holds the Interface wrapper value.
	iface wpb.StringValue
	// sub holds the Subinterface wrapper value.
	sub wpb.UintValue
	// pushedPtrs stores inline pointers for up to 2 pushed MPLS labels.
	pushedPtrs [2]*aftpb.Afts_NextHop_PushedMplsLabelStackUnion
	// pushedVals stores inline values for up to 2 pushed MPLS labels.
	pushedVals [2]aftpb.Afts_NextHop_PushedMplsLabelStackUnion
}

// greProtoGetter abstracts GRE protobuf containers with DstIp, SrcIp, and Ttl fields.
type greProtoGetter interface {
	GetDstIp() *wpb.StringValue
	GetSrcIp() *wpb.StringValue
	GetTtl() *wpb.UintValue
}

// validateGRE validates the fields of a GRE protobuf container.
func validateGRE[G greProtoGetter](g G, prefix string) error {
	if dst := g.GetDstIp(); dst != nil && !isValidIPAddr(dst.GetValue()) {
		return fmt.Errorf("invalid %s DstIp %q", prefix, dst.GetValue())
	}
	if src := g.GetSrcIp(); src != nil && !isValidIPAddr(src.GetValue()) {
		return fmt.Errorf("invalid %s SrcIp %q", prefix, src.GetValue())
	}
	return checkUintRange(g.GetTtl(), 0, math.MaxUint8, prefix+" Ttl")
}

// ipHeaderProtoGetter abstracts IPv4 and IPv6 encapsulation header protobuf containers.
type ipHeaderProtoGetter interface {
	GetDstIp() *wpb.StringValue
	GetSrcIp() *wpb.StringValue
}

// validateIPHeader validates an IPv4 or IPv6 encapsulation header protobuf container.
func validateIPHeader[H ipHeaderProtoGetter](h H, validIP func(string) bool, prefix string) error {
	if dst := h.GetDstIp(); dst != nil && !validIP(dst.GetValue()) {
		return fmt.Errorf("invalid %s DstIp %q", prefix, dst.GetValue())
	}
	if src := h.GetSrcIp(); src != nil && !validIP(src.GetValue()) {
		return fmt.Errorf("invalid %s SrcIp %q", prefix, src.GetValue())
	}
	return nil
}

// udpHeaderProtoGetter abstracts UDPv4 and UDPv6 encapsulation header protobuf containers.
type udpHeaderProtoGetter interface {
	GetDscp() *wpb.UintValue
	GetDstIp() *wpb.StringValue
	GetDstUdpPort() *wpb.UintValue
	GetIpTtl() *wpb.UintValue
	GetSrcIp() *wpb.StringValue
	GetSrcUdpPort() *wpb.UintValue
}

// validateUDPHeader validates a UDPv4 or UDPv6 encapsulation header protobuf container.
func validateUDPHeader[U udpHeaderProtoGetter](u U, validIP func(string) bool, prefix string) error {
	if err := checkUintRange(u.GetDscp(), 0, 63, prefix+" Dscp"); err != nil {
		return err
	}
	if dst := u.GetDstIp(); dst != nil && !validIP(dst.GetValue()) {
		return fmt.Errorf("invalid %s DstIp %q", prefix, dst.GetValue())
	}
	if err := checkUintRange(u.GetDstUdpPort(), 0, math.MaxUint16, prefix+" DstUdpPort"); err != nil {
		return err
	}
	if err := checkUintRange(u.GetIpTtl(), 0, math.MaxUint8, prefix+" IpTtl"); err != nil {
		return err
	}
	if src := u.GetSrcIp(); src != nil && !validIP(src.GetValue()) {
		return fmt.Errorf("invalid %s SrcIp %q", prefix, src.GetValue())
	}
	return checkUintRange(u.GetSrcUdpPort(), 0, math.MaxUint16, prefix+" SrcUdpPort")
}

// validateAndCloneNH validates k and returns an immutable deep copy of the NextHopKey.
func validateAndCloneNH(k *aftpb.Afts_NextHopKey) (*aftpb.Afts_NextHopKey, error) {
	if k == nil || k.NextHop == nil {
		return nil, errors.New("could not parse a field within the list gribi_aft.Afts.next_hop , nil list member in field gribi_aft.Afts.NextHopKey.next_hop, <nil>")
	}
	p := k.NextHop
	if err := checkEncapHeaderType(p.DecapsulateHeader, "DecapsulateHeader"); err != nil {
		return nil, err
	}
	if err := checkEncapHeaderType(p.EncapsulateHeader, "EncapsulateHeader"); err != nil {
		return nil, err
	}
	if p.IpAddress != nil && !isValidIPAddr(p.IpAddress.GetValue()) {
		return nil, fmt.Errorf("invalid NextHop IpAddress %q", p.IpAddress.GetValue())
	}
	if p.MacAddress != nil && !isValidMACAddr(p.MacAddress.GetValue()) {
		return nil, fmt.Errorf("invalid NextHop MacAddress %q", p.MacAddress.GetValue())
	}
	if p.TunnelSrcIpAddress != nil && !isValidIPAddr(p.TunnelSrcIpAddress.GetValue()) {
		return nil, fmt.Errorf("invalid NextHop TunnelSrcIpAddress %q", p.TunnelSrcIpAddress.GetValue())
	}
	if err := checkUintRange(p.VniLabel, 1, 16777215, "NextHop VniLabel"); err != nil {
		return nil, err
	}
	if p.Gre != nil {
		if err := validateGRE(p.Gre, "Gre"); err != nil {
			return nil, err
		}
	}
	if ipip := p.IpInIp; ipip != nil {
		if ipip.DstIp != nil && !isValidIPAddr(ipip.DstIp.GetValue()) {
			return nil, fmt.Errorf("invalid IpInIp DstIp %q", ipip.DstIp.GetValue())
		}
		if ipip.SrcIp != nil && !isValidIPAddr(ipip.SrcIp.GetValue()) {
			return nil, fmt.Errorf("invalid IpInIp SrcIp %q", ipip.SrcIp.GetValue())
		}
	}
	if iref := p.InterfaceRef; iref != nil {
		if err := checkUintRange(iref.Subinterface, 0, math.MaxUint32, "Subinterface"); err != nil {
			return nil, err
		}
	}
	for _, u := range p.PushedMplsLabelStack {
		if u == nil {
			continue
		}
		if err := checkMPLSLabelUnion(u.PushedMplsLabelStackOpenconfigmplstypesmplslabelenum, u.PushedMplsLabelStackUint64, "PushedMplsLabelStack"); err != nil {
			return nil, err
		}
	}
	if len(p.EncapHeader) > 0 {
		for i, ehk := range p.EncapHeader {
			if ehk == nil || ehk.EncapHeader == nil {
				return nil, errors.New("could not parse a field within the list gribi_aft.Afts.NextHop.encap_header , nil list member in field gribi_aft.Afts.NextHop.EncapHeaderKey.encap_header, <nil>")
			}
			if ehk.Index > math.MaxUint8 {
				return nil, fmt.Errorf("invalid EncapHeader index %d", ehk.Index)
			}
			for j := 0; j < i; j++ {
				if p.EncapHeader[j].Index == ehk.Index {
					return nil, fmt.Errorf("duplicate EncapHeader index %d", ehk.Index)
				}
			}
			ep := ehk.EncapHeader
			if err := checkEncapHeaderType(ep.Type, "EncapHeader Type"); err != nil {
				return nil, err
			}
			if ep.Gre != nil {
				if err := validateGRE(ep.Gre, "EncapHeader Gre"); err != nil {
					return nil, err
				}
			}
			if ep.Ipv4 != nil {
				if err := validateIPHeader(ep.Ipv4, isValidIPv4Addr, "EncapHeader Ipv4"); err != nil {
					return nil, err
				}
			}
			if ep.Ipv6 != nil {
				if err := validateIPHeader(ep.Ipv6, isValidIPv6Addr, "EncapHeader Ipv6"); err != nil {
					return nil, err
				}
			}
			if m := ep.Mpls; m != nil {
				for _, u := range m.MplsLabelStack {
					if u == nil {
						continue
					}
					if err := checkMPLSLabelUnion(u.MplsLabelStackOpenconfigmplstypesmplslabelenum, u.MplsLabelStackUint64, "EncapHeader MplsLabelStack"); err != nil {
						return nil, err
					}
				}
				if err := checkUintRange(m.TrafficClass, 0, 7, "EncapHeader Mpls TrafficClass"); err != nil {
					return nil, err
				}
			}
			if ep.UdpV4 != nil {
				if err := validateUDPHeader(ep.UdpV4, isValidIPv4Addr, "EncapHeader UdpV4"); err != nil {
					return nil, err
				}
			}
			if ep.UdpV6 != nil {
				if err := validateUDPHeader(ep.UdpV6, isValidIPv6Addr, "EncapHeader UdpV6"); err != nil {
					return nil, err
				}
			}
		}
	}

	if p.Gre != nil || p.IpInIp != nil || len(p.EncapHeader) > 0 || len(k.ProtoReflect().GetUnknown()) > 0 || len(p.ProtoReflect().GetUnknown()) > 0 {
		return proto.Clone(k).(*aftpb.Afts_NextHopKey), nil
	}

	var c clonedNHProto
	c.key.Index = k.Index
	c.entry.DecapsulateHeader = p.DecapsulateHeader
	c.entry.EncapsulateHeader = p.EncapsulateHeader
	if p.IpAddress != nil {
		c.ip.Value = p.IpAddress.Value
		c.entry.IpAddress = &c.ip
	}
	if p.MacAddress != nil {
		c.mac.Value = p.MacAddress.Value
		c.entry.MacAddress = &c.mac
	}
	if p.NetworkInstance != nil {
		c.ni.Value = p.NetworkInstance.Value
		c.entry.NetworkInstance = &c.ni
	}
	if p.PopTopLabel != nil {
		c.pop.Value = p.PopTopLabel.Value
		c.entry.PopTopLabel = &c.pop
	}
	if p.TunnelSrcIpAddress != nil {
		c.tun.Value = p.TunnelSrcIpAddress.Value
		c.entry.TunnelSrcIpAddress = &c.tun
	}
	if p.VniLabel != nil {
		c.vni.Value = p.VniLabel.Value
		c.entry.VniLabel = &c.vni
	}
	if p.InterfaceRef != nil {
		if p.InterfaceRef.Interface != nil {
			c.iface.Value = p.InterfaceRef.Interface.Value
			c.iref.Interface = &c.iface
		}
		if p.InterfaceRef.Subinterface != nil {
			c.sub.Value = p.InterfaceRef.Subinterface.Value
			c.iref.Subinterface = &c.sub
		}
		c.entry.InterfaceRef = &c.iref
	}
	if m := len(p.PushedMplsLabelStack); m > 0 {
		var stack []*aftpb.Afts_NextHop_PushedMplsLabelStackUnion
		var pool []aftpb.Afts_NextHop_PushedMplsLabelStackUnion
		if m <= len(c.pushedPtrs) {
			stack = c.pushedPtrs[:m:m]
			pool = c.pushedVals[:m]
		} else {
			stack = make([]*aftpb.Afts_NextHop_PushedMplsLabelStackUnion, m)
			pool = make([]aftpb.Afts_NextHop_PushedMplsLabelStackUnion, m)
		}
		for i, u := range p.PushedMplsLabelStack {
			if u != nil {
				pool[i].PushedMplsLabelStackUint64 = u.PushedMplsLabelStackUint64
				pool[i].PushedMplsLabelStackOpenconfigmplstypesmplslabelenum = u.PushedMplsLabelStackOpenconfigmplstypesmplslabelenum
				stack[i] = &pool[i]
			}
		}
		c.entry.PushedMplsLabelStack = stack
	}
	c.key.NextHop = &c.entry
	return &c.key, nil
}

// clonedIPv4Op co-locates a cloned IPv4 AFTOperation with its nested protobuf messages.
type clonedIPv4Op struct {
	// op is the outer AFTOperation protobuf message.
	op spb.AFTOperation
	// oneof is the IPv4 entry oneof wrapper.
	oneof spb.AFTOperation_Ipv4
	// key is the IPv4 entry key protobuf message.
	key aftpb.Afts_Ipv4EntryKey
	// entry is the inner IPv4 entry protobuf message.
	entry aftpb.Afts_Ipv4Entry
	// nhg holds the NextHopGroup wrapper value.
	nhg wpb.UintValue
	// nhgNI holds the NextHopGroupNetworkInstance wrapper value.
	nhgNI wpb.StringValue
	// meta holds the EntryMetadata wrapper value.
	meta wpb.BytesValue
	// metaBuf stores up to 8 bytes of inline EntryMetadata.
	metaBuf [8]byte
}

// clonedIPv6Op co-locates a cloned IPv6 AFTOperation with its nested protobuf messages.
type clonedIPv6Op struct {
	// op is the outer AFTOperation protobuf message.
	op spb.AFTOperation
	// oneof is the IPv6 entry oneof wrapper.
	oneof spb.AFTOperation_Ipv6
	// key is the IPv6 entry key protobuf message.
	key aftpb.Afts_Ipv6EntryKey
	// entry is the inner IPv6 entry protobuf message.
	entry aftpb.Afts_Ipv6Entry
	// nhg holds the NextHopGroup wrapper value.
	nhg wpb.UintValue
	// nhgNI holds the NextHopGroupNetworkInstance wrapper value.
	nhgNI wpb.StringValue
	// meta holds the EntryMetadata wrapper value.
	meta wpb.BytesValue
	// metaBuf stores up to 8 bytes of inline EntryMetadata.
	metaBuf [8]byte
}

// clonedMPLSOp co-locates a cloned MPLS AFTOperation with its nested protobuf messages.
type clonedMPLSOp struct {
	// op is the outer AFTOperation protobuf message.
	op spb.AFTOperation
	// oneof is the MPLS entry oneof wrapper.
	oneof spb.AFTOperation_Mpls
	// key is the MPLS label entry key protobuf message.
	key aftpb.Afts_LabelEntryKey
	// labelUint holds the uint64 label oneof wrapper.
	labelUint aftpb.Afts_LabelEntryKey_LabelUint64
	// labelEnum holds the enum label oneof wrapper.
	labelEnum aftpb.Afts_LabelEntryKey_LabelOpenconfigmplstypesmplslabelenum
	// entry is the inner label entry protobuf message.
	entry aftpb.Afts_LabelEntry
	// nhg holds the NextHopGroup wrapper value.
	nhg wpb.UintValue
	// nhgNI holds the NextHopGroupNetworkInstance wrapper value.
	nhgNI wpb.StringValue
	// meta holds the EntryMetadata wrapper value.
	meta wpb.BytesValue
	// metaBuf stores up to 8 bytes of inline EntryMetadata.
	metaBuf [8]byte
	// poppedPtrs stores inline pointers for up to 2 popped MPLS labels.
	poppedPtrs [2]*aftpb.Afts_LabelEntry_PoppedMplsLabelStackUnion
	// poppedVals stores inline values for up to 2 popped MPLS labels.
	poppedVals [2]aftpb.Afts_LabelEntry_PoppedMplsLabelStackUnion
}

// clonedNHGOp co-locates a cloned NextHopGroup AFTOperation with its nested protobuf messages.
type clonedNHGOp struct {
	// op is the outer AFTOperation protobuf message.
	op spb.AFTOperation
	// oneof is the NextHopGroup entry oneof wrapper.
	oneof spb.AFTOperation_NextHopGroup
	// key is the NextHopGroupKey protobuf message.
	key aftpb.Afts_NextHopGroupKey
	// entry is the inner NextHopGroup protobuf message.
	entry aftpb.Afts_NextHopGroup
	// backup holds the BackupNextHopGroup wrapper value.
	backup wpb.UintValue
	// color holds the Color wrapper value.
	color wpb.UintValue
	// nhPtrs stores inline pointers for up to 4 member next-hops.
	nhPtrs [4]*aftpb.Afts_NextHopGroup_NextHopKey
	// nhNodes stores inline structs for up to 4 member next-hops.
	nhNodes [4]clonedNHGOpNH
}

// clonedNHOp co-locates a cloned NextHop AFTOperation with its nested protobuf messages.
type clonedNHOp struct {
	// op is the outer AFTOperation protobuf message.
	op spb.AFTOperation
	// oneof is the NextHop entry oneof wrapper.
	oneof spb.AFTOperation_NextHop
	// key is the NextHopKey protobuf message.
	key aftpb.Afts_NextHopKey
	// entry is the inner NextHop protobuf message.
	entry aftpb.Afts_NextHop
	// ip holds the IpAddress wrapper value.
	ip wpb.StringValue
	// mac holds the MacAddress wrapper value.
	mac wpb.StringValue
	// ni holds the NetworkInstance wrapper value.
	ni wpb.StringValue
	// pop holds the PopTopLabel wrapper value.
	pop wpb.BoolValue
	// tun holds the TunnelSrcIpAddress wrapper value.
	tun wpb.StringValue
	// vni holds the VniLabel wrapper value.
	vni wpb.UintValue
	// iref holds the InterfaceRef sub-message.
	iref aftpb.Afts_NextHop_InterfaceRef
	// iface holds the Interface wrapper value.
	iface wpb.StringValue
	// sub holds the Subinterface wrapper value.
	sub wpb.UintValue
	// pushedPtrs stores inline pointers for up to 2 pushed MPLS labels.
	pushedPtrs [2]*aftpb.Afts_NextHop_PushedMplsLabelStackUnion
	// pushedVals stores inline values for up to 2 pushed MPLS labels.
	pushedVals [2]aftpb.Afts_NextHop_PushedMplsLabelStackUnion
}

// canFastCloneIPv4Op reports whether op can be cloned using clonedIPv4Op.
func canFastCloneIPv4Op(op *spb.AFTOperation, e *spb.AFTOperation_Ipv4) bool {
	if op.ElectionId != nil || len(op.ProtoReflect().GetUnknown()) > 0 || e == nil || e.Ipv4 == nil {
		return false
	}
	if len(e.Ipv4.ProtoReflect().GetUnknown()) > 0 {
		return false
	}
	if v4 := e.Ipv4.Ipv4Entry; v4 != nil && len(v4.ProtoReflect().GetUnknown()) > 0 {
		return false
	}
	return true
}

// fillClonedIPv4Op populates c as a deep copy of op and e.
func fillClonedIPv4Op(c *clonedIPv4Op, op *spb.AFTOperation, e *spb.AFTOperation_Ipv4) *spb.AFTOperation {
	c.op.Id = op.Id
	c.op.NetworkInstance = op.NetworkInstance
	c.op.Op = op.Op
	c.key.Prefix = e.Ipv4.Prefix
	if src := e.Ipv4.Ipv4Entry; src != nil {
		c.entry.DecapsulateHeader = src.DecapsulateHeader
		if src.EntryMetadata != nil {
			if b := src.EntryMetadata.Value; b != nil {
				if m := len(b); m <= len(c.metaBuf) {
					copy(c.metaBuf[:m], b)
					c.meta.Value = c.metaBuf[:m:m]
				} else {
					c.meta.Value = append([]byte(nil), b...)
				}
			}
			c.entry.EntryMetadata = &c.meta
		}
		if src.NextHopGroup != nil {
			c.nhg.Value = src.NextHopGroup.Value
			c.entry.NextHopGroup = &c.nhg
		}
		if src.NextHopGroupNetworkInstance != nil {
			c.nhgNI.Value = src.NextHopGroupNetworkInstance.Value
			c.entry.NextHopGroupNetworkInstance = &c.nhgNI
		}
		c.key.Ipv4Entry = &c.entry
	}
	c.oneof.Ipv4 = &c.key
	c.op.Entry = &c.oneof
	return &c.op
}

// canFastCloneIPv6Op reports whether op can be cloned using clonedIPv6Op.
func canFastCloneIPv6Op(op *spb.AFTOperation, e *spb.AFTOperation_Ipv6) bool {
	if op.ElectionId != nil || len(op.ProtoReflect().GetUnknown()) > 0 || e == nil || e.Ipv6 == nil {
		return false
	}
	if len(e.Ipv6.ProtoReflect().GetUnknown()) > 0 {
		return false
	}
	if v6 := e.Ipv6.Ipv6Entry; v6 != nil && len(v6.ProtoReflect().GetUnknown()) > 0 {
		return false
	}
	return true
}

// fillClonedIPv6Op populates c as a deep copy of op and e.
func fillClonedIPv6Op(c *clonedIPv6Op, op *spb.AFTOperation, e *spb.AFTOperation_Ipv6) *spb.AFTOperation {
	c.op.Id = op.Id
	c.op.NetworkInstance = op.NetworkInstance
	c.op.Op = op.Op
	c.key.Prefix = e.Ipv6.Prefix
	if src := e.Ipv6.Ipv6Entry; src != nil {
		c.entry.DecapsulateHeader = src.DecapsulateHeader
		if src.EntryMetadata != nil {
			if b := src.EntryMetadata.Value; b != nil {
				if m := len(b); m <= len(c.metaBuf) {
					copy(c.metaBuf[:m], b)
					c.meta.Value = c.metaBuf[:m:m]
				} else {
					c.meta.Value = append([]byte(nil), b...)
				}
			}
			c.entry.EntryMetadata = &c.meta
		}
		if src.NextHopGroup != nil {
			c.nhg.Value = src.NextHopGroup.Value
			c.entry.NextHopGroup = &c.nhg
		}
		if src.NextHopGroupNetworkInstance != nil {
			c.nhgNI.Value = src.NextHopGroupNetworkInstance.Value
			c.entry.NextHopGroupNetworkInstance = &c.nhgNI
		}
		c.key.Ipv6Entry = &c.entry
	}
	c.oneof.Ipv6 = &c.key
	c.op.Entry = &c.oneof
	return &c.op
}

// canFastCloneMPLSOp reports whether op can be cloned using clonedMPLSOp.
func canFastCloneMPLSOp(op *spb.AFTOperation, e *spb.AFTOperation_Mpls) bool {
	if op.ElectionId != nil || len(op.ProtoReflect().GetUnknown()) > 0 || e == nil || e.Mpls == nil {
		return false
	}
	if len(e.Mpls.ProtoReflect().GetUnknown()) > 0 {
		return false
	}
	if le := e.Mpls.LabelEntry; le != nil && len(le.ProtoReflect().GetUnknown()) > 0 {
		return false
	}
	return true
}

// fillClonedMPLSOp populates c as a deep copy of op and e.
func fillClonedMPLSOp(c *clonedMPLSOp, op *spb.AFTOperation, e *spb.AFTOperation_Mpls) *spb.AFTOperation {
	c.op.Id = op.Id
	c.op.NetworkInstance = op.NetworkInstance
	c.op.Op = op.Op
	switch l := e.Mpls.Label.(type) {
	case *aftpb.Afts_LabelEntryKey_LabelUint64:
		if l != nil {
			c.labelUint.LabelUint64 = l.LabelUint64
			c.key.Label = &c.labelUint
		}
	case *aftpb.Afts_LabelEntryKey_LabelOpenconfigmplstypesmplslabelenum:
		if l != nil {
			c.labelEnum.LabelOpenconfigmplstypesmplslabelenum = l.LabelOpenconfigmplstypesmplslabelenum
			c.key.Label = &c.labelEnum
		}
	}
	if src := e.Mpls.LabelEntry; src != nil {
		if src.EntryMetadata != nil {
			if b := src.EntryMetadata.Value; b != nil {
				if m := len(b); m <= len(c.metaBuf) {
					copy(c.metaBuf[:m], b)
					c.meta.Value = c.metaBuf[:m:m]
				} else {
					c.meta.Value = append([]byte(nil), b...)
				}
			}
			c.entry.EntryMetadata = &c.meta
		}
		if src.NextHopGroup != nil {
			c.nhg.Value = src.NextHopGroup.Value
			c.entry.NextHopGroup = &c.nhg
		}
		if src.NextHopGroupNetworkInstance != nil {
			c.nhgNI.Value = src.NextHopGroupNetworkInstance.Value
			c.entry.NextHopGroupNetworkInstance = &c.nhgNI
		}
		if m := len(src.PoppedMplsLabelStack); m > 0 {
			var stack []*aftpb.Afts_LabelEntry_PoppedMplsLabelStackUnion
			var pool []aftpb.Afts_LabelEntry_PoppedMplsLabelStackUnion
			if m <= len(c.poppedPtrs) {
				stack = c.poppedPtrs[:m:m]
				pool = c.poppedVals[:m]
			} else {
				stack = make([]*aftpb.Afts_LabelEntry_PoppedMplsLabelStackUnion, m)
				pool = make([]aftpb.Afts_LabelEntry_PoppedMplsLabelStackUnion, m)
			}
			for i, u := range src.PoppedMplsLabelStack {
				if u != nil {
					pool[i].PoppedMplsLabelStackUint64 = u.PoppedMplsLabelStackUint64
					pool[i].PoppedMplsLabelStackOpenconfigmplstypesmplslabelenum = u.PoppedMplsLabelStackOpenconfigmplstypesmplslabelenum
					stack[i] = &pool[i]
				}
			}
			c.entry.PoppedMplsLabelStack = stack
		}
		c.key.LabelEntry = &c.entry
	}
	c.oneof.Mpls = &c.key
	c.op.Entry = &c.oneof
	return &c.op
}

// canFastCloneNHGOp reports whether op can be cloned using clonedNHGOp.
func canFastCloneNHGOp(op *spb.AFTOperation, e *spb.AFTOperation_NextHopGroup) bool {
	if op.ElectionId != nil || len(op.ProtoReflect().GetUnknown()) > 0 || e == nil || e.NextHopGroup == nil {
		return false
	}
	if len(e.NextHopGroup.ProtoReflect().GetUnknown()) > 0 {
		return false
	}
	if nhg := e.NextHopGroup.NextHopGroup; nhg != nil && len(nhg.ProtoReflect().GetUnknown()) > 0 {
		return false
	}
	return true
}

// fillClonedNHGOp populates c as a deep copy of op and e.
func fillClonedNHGOp(c *clonedNHGOp, op *spb.AFTOperation, e *spb.AFTOperation_NextHopGroup) *spb.AFTOperation {
	c.op.Id = op.Id
	c.op.NetworkInstance = op.NetworkInstance
	c.op.Op = op.Op
	c.key.Id = e.NextHopGroup.Id
	if src := e.NextHopGroup.NextHopGroup; src != nil {
		if src.BackupNextHopGroup != nil {
			c.backup.Value = src.BackupNextHopGroup.Value
			c.entry.BackupNextHopGroup = &c.backup
		}
		if src.Color != nil {
			c.color.Value = src.Color.Value
			c.entry.Color = &c.color
		}
		if m := len(src.NextHop); m > 0 {
			var nhs []*aftpb.Afts_NextHopGroup_NextHopKey
			var pool []clonedNHGOpNH
			if m <= len(c.nhPtrs) {
				nhs = c.nhPtrs[:m:m]
				pool = c.nhNodes[:m]
			} else {
				nhs = make([]*aftpb.Afts_NextHopGroup_NextHopKey, m)
				pool = make([]clonedNHGOpNH, m)
			}
			for i, nhk := range src.NextHop {
				if nhk != nil {
					n := &pool[i]
					n.key.Index = nhk.Index
					if nhk.NextHop != nil {
						if nhk.NextHop.Weight != nil {
							n.weight.Value = nhk.NextHop.Weight.Value
							n.entry.Weight = &n.weight
						}
						n.key.NextHop = &n.entry
					}
					nhs[i] = &n.key
				}
			}
			c.entry.NextHop = nhs
		}
		c.key.NextHopGroup = &c.entry
	}
	c.oneof.NextHopGroup = &c.key
	c.op.Entry = &c.oneof
	return &c.op
}

// canFastCloneNHOp reports whether op can be cloned using clonedNHOp.
func canFastCloneNHOp(op *spb.AFTOperation, e *spb.AFTOperation_NextHop) bool {
	if op.ElectionId != nil || len(op.ProtoReflect().GetUnknown()) > 0 || e == nil || e.NextHop == nil {
		return false
	}
	if len(e.NextHop.ProtoReflect().GetUnknown()) > 0 {
		return false
	}
	if nh := e.NextHop.NextHop; nh != nil {
		if nh.Gre != nil || nh.IpInIp != nil || len(nh.EncapHeader) > 0 || len(nh.ProtoReflect().GetUnknown()) > 0 {
			return false
		}
	}
	return true
}

// fillClonedNHOp populates c as a deep copy of op and e.
func fillClonedNHOp(c *clonedNHOp, op *spb.AFTOperation, e *spb.AFTOperation_NextHop) *spb.AFTOperation {
	c.op.Id = op.Id
	c.op.NetworkInstance = op.NetworkInstance
	c.op.Op = op.Op
	c.key.Index = e.NextHop.Index
	if src := e.NextHop.NextHop; src != nil {
		c.entry.DecapsulateHeader = src.DecapsulateHeader
		c.entry.EncapsulateHeader = src.EncapsulateHeader
		if src.IpAddress != nil {
			c.ip.Value = src.IpAddress.Value
			c.entry.IpAddress = &c.ip
		}
		if src.MacAddress != nil {
			c.mac.Value = src.MacAddress.Value
			c.entry.MacAddress = &c.mac
		}
		if src.NetworkInstance != nil {
			c.ni.Value = src.NetworkInstance.Value
			c.entry.NetworkInstance = &c.ni
		}
		if src.PopTopLabel != nil {
			c.pop.Value = src.PopTopLabel.Value
			c.entry.PopTopLabel = &c.pop
		}
		if src.TunnelSrcIpAddress != nil {
			c.tun.Value = src.TunnelSrcIpAddress.Value
			c.entry.TunnelSrcIpAddress = &c.tun
		}
		if src.VniLabel != nil {
			c.vni.Value = src.VniLabel.Value
			c.entry.VniLabel = &c.vni
		}
		if src.InterfaceRef != nil {
			if src.InterfaceRef.Interface != nil {
				c.iface.Value = src.InterfaceRef.Interface.Value
				c.iref.Interface = &c.iface
			}
			if src.InterfaceRef.Subinterface != nil {
				c.sub.Value = src.InterfaceRef.Subinterface.Value
				c.iref.Subinterface = &c.sub
			}
			c.entry.InterfaceRef = &c.iref
		}
		if m := len(src.PushedMplsLabelStack); m > 0 {
			var stack []*aftpb.Afts_NextHop_PushedMplsLabelStackUnion
			var pool []aftpb.Afts_NextHop_PushedMplsLabelStackUnion
			if m <= len(c.pushedPtrs) {
				stack = c.pushedPtrs[:m:m]
				pool = c.pushedVals[:m]
			} else {
				stack = make([]*aftpb.Afts_NextHop_PushedMplsLabelStackUnion, m)
				pool = make([]aftpb.Afts_NextHop_PushedMplsLabelStackUnion, m)
			}
			for i, u := range src.PushedMplsLabelStack {
				if u != nil {
					pool[i].PushedMplsLabelStackUint64 = u.PushedMplsLabelStackUint64
					pool[i].PushedMplsLabelStackOpenconfigmplstypesmplslabelenum = u.PushedMplsLabelStackOpenconfigmplstypesmplslabelenum
					stack[i] = &pool[i]
				}
			}
			c.entry.PushedMplsLabelStack = stack
		}
		c.key.NextHop = &c.entry
	}
	c.oneof.NextHop = &c.key
	c.op.Entry = &c.oneof
	return &c.op
}

// CloneAFTOpSlice returns a deep copy of ops using contiguous backing slices,
// invoking fallback for uncommon or unknown-field protobuf messages.
func CloneAFTOpSlice(ops []*spb.AFTOperation, fallback func(*spb.AFTOperation) *spb.AFTOperation) []*spb.AFTOperation {
	if len(ops) == 0 {
		return nil
	}
	var nBare, nV4, nV6, nMPLS, nNHG, nNH int
	for _, op := range ops {
		if op == nil {
			continue
		}
		switch e := op.Entry.(type) {
		case nil:
			if op.ElectionId == nil && len(op.ProtoReflect().GetUnknown()) == 0 {
				nBare++
			}
		case *spb.AFTOperation_Ipv4:
			if canFastCloneIPv4Op(op, e) {
				nV4++
			}
		case *spb.AFTOperation_Ipv6:
			if canFastCloneIPv6Op(op, e) {
				nV6++
			}
		case *spb.AFTOperation_Mpls:
			if canFastCloneMPLSOp(op, e) {
				nMPLS++
			}
		case *spb.AFTOperation_NextHopGroup:
			if canFastCloneNHGOp(op, e) {
				nNHG++
			}
		case *spb.AFTOperation_NextHop:
			if canFastCloneNHOp(op, e) {
				nNH++
			}
		}
	}

	dst := make([]*spb.AFTOperation, len(ops))
	var barePool []spb.AFTOperation
	if nBare > 0 {
		barePool = make([]spb.AFTOperation, nBare)
	}
	var v4Pool []clonedIPv4Op
	if nV4 > 0 {
		v4Pool = make([]clonedIPv4Op, nV4)
	}
	var v6Pool []clonedIPv6Op
	if nV6 > 0 {
		v6Pool = make([]clonedIPv6Op, nV6)
	}
	var mplsPool []clonedMPLSOp
	if nMPLS > 0 {
		mplsPool = make([]clonedMPLSOp, nMPLS)
	}
	var nhgPool []clonedNHGOp
	if nNHG > 0 {
		nhgPool = make([]clonedNHGOp, nNHG)
	}
	var nhPool []clonedNHOp
	if nNH > 0 {
		nhPool = make([]clonedNHOp, nNH)
	}

	var iBare, iV4, iV6, iMPLS, iNHG, iNH int
	for i, op := range ops {
		if op == nil {
			continue
		}
		switch e := op.Entry.(type) {
		case nil:
			if op.ElectionId == nil && len(op.ProtoReflect().GetUnknown()) == 0 {
				cop := &barePool[iBare]
				iBare++
				cop.Id = op.Id
				cop.NetworkInstance = op.NetworkInstance
				cop.Op = op.Op
				dst[i] = cop
			} else {
				dst[i] = fallback(op)
			}
		case *spb.AFTOperation_Ipv4:
			if canFastCloneIPv4Op(op, e) {
				dst[i] = fillClonedIPv4Op(&v4Pool[iV4], op, e)
				iV4++
			} else {
				dst[i] = fallback(op)
			}
		case *spb.AFTOperation_Ipv6:
			if canFastCloneIPv6Op(op, e) {
				dst[i] = fillClonedIPv6Op(&v6Pool[iV6], op, e)
				iV6++
			} else {
				dst[i] = fallback(op)
			}
		case *spb.AFTOperation_Mpls:
			if canFastCloneMPLSOp(op, e) {
				dst[i] = fillClonedMPLSOp(&mplsPool[iMPLS], op, e)
				iMPLS++
			} else {
				dst[i] = fallback(op)
			}
		case *spb.AFTOperation_NextHopGroup:
			if canFastCloneNHGOp(op, e) {
				dst[i] = fillClonedNHGOp(&nhgPool[iNHG], op, e)
				iNHG++
			} else {
				dst[i] = fallback(op)
			}
		case *spb.AFTOperation_NextHop:
			if canFastCloneNHOp(op, e) {
				dst[i] = fillClonedNHOp(&nhPool[iNH], op, e)
				iNH++
			} else {
				dst[i] = fallback(op)
			}
		default:
			dst[i] = fallback(op)
		}
	}
	return dst
}

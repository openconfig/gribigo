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

// Package chk implements checks against the gRIBI client return values, it can be
// used to determine whether there are expected results within a specific set of return
// values.
//
// Package chk relies on the testing package, and therefore is a test only package -
// that should be used as a helper to tets that are executed by 'go test'.
package chk

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	"github.com/openconfig/gribigo/client"
	"github.com/openconfig/gribigo/fluent"

	gspb "google.golang.org/genproto/googleapis/rpc/status"
	spb "github.com/openconfig/gribi/v1/proto/service"
)

// resultOpt is an interface implemented by all options that can be
// handed to HasResult.
type resultOpt interface {
	isHasResultOpt()
}

// ignoreOpID is an option that specifies that the operation ID
// in the OpResult message should be ignored.
type ignoreOpID struct{}

// isHasResultOpt implements the resultOpt interface.
func (*ignoreOpID) isHasResultOpt() {}

// IgnoreOperationID specifies that the comparison of OpResult structs
// should ignore the OperationID field. It can be used to match the
// occurrence of a transaction related to a particular prefix, NHG, or NH
// without caring about the order that the transaction was sent to the
// server.
func IgnoreOperationID() *ignoreOpID {
	return &ignoreOpID{}
}

// hasIgnoreOperationID checks whether the supplied resultOpt slice contains
// the IgnoreOperationID option.
func hasIgnoreOperationID(opt []resultOpt) bool {
	for _, v := range opt {
		if _, ok := v.(*ignoreOpID); ok {
			return true
		}
	}
	return false
}

// includeServerError is an option that specifies that the server
// error message field should be compared in an OpResult.
type includeServerError struct{}

// isHasResultOpt implements the resultOpt interface.
func (*includeServerError) isHasResultOpt() {}

// IncludeServerError specifies that the comparison of OpResult structs
// should include the ServerError field, which includes a text error
// that the server supplied. Typically, such comparisons should not be
// used since they are likely to be implementation specific.
func IncludeServerError() *includeServerError {
	return &includeServerError{}
}

// hasIncludeServerError checks whether the supplied resultOpt slide contains
// the IncludeServerError option.
func hasIncludeServerError(opt []resultOpt) bool {
	for _, v := range opt {
		if _, ok := v.(*includeServerError); ok {
			return true
		}
	}
	return false
}

// HasResult checks whether the specified res slice contains a result containing
// with the value of want.
func HasResult(t testing.TB, res []*client.OpResult, want *client.OpResult, opt ...resultOpt) {
	t.Helper()
	var found bool

	ignoreFields := []string{"Timestamp", "Latency"}
	// If the library upstream of us didn't ask for any details to be compared,
	// then we just ignore that field.
	if want.Details == nil {
		ignoreFields = append(ignoreFields, "Details")
	}
	if hasIgnoreOperationID(opt) {
		ignoreFields = append(ignoreFields, "OperationID")
	}
	if !hasIncludeServerError(opt) {
		ignoreFields = append(ignoreFields, "ServerError")
	}

	opts := []cmp.Option{
		cmpopts.IgnoreFields(client.OpResult{}, ignoreFields...),
		protocmp.Transform(),
	}

	for _, r := range res {
		if cmp.Equal(r, want, opts...) {
			found = true
		}
	}
	if !found {
		buf := &bytes.Buffer{}
		buf.WriteString(fmt.Sprintf("results did not contain a result of value %s\n", want))
		buf.WriteString("got:\n")
		for _, r := range res {
			buf.WriteString(fmt.Sprintf("\t%s\n", r))
		}
		t.Fatal(buf.String())
	}
}

// HasResultsCache implements an efficient mechanism to call HasResults across
// a large set of operations results. HasResultsCache checks whether each result
// in wants is present in res, using the options specified.
func HasResultsCache(t testing.TB, res, wants []*client.OpResult, opt ...resultOpt) {
	t.Helper()

	byOpID := map[uint64]*client.OpResult{}
	byNHID := map[uint64]*client.OpResult{}
	byNHGID := map[uint64]*client.OpResult{}
	byIPv4Prefix := map[string]*client.OpResult{}

	for _, r := range res {
		byOpID[r.OperationID] = r
		if r.Details != nil {
			switch {
			case r.Details.NextHopGroupID != 0:
				byNHGID[r.Details.NextHopGroupID] = r
			case r.Details.NextHopIndex != 0:
				byNHID[r.Details.NextHopIndex] = r
			case r.Details.IPv4Prefix != "":
				byIPv4Prefix[r.Details.IPv4Prefix] = r
			}
		}
	}

	if !hasIgnoreOperationID(opt) {
		for _, want := range wants {
			HasResult(t, []*client.OpResult{byOpID[want.OperationID]}, want, opt...)
		}
		return
	}

	for _, want := range wants {
		// If we get a want that has no 'details' field, but we've been asked to check the
		// details, then this is an error on the test author's part.
		if want.Details == nil {
			t.Fatalf("test error: cannot check for wanted message %v with nil details when IgnoreOperationID is specified", want)
		}
		switch {
		case want.Details.NextHopGroupID != 0:
			HasResult(t, []*client.OpResult{byNHGID[want.Details.NextHopGroupID]}, want, opt...)
		case want.Details.NextHopIndex != 0:
			HasResult(t, []*client.OpResult{byNHID[want.Details.NextHopIndex]}, want, opt...)
		case want.Details.IPv4Prefix != "":
			HasResult(t, []*client.OpResult{byIPv4Prefix[want.Details.IPv4Prefix]}, want, opt...)
		}
	}
}

// clientError converts the given error into a client ClientErr.
func clientError(t testing.TB, err error) *client.ClientErr {
	t.Helper()
	ce, ok := err.(*client.ClientErr)
	if !ok {
		t.Fatalf("error returned from client was not expected type, got: %T, want: *client.ClientError", err)
	}
	return ce
}

// HasNSendErrors checks that the error contains N sender errors.
func HasNSendErrors(t testing.TB, err error, count int) {
	t.Helper()

	if err == nil && count == 0 {
		return
	}

	ce := clientError(t, err)
	if l := len(ce.Send); l != count {
		t.Fatalf("got unexpected number of send errors, got: %d (%v), want: %d", l, ce.Send, count)
	}
}

// HasNRecvErrors checks that the error contains N receive errors.
func HasNRecvErrors(t testing.TB, err error, count int) {
	t.Helper()

	if err == nil && count == 0 {
		return
	}

	ce := clientError(t, err)
	if l := len(ce.Recv); l != count {
		t.Fatalf("got unexpected number of receive errors, got: %d (%v), want: %d", l, ce.Recv, count)
	}
}

// ErrorOpt is an interface that is implemented by functions that examine errrors.
type ErrorOpt interface {
	isErrorOpt()
}

// allowUnimplemented is the internal representation of an ErrorOpt that allows for
// an error that is unimplemented or a specific error.
type allowUnimplemented struct{}

// isErrorOpt marks allowUnimplemented as an ErrorOpt.
func (*allowUnimplemented) isErrorOpt() {}

// AllowUnimplemented specifies that receive error with a particular status can be unimplemented
// OR the specified error type. It can be used to not return a fatal error when a server does
// not support a particular functionality. When AllowUnimplemented is specified, the details of
// the error are not checked.
func AllowUnimplemented() *allowUnimplemented {
	return &allowUnimplemented{}
}

// ignoreDetails is used to set DetailReason as nil in wanted modifyError
type ignoreDetails struct{}

// isErrorOpt marks ignoreDetails as an ErrorOpt.
func (*ignoreDetails) isErrorOpt() {}

// IgnoreDetails set Details as nil in modifyError
func IgnoreDetails() *ignoreDetails {
	return &ignoreDetails{}
}

// HasRecvClientErrorWithStatus checks whether the supplied ClientErr ce contains a status with
// the code and details set to the values supplied in want.
//
// TODO(robjs): Add unit test for this check.
func HasRecvClientErrorWithStatus(t testing.TB, err error, want *status.Status, opts ...ErrorOpt) {
	t.Helper()

	okMsgs := []*status.Status{want}
	var (
		ignoreUnimplDets bool
		ignoreDets       bool
	)
	for _, o := range opts {
		if _, ok := o.(*allowUnimplemented); ok {
			okMsgs = append(okMsgs, status.FromProto(&gspb.Status{
				Code: int32(codes.Unimplemented),
			}))
			ignoreUnimplDets = true
		}
		if _, ok := o.(*ignoreDetails); ok {
			iDetails := proto.Clone(want.Proto()).(*gspb.Status)
			iDetails.Details = nil
			okMsgs = append(okMsgs, status.FromProto(iDetails))
			ignoreDets = true
		}
	}

	var found bool
	ce := clientError(t, err)
	for _, e := range ce.Recv {
		for _, wo := range okMsgs {
			s, ok := status.FromError(e)
			if !ok {
				continue
			}
			ns := s.Proto()

			if wo.Message() == "" {
				ns.Message = "" // blank out message so that we don't compare it.
			}

			if ignoreUnimplDets && wo.Code() == codes.Unimplemented || ignoreDets {
				ns.Details = nil
			}

			if proto.Equal(ns, wo.Proto()) {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("client does not have receive error with status %s, got: %v", want.Proto(), ce.Recv)
	}
}

// GetResponseHasEntries checks whether the supplied GetResponse has the gRIBI
// entry described by the specified want within it. It calls t.Fatalf if no
// such entry is found.
func GetResponseHasEntries(t testing.TB, getres *spb.GetResponse, wants ...fluent.GRIBIEntry) {
	// proto.Equal tends to be expensive, so start with building a cache
	// so that we do not loop each time. We have to do this by network
	// instance, because each NI has its own namespace for each included
	// value.

	type cache struct {
		ipv4 map[string]*spb.AFTEntry
		nhg  map[uint64]*spb.AFTEntry
		nh   map[uint64]*spb.AFTEntry
	}

	netinsts := map[string]*cache{}

	for _, r := range getres.GetEntry() {
		if _, ok := netinsts[r.NetworkInstance]; !ok {
			netinsts[r.NetworkInstance] = &cache{
				ipv4: make(map[string]*spb.AFTEntry),
				nhg:  make(map[uint64]*spb.AFTEntry),
				nh:   make(map[uint64]*spb.AFTEntry),
			}
		}
		ni := netinsts[r.NetworkInstance]

		switch v := r.Entry.(type) {
		case *spb.AFTEntry_NextHopGroup:
			if id := v.NextHopGroup.GetId(); id != 0 {
				ni.nhg[id] = r
			}
		case *spb.AFTEntry_NextHop:
			if idx := v.NextHop.GetIndex(); idx != 0 {
				ni.nh[idx] = r
			}
		case *spb.AFTEntry_Ipv4:
			if pfx := v.Ipv4.GetPrefix(); pfx != "" {
				ni.ipv4[pfx] = r
			}
		}
	}

	for _, want := range wants {
		wantProto, err := want.EntryProto()
		if err != nil {
			t.Fatalf("cannot convert want to an AFTEntry protobuf, %v", err)
		}

		if wantProto.GetNetworkInstance() == "" {
			t.Fatalf("got nil network instance, required.")
		}

		if wantProto.GetEntry() == nil {
			t.Fatalf("got nil entry, required")
		}

		ni, ok := netinsts[wantProto.GetNetworkInstance()]
		if !ok {
			t.Fatalf("did not find entry, got: %s, did not find network instance in want: %s", wantProto.NetworkInstance, getres)
		}

		switch v := wantProto.Entry.(type) {
		case *spb.AFTEntry_NextHopGroup:
			if nhg, ok := ni.nhg[v.NextHopGroup.GetId()]; !ok {
				t.Fatalf("did not find entry, did not find nexthop group: %s, got:\n%s", v.NextHopGroup, getres)
			} else {
				programmedStatusMatch(t, nhg, wantProto)
			}
		case *spb.AFTEntry_NextHop:
			if nh, ok := ni.nh[v.NextHop.GetIndex()]; !ok {
				t.Fatalf("did not find entry, did not find nexthop: %s, got:\n%s", v.NextHop, getres)
			} else {
				programmedStatusMatch(t, nh, wantProto)
			}
		case *spb.AFTEntry_Ipv4:
			if ipv4, ok := ni.ipv4[v.Ipv4.GetPrefix()]; !ok {
				t.Fatalf("did not find entry, did not find ipv4: %s, got: %s\n", v.Ipv4, getres)
			} else {
				programmedStatusMatch(t, ipv4, wantProto)
			}
		}
	}
}

// programmedStatusMatch checks if the response's AFT entry FIB status matches the expected status.
// It skips the check if the expected status is UNAVAILABLE (default value).
func programmedStatusMatch(t testing.TB, resp *spb.AFTEntry, want *spb.AFTEntry) {
	if want.GetFibStatus() != spb.AFTEntry_UNAVAILABLE && resp.GetFibStatus() != want.GetFibStatus() {
		t.Fatalf("FIB status mismatch: %s, got: %s\n", want.GetFibStatus(), resp.GetFibStatus())
	}
}

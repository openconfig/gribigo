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
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/openconfig/gribigo/client"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
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
		t.Fatalf("results did not contain a result of value %s, got: %v", want, res)
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
	ce := clientError(t, err)
	if l := len(ce.Send); l != count {
		t.Fatalf("got unexpected number of send errors, got: %d (%v), want: %d", l, ce.Send, count)
	}
}

// HasNRecvErrors checks that the error contains N receive errors.
func HasNRecvErrors(t testing.TB, err error, count int) {
	t.Helper()
	ce := clientError(t, err)
	if l := len(ce.Recv); l != count {
		t.Fatalf("got unexpected number of receive errors, got: %d (%v), want: %d", l, ce.Recv, count)
	}
}

// HasRecvClientErrorWithStatus checks whether the supplied ClientErr ce contains a status with
// the code and details set to the values supplied in want.
func HasRecvClientErrorWithStatus(t testing.TB, err error, want *status.Status) {
	t.Helper()

	var found bool
	ce := clientError(t, err)
	for _, e := range ce.Recv {
		s, ok := status.FromError(e)
		if !ok {
			continue
		}
		ns := s.Proto()
		ns.Message = "" // blank out message so that we don't compare it.
		if proto.Equal(ns, want.Proto()) {
			found = true
		}
	}
	if !found {
		t.Fatalf("client does not have receive error with status %s, got: %v", want.Proto(), ce.Recv)
	}
}

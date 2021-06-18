package chk

import (
	"strings"
	"testing"

	"github.com/openconfig/gribigo/client"
	"github.com/openconfig/gribigo/constants"
	"github.com/openconfig/gribigo/fluent"
	"github.com/openconfig/gribigo/negtest"

	spb "github.com/openconfig/gribi/v1/proto/service"
)

func TestHasMessage(t *testing.T) {
	tests := []struct {
		desc           string
		inResults      []*client.OpResult
		inMsg          *client.OpResult
		expectFatalMsg string
	}{{
		desc: "election ID is present",
		inResults: []*client.OpResult{{
			CurrentServerElectionID: &spb.Uint128{
				High: 0,
				Low:  42,
			},
		}},
		inMsg: fluent.OperationResult().WithCurrentServerElectionID(42, 0).AsResult(),
	}, {
		desc:           "election ID is not present",
		inResults:      []*client.OpResult{},
		inMsg:          fluent.OperationResult().WithCurrentServerElectionID(1, 1).AsResult(),
		expectFatalMsg: "results did not contain a result of value",
	}, {
		desc: "successful session params",
		inResults: []*client.OpResult{{
			SessionParameters: &spb.SessionParametersResult{
				Status: spb.SessionParametersResult_OK,
			},
		}},
		inMsg: fluent.OperationResult().WithSuccessfulSessionParams().AsResult(),
	}, {
		desc: "with specific operation ID",
		inResults: []*client.OpResult{{
			OperationID: 42,
		}},
		inMsg: fluent.OperationResult().WithOperationID(42).AsResult(),
	}, {
		desc: "with specific operation ID, ignore details",
		inResults: []*client.OpResult{{
			OperationID: 42,
			Details: &client.OpDetailsResults{
				Type: constants.ADD,
			},
		}},
		inMsg: fluent.OperationResult().WithOperationID(42).AsResult(),
	}, {
		desc: "with specific operation ID, and details",
		inResults: []*client.OpResult{{
			OperationID: 42,
			Details: &client.OpDetailsResults{
				Type: constants.ADD,
			},
		}},
		inMsg: fluent.OperationResult().
			WithOperationID(42).
			WithOperationType(constants.ADD).
			AsResult(),
	}, {
		desc: "check for ipv4 prefix",
		inResults: []*client.OpResult{{
			OperationID: 42,
			Details: &client.OpDetailsResults{
				Type:       constants.ADD,
				IPv4Prefix: "1.1.1.1/32",
			},
		}},
		inMsg: fluent.OperationResult().
			WithOperationID(42).
			WithIPv4Operation("1.1.1.1/32").
			WithOperationType(constants.ADD).
			AsResult(),
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			if tt.expectFatalMsg != "" {
				got := negtest.ExpectFatal(t, func(t testing.TB) {
					HasResult(t, tt.inResults, tt.inMsg)
				})
				if !strings.Contains(got, tt.expectFatalMsg) {
					t.Fatalf("did not get expected fatal message, but test called Fatal, got: %s, want: %s", got, tt.expectFatalMsg)
				}
				return
			}
			HasResult(t, tt.inResults, tt.inMsg)
		})
	}
}

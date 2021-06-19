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

package compliance

import (
	"context"
	"strings"
	"testing"

	"github.com/openconfig/gribigo/device"
	"github.com/openconfig/gribigo/negtest"
	"github.com/openconfig/gribigo/testcommon"
)

func TestCompliance(t *testing.T) {
	for _, tt := range TestSuite {
		t.Run(tt.In.ShortName, func(t *testing.T) {
			creds, err := device.TLSCredsFromFile(testcommon.TLSCreds())
			if err != nil {
				t.Fatalf("cannot load credentials, got err: %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())

			defer cancel()
			d, err := device.New(ctx, creds)

			if err != nil {
				t.Fatalf("cannot start server, %v", err)
			}

			if tt.FatalMsg != "" {
				if got := negtest.ExpectFatal(t, func(t testing.TB) {
					tt.In.Fn(d.GRIBIAddr(), t)
				}); !strings.Contains(got, tt.FatalMsg) {
					t.Fatalf("did not get expected fatal error, got: %s, want: %s", got, tt.FatalMsg)
				}
				return
			}

			if tt.ErrorMsg != "" {
				if got := negtest.ExpectError(t, func(t testing.TB) {
					tt.In.Fn(d.GRIBIAddr(), t)
				}); !strings.Contains(got, tt.ErrorMsg) {
					t.Fatalf("did not get expected error, got: %s, want: %s", got, tt.ErrorMsg)
				}
			}

			// Any unexpected error will be caught by being called directly on t from the fluent library.
			tt.In.Fn(d.GRIBIAddr(), t)
		})
	}
}

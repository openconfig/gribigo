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
	"github.com/openconfig/gribigo/fluent"
	"github.com/openconfig/gribigo/ocrt"
	"github.com/openconfig/gribigo/server"
	"github.com/openconfig/gribigo/testcommon"
	"github.com/openconfig/testt"
	"github.com/openconfig/ygot/ygot"
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

			cfg := &ocrt.Device{}
			cfg.GetOrCreateNetworkInstance(server.DefaultNetworkInstanceName).Type = ocrt.NetworkInstanceTypes_NETWORK_INSTANCE_TYPE_DEFAULT_INSTANCE
			cfg.GetOrCreateNetworkInstance(vrfName).Type = ocrt.NetworkInstanceTypes_NETWORK_INSTANCE_TYPE_L3VRF
			jsonConfig, err := ygot.Marshal7951(cfg)
			if err != nil {
				t.Fatalf("cannot create configuration for device, error: %v", err)
			}

			d, err := device.New(ctx, creds, device.DeviceConfig(jsonConfig))
			if err != nil {
				t.Fatalf("cannot start server, %v", err)
			}

			c := fluent.NewClient()
			c.Connection().WithTarget(d.GRIBIAddr())

			sc := fluent.NewClient()
			sc.Connection().WithTarget(d.GRIBIAddr())
			opts := []TestOpt{
				SecondClient(sc),
			}

			if tt.FatalMsg != "" {
				if got := testt.ExpectFatal(t, func(t testing.TB) {
					tt.In.Fn(c, t, opts...)
				}); !strings.Contains(got, tt.FatalMsg) {
					t.Fatalf("did not get expected fatal error, got: %s, want: %s", got, tt.FatalMsg)
				}
				return
			}

			if tt.ErrorMsg != "" {
				if got := testt.ExpectError(t, func(t testing.TB) {
					tt.In.Fn(c, t, opts...)
				}); !strings.Contains(strings.Join(got, " "), tt.ErrorMsg) {
					t.Fatalf("did not get expected error, got: %s, want: %s", got, tt.ErrorMsg)
				}
			}

			// Any unexpected error will be caught by being called directly on t from the fluent library.
			tt.In.Fn(c, t, opts...)
		})
	}
}

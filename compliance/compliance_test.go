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
	"strings"
	"testing"

	"github.com/openconfig/gribigo/fluent"
	"github.com/openconfig/gribigo/server"
	"github.com/openconfig/gribigo/testcommon"
	"github.com/openconfig/lemming"
	"github.com/openconfig/lemming/gnmi/oc"
	"github.com/openconfig/testt"
	"github.com/openconfig/ygot/ygot"
)

func TestCompliance(t *testing.T) {
	for _, tt := range TestSuite {
		t.Run(tt.In.ShortName, func(t *testing.T) {
			cfg := &oc.Root{}
			cfg.GetOrCreateNetworkInstance(server.DefaultNetworkInstanceName).Type = oc.NetworkInstanceTypes_NETWORK_INSTANCE_TYPE_DEFAULT_INSTANCE
			cfg.GetOrCreateNetworkInstance(vrfName).Type = oc.NetworkInstanceTypes_NETWORK_INSTANCE_TYPE_L3VRF
			jsonConfig, err := ygot.Marshal7951(cfg)
			if err != nil {
				t.Fatalf("cannot create configuration for device, error: %v", err)
			}

			creds, err := lemming.WithTLSCredsFromFile(testcommon.TLSCreds())
			if err != nil {
				t.Fatalf("cannot load credentials, got err: %v", err)
			}

			lemmingOpts := []lemming.Option{creds, lemming.WithInitialConfig(jsonConfig), lemming.WithGNMIAddr(":0"), lemming.WithGRIBIAddr(":0")}

			if tt.In.RequiresDisallowedForwardReferences {
				lemmingOpts = append(lemmingOpts, lemming.WithGRIBIOpts(server.WithNoRIBForwardReferences()))
			}

			d, err := lemming.New("DUT", "", lemmingOpts...)
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

			c.Stop(t)
			sc.Stop(t)
			// TODO(robjs): Currnetly, lemming does not return nil errors when stopping successfully, check
			// error when upstream is fixed.
			d.Stop()
		})
	}
}

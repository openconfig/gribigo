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

package device

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"

	log "github.com/golang/glog"
	"github.com/google/go-cmp/cmp"
	"github.com/openconfig/gribigo/compliance"
	"github.com/openconfig/gribigo/ocrt"
	"github.com/openconfig/gribigo/sysrib"
	"github.com/openconfig/gribigo/testcommon"
	"github.com/openconfig/ygot/ygot"
)

type ribQuery struct {
	NetworkInstance string
	Prefix          *net.IPNet
}

func jsonDevice() []byte {
	d := &ocrt.Device{}
	d.GetOrCreateNetworkInstance("DEFAULT").Type = ocrt.NetworkInstanceTypes_NETWORK_INSTANCE_TYPE_DEFAULT_INSTANCE
	d.GetOrCreateInterface("eth0").GetOrCreateSubinterface(1).GetOrCreateIpv4().GetOrCreateAddress("192.0.2.1").PrefixLength = ygot.Uint8(31)

	j, err := ygot.Marshal7951(d, nil)
	if err != nil {
		panic(fmt.Sprintf("cannot create JSON, %v", err))
	}
	return j
}

func TestDevice(t *testing.T) {
	devCh := make(chan string, 1)
	errCh := make(chan error, 1)
	ribCh := make(chan *ribQuery, 1)
	ribErrCh := make(chan error)
	ribResultCh := make(chan []*sysrib.Interface, 1)

	creds, err := TLSCredsFromFile(testcommon.TLSCreds())
	if err != nil {
		t.Fatalf("cannot load TLS credentials, %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		d, err := New(ctx, creds, DeviceConfig(jsonDevice()))
		if err != nil {
			errCh <- err
		}
		devCh <- d.GRIBIAddr()

		for {
			select {
			case qryPfx := <-ribCh:
				ints, err := d.sysRIB.EgressInterface(qryPfx.NetworkInstance, qryPfx.Prefix)
				if err != nil {
					ribErrCh <- err
				}
				ribResultCh <- ints
			case <-ctx.Done():
				return
			}
		}
	}()
	select {
	case err := <-errCh:
		t.Fatalf("got unexpected error from device, got: %v", err)
	case addr := <-devCh:
		compliance.AddIPv4EntryRIBACK(addr, t)

		_, cidr, err := net.ParseCIDR("1.1.1.1/32")
		if err != nil {
			t.Fatalf("cannot parse CIDR for destination, err: %v", err)
		}

		ribCh <- &ribQuery{NetworkInstance: "DEFAULT", Prefix: cidr}
		select {
		case err := <-ribErrCh:
			t.Fatalf("cannot run RIB query, gotErr: %v", err)
		case got := <-ribResultCh:
			js, err := json.MarshalIndent(got, "", "  ")
			if err != nil {
				t.Fatalf("cannot marshal JSON response, %v", err)
			}
			log.Infof("got egress interface, %s", js)

			want := []*sysrib.Interface{{
				Name:         "eth0",
				Subinterface: 1,
			}}

			if diff := cmp.Diff(got, want); diff != "" {
				t.Fatalf("did not get expected egress interface, diff(-got,+want):\n%s", diff)
			}
		}
	}
}

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
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"

	log "github.com/golang/glog"
	"github.com/google/go-cmp/cmp"
	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/openconfig/gribigo/compliance"
	"github.com/openconfig/gribigo/fluent"
	"github.com/openconfig/gribigo/ocrt"
	"github.com/openconfig/gribigo/sysrib"
	"github.com/openconfig/gribigo/testcommon"
	"github.com/openconfig/ygot/ygot"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/encoding/prototext"

	wirepb "github.com/openconfig/gribigo/proto/wire"
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
		c := fluent.NewClient()
		c.Connection().WithTarget(addr)
		compliance.AddIPv4Entry(c, fluent.InstalledInRIB, t)

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

func TestWires(t *testing.T) {
	devCh := make(chan string, 1)
	portCh := make(chan []string, 1)
	errCh := make(chan error, 1)

	creds, err := TLSCredsFromFile(testcommon.TLSCreds())
	if err != nil {
		t.Fatalf("cannot load TLS credentials, %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		d, err := New(ctx, creds, DeviceConfig(jsonDevice()), EnableWires())
		if err != nil {
			errCh <- err
		}
		devCh <- d.GRIBIAddr()
		portCh <- d.PortAddrs()
	}()

	type wireconn struct {
		c    chan *wirepb.WireStream
		stop func()
	}

	dialOpts := []grpc.DialOption{grpc.WithBlock()}
	tlsc := credentials.NewTLS(&tls.Config{
		InsecureSkipVerify: true,
	})
	dialOpts = append(dialOpts, grpc.WithTransportCredentials(tlsc))

	portAddrs := <-portCh
	portConn := map[string]*wireconn{}
	for _, a := range portAddrs {
		ctx := context.Background()
		conn, err := grpc.DialContext(ctx, a, dialOpts...)
		if err != nil {
			t.Fatalf("could not dial wire server at %s, %v", a, err)
		}
		defer conn.Close()
		c := wirepb.NewWireServerClient(conn)

		tc := &wireconn{
			c: make(chan *wirepb.WireStream),
		}
		portConn[a] = tc

		go func() {
			stream, err := c.Stream(ctx)
			if err != nil {
				log.Errorf("cannot start stream, %v", errCh)
				return
			}
			for {
				m := <-tc.c
				if err := stream.Send(m); err != nil {
					log.Errorf("got error sending message, %s", prototext.Format(m))
					break
				}
			}
		}()
	}

	select {
	case err := <-errCh:
		t.Fatalf("got unexpected error from device, got: %v", err)
	case addr := <-devCh:
		c := fluent.NewClient()
		c.Connection().WithTarget(addr)
		compliance.AddIPv4Entry(c, fluent.InstalledInRIB, t)

		for id, p := range portConn {
			fmt.Printf("sending message to %s!\n", id)

			buf := gopacket.NewSerializeBuffer()
			opts := gopacket.SerializeOptions{
				FixLengths:       true,
				ComputeChecksums: true,
			}
			// TODO(robjs): don't discard errors
			src, _ := net.ParseMAC("82:a1:0b:c3:ca:fe")
			dst, _ := net.ParseMAC("82:a1:0b:c3:be:ef")
			gopacket.SerializeLayers(buf, opts,
				&layers.Ethernet{
					SrcMAC:       src,
					DstMAC:       dst,
					EthernetType: layers.EthernetTypeIPv4,
				},
				&layers.IPv4{
					SrcIP: net.ParseIP("1.1.1.1"),
					DstIP: net.ParseIP("192.0.2.1"),
				},
			)
			packetData := buf.Bytes()

			p.c <- &wirepb.WireStream{
				Message: &wirepb.WireStream_Data{
					Data: &wirepb.Data{
						Raw: packetData,
					},
				},
			}
			time.Sleep(2 * time.Second)
		}

	}
}

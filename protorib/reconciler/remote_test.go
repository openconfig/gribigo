// Copyright 2023 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package reconciler

import (
	"context"
	"crypto/tls"
	"net"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/openconfig/gribigo/protorib"
	"github.com/openconfig/gribigo/server"
	"github.com/openconfig/gribigo/testcommon"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	aftpb "github.com/openconfig/gribi/v1/proto/gribi_aft"
	spb "github.com/openconfig/gribi/v1/proto/service"
	wpb "github.com/openconfig/ygot/proto/ywrapper"
)

// newServer starts a TLS gRIBI server injected with r and returns its address and stop function.
func newServer(t *testing.T, r *protorib.RIB) (string, func()) {
	creds, err := credentials.NewServerTLSFromFile(testcommon.TLSCreds())
	if err != nil {
		t.Fatalf("cannot load TLS credentials, got err: %v", err)
	}
	srv := grpc.NewServer(grpc.Creds(creds))
	s, err := server.NewFake()
	if err != nil {
		t.Fatalf("cannot create server, got err: %v", err)
	}
	s.InjectProtoRIB(r)

	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("cannot create listener, got err: %v", err)
	}
	spb.RegisterGRIBIServer(srv, s)

	go srv.Serve(l)
	return l.Addr().String(), srv.Stop
}

func TestNewRemoteRIB(t *testing.T) {
	tests := []struct {
		desc           string
		inDefName      string
		inAddrOverride string
		wantErr        bool
	}{{
		desc:      "successful dial",
		inDefName: "DEFAULT",
	}, {
		desc:           "unsuccessful dial",
		inDefName:      "DEFAULT",
		inAddrOverride: "http\r://foo.com/",
		wantErr:        true,
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			addr, stop := newServer(t, nil)
			defer stop()

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()

			if tt.inAddrOverride != "" {
				addr = tt.inAddrOverride
			}

			rr, err := NewRemoteRIB(ctx, tt.inDefName, addr)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewRemoteRIB(ctx, %s, %s): did not get expected error, got: %v, wantErr? %v", tt.inDefName, addr, err, tt.wantErr)
			}
			if rr != nil {
				rr.CleanUp()
			}
		})
	}
}

func TestNewRemoteRIBWithStub(t *testing.T) {
	tests := []struct {
		desc      string
		inDefName string
		wantErr   bool
	}{{
		desc:      "successful dial",
		inDefName: "DEFAULT",
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			addr, stop := newServer(t, nil)
			defer stop()

			tlsc := credentials.NewTLS(&tls.Config{
				InsecureSkipVerify: true,
			})

			c, err := grpc.NewClient(addr,
				grpc.WithTransportCredentials(tlsc),
			)
			if err != nil {
				t.Fatalf("grpc.NewClient(%s): cannot dial server, %v", addr, err)
			}
			defer c.Close()

			if _, err := NewRemoteRIBWithStub(tt.inDefName, spb.NewGRIBIClient(c)); err != nil {
				t.Fatalf("NewRemoteRIBWithStub(...): cannot create client, %v", err)
			}
		})
	}
}

// badGRIBI is a stub gRIBI server that always returns Unimplemented on Get.
type badGRIBI struct {
	spb.UnimplementedGRIBIServer
}

// Get returns codes.Unimplemented.
func (b *badGRIBI) Get(_ *spb.GetRequest, _ spb.GRIBI_GetServer) error {
	return status.Errorf(codes.Unimplemented, "RPC unimplemented")
}

// newBadServer starts a TLS gRIBI server that fails all Get RPCs.
func newBadServer(t *testing.T, _ *protorib.RIB) (string, func()) {
	creds, err := credentials.NewServerTLSFromFile(testcommon.TLSCreds())
	if err != nil {
		t.Fatalf("cannot load TLS credentials, got err: %v", err)
	}
	srv := grpc.NewServer(grpc.Creds(creds))
	s := &badGRIBI{}

	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("cannot create listener, got err: %v", err)
	}
	spb.RegisterGRIBIServer(srv, s)

	go srv.Serve(l)
	return l.Addr().String(), srv.Stop
}

// hangingGRIBI is a stub gRIBI server that sleeps on Get until timeout.
type hangingGRIBI struct {
	spb.UnimplementedGRIBIServer
}

// Get sleeps for 600 seconds to simulate a hung RPC.
func (h *hangingGRIBI) Get(_ *spb.GetRequest, _ spb.GRIBI_GetServer) error {
	time.Sleep(600 * time.Second)
	return nil
}

// newHangingServer starts a TLS gRIBI server that hangs on Get RPCs.
func newHangingServer(t *testing.T, _ *protorib.RIB) (string, func()) {
	creds, err := credentials.NewServerTLSFromFile(testcommon.TLSCreds())
	if err != nil {
		t.Fatalf("cannot load TLS credentials, got err: %v", err)
	}
	srv := grpc.NewServer(grpc.Creds(creds))
	s := &hangingGRIBI{}

	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("cannot create listener, got err: %v", err)
	}
	spb.RegisterGRIBIServer(srv, s)

	go srv.Serve(l)
	return l.Addr().String(), srv.Stop
}

func TestGet(t *testing.T) {
	dn := "DEFAULT"
	tests := []struct {
		desc            string
		inDefName       string
		inServer        func(*testing.T, *protorib.RIB) (string, func())
		inInjectedRIB   *protorib.RIB
		wantRIBContents map[string]*protorib.AFTs
		wantErr         bool
	}{{
		desc:      "cannot get RIB",
		inDefName: "DEFAULT",
		inServer:  newBadServer,
		wantErr:   true,
	}, {
		desc:      "successfully got RIB",
		inDefName: dn,
		inServer:  newServer,
		inInjectedRIB: func() *protorib.RIB {
			r := protorib.NewFake(dn)
			if err := r.InjectNH(dn, 1, "int42"); err != nil {
				t.Fatalf("cannot add NH, %v", err)
			}
			if err := r.InjectNHG(dn, 1, map[uint64]uint64{1: 1}); err != nil {
				t.Fatalf("cannot add NHG, %v", err)
			}
			if err := r.InjectIPv4(dn, "1.0.0.0/24", 1); err != nil {
				t.Fatalf("cannot add IPv4, %v", err)
			}
			return r.RIB()
		}(),
		wantRIBContents: map[string]*protorib.AFTs{
			dn: {
				IPv4Entry: map[string]*aftpb.Afts_Ipv4EntryKey{
					"1.0.0.0/24": {
						Prefix: "1.0.0.0/24",
						Ipv4Entry: &aftpb.Afts_Ipv4Entry{
							NextHopGroup: &wpb.UintValue{Value: 1},
						},
					},
				},
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
				NextHop: map[uint64]*aftpb.Afts_NextHopKey{
					1: {
						Index: 1,
						NextHop: &aftpb.Afts_NextHop{
							InterfaceRef: &aftpb.Afts_NextHop_InterfaceRef{
								Interface: &wpb.StringValue{Value: "int42"},
							},
						},
					},
				},
			},
		},
	}, {
		desc:      "hanging gRIBI",
		inDefName: dn,
		inServer:  newHangingServer,
		wantErr:   true,
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			addr, stop := tt.inServer(t, tt.inInjectedRIB)
			defer stop()

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()

			rr, err := NewRemoteRIB(ctx, tt.inDefName, addr)
			if err != nil {
				t.Fatalf("NewRemoteRIB(_, %s, %s): cannot connect, got err: %v", tt.inDefName, addr, err)
			}
			defer rr.CleanUp()

			got, err := rr.Get(ctx)
			if (err != nil) != tt.wantErr {
				t.Fatalf("(*RemoteRIB).Get(ctx): did not get expected err, got: %v, wantErr? %v", err, tt.wantErr)
			}

			if err != nil {
				return
			}

			gotContents, err := got.RIBContents()
			if err != nil {
				t.Fatalf("(*RemoteRIB).Get(ctx).RIBContents(): cannot introspect RIB, err: %v", err)
			}

			if diff := cmp.Diff(gotContents, tt.wantRIBContents, protocmp.Transform(), cmpopts.EquateEmpty()); diff != "" {
				t.Fatalf("(*RemoteRIB).Get(ctx): did not get expected contents, diff(-got,+want):\n%s", diff)
			}
		})
	}
}

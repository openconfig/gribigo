package reconciler

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/openconfig/gribigo/aft"
	"github.com/openconfig/gribigo/rib"
	"github.com/openconfig/gribigo/server"
	"github.com/openconfig/gribigo/testcommon"
	"github.com/openconfig/ygot/ygot"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	spb "github.com/openconfig/gribi/v1/proto/service"
)

func newServer(t *testing.T, r *rib.RIB) (string, func()) {
	creds, err := credentials.NewServerTLSFromFile(testcommon.TLSCreds())
	if err != nil {
		t.Fatalf("cannot load TLS credentials, got err: %v", err)
	}
	srv := grpc.NewServer(grpc.Creds(creds))
	s, err := server.NewFake()
	if err != nil {
		t.Fatalf("cannot create server, got err: %v", err)
	}
	s.InjectRIB(r)

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
		inAddrOverride: "invalid.addr:999999",
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

			if _, err := NewRemoteRIB(ctx, tt.inDefName, addr); (err != nil) != tt.wantErr {
				t.Fatalf("NewRemoteRIB(ctx, %s, %s): did not get expected error, got: %v, wantErr? %v", tt.inDefName, addr, err, tt.wantErr)
			}
		})
	}
}

type badGRIBI struct {
	*spb.UnimplementedGRIBIServer
}

func (b *badGRIBI) Get(_ *spb.GetRequest, _ spb.GRIBI_GetServer) error {
	return status.Errorf(codes.Unimplemented, "RPC unimplemented")
}

func newBadServer(t *testing.T, r *rib.RIB) (string, func()) {
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

func TestGet(t *testing.T) {
	dn := "DEFAULT"
	tests := []struct {
		desc            string
		inDefName       string
		inServer        func(*testing.T, *rib.RIB) (string, func())
		inInjectedRIB   *rib.RIB
		wantRIBContents map[string]*aft.RIB
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
		inInjectedRIB: func() *rib.RIB {
			r := rib.NewFake(dn)
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
		wantRIBContents: map[string]*aft.RIB{
			dn: func() *aft.RIB {
				r := &aft.RIB{}
				a := r.GetOrCreateAfts()
				a.GetOrCreateIpv4Entry("1.0.0.0/24").NextHopGroup = ygot.Uint64(1)
				a.GetOrCreateNextHopGroup(1).GetOrCreateNextHop(1).Weight = ygot.Uint64(1)
				a.GetOrCreateNextHop(1).GetOrCreateInterfaceRef().Interface = ygot.String("int42")
				return r
			}(),
		},
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

			got, err := rr.Get(ctx)
			if (err != nil) != tt.wantErr {
				t.Fatalf("(*RemoteRIB).Get(ctx): did not get expected err, got: %v, wantErr? %v", err, tt.wantErr)
			}

			if err != nil {
				return
			}

			// We can't introspect the private RIB contents, so make a copy to introspect.
			gotContents, err := got.RIBContents()
			if err != nil {
				t.Fatalf("(*RemoteRIB).Get(ctx).RIBContents(): can't introspect RIB, err: %v", err)
			}

			if diff := cmp.Diff(gotContents, tt.wantRIBContents); diff != "" {
				t.Fatalf("(*RemoteRIB).Get(ctx): did not get expected contents, diff(-got,+want):\n%s", diff)
			}

		})
	}
}

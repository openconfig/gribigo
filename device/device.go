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
	"fmt"
	"net"

	"github.com/openconfig/gribigo/server"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	spb "github.com/openconfig/gribi/v1/proto/service"
)

// Device is a wrapper struct that contains a stub gRIBI server implementation.
type Device struct {
	// gribiAddr is the address that the server is listening on
	// for gRIBI.
	gribiAddr string
	// gribiSrv is the gRIBI server.
	gribiSrv *server.Server
}

const (
	// targetName is the name that the device has in gNMI.
	targetName string = "DUT"
)

// DevOpt is an interface that is implemented by options that can be handed to New()
// for the device.
type DevOpt interface {
	isDevOpt()
}

// gRIBIAddr is the internal implementation that specifies the port that gRIBI should
// listen on.
type gRIBIAddr struct {
	host string
	port int
}

// isDevOpt implements the DevOpt interface.
func (*gRIBIAddr) isDevOpt() {}

// GRIBIPort is a device option that specifies that the port that should be listened on
// is i.
func GRIBIPort(host string, i int) *gRIBIAddr {
	return &gRIBIAddr{host: host, port: i}
}

// tlsCreds returns TLS credentials that can be used for a device.
type tlsCreds struct {
	c credentials.TransportCredentials
}

// TLSCredsFromFile loads the credentials from the specified cert and key file
// and returns them such that they can be used for the gNMI and gRIBI servers.
func TLSCredsFromFile(certFile, keyFile string) (*tlsCreds, error) {
	t, err := credentials.NewServerTLSFromFile(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	return &tlsCreds{c: t}, nil
}

// IsDevOpt implements the DevOpt interface for tlsCreds.
func (*tlsCreds) isDevOpt() {}

// New returns a new device with the specific context. It returns the device, and
// an optional error. The servers can be stopped by cancelling the supplied context.
func New(ctx context.Context, opts ...DevOpt) (*Device, error) {
	d := &Device{}
	gr := optGRIBIAddr(opts)

	creds := optTLSCreds(opts)
	if creds == nil {
		return nil, fmt.Errorf("must specific TLS credentials to start a server")
	}

	gRIBIStop, err := d.startgRIBI(ctx, gr.host, gr.port, creds)
	if err != nil {
		return nil, fmt.Errorf("cannot start gRIBI server, %v", err)
	}

	go func() {
		<-ctx.Done()
		gRIBIStop()
	}()

	return d, nil
}

// optGRIBIAddr finds the first occurrence of the GRIBIAddr option in opts.
// If no GRIBIAddr option is found, the default of localhost:0 is returned.
func optGRIBIAddr(opts []DevOpt) *gRIBIAddr {
	for _, o := range opts {
		if v, ok := o.(*gRIBIAddr); ok {
			return v
		}
	}
	return &gRIBIAddr{host: "localhost", port: 0}
}

// optTLSCreds finds the first occurrence of the tlsCreds option in opts.
func optTLSCreds(opts []DevOpt) *tlsCreds {
	for _, o := range opts {
		if v, ok := o.(*tlsCreds); ok {
			return v
		}
	}
	return nil
}

// Start gRIBI starts the gRIBI server on the device on the specified host:port
// and the specified TLS credentials, with the specified options.
// It returns a function to stop the server, and error if the server cannot be started.
func (d *Device) startgRIBI(ctx context.Context, host string, port int, creds *tlsCreds, opt ...server.ServerOpt) (func(), error) {
	l, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return nil, fmt.Errorf("cannot create gRPC server for gRIBI, %v", err)
	}

	s := grpc.NewServer(grpc.Creds(creds.c))
	ts, err := server.New(opt...)
	if err != nil {
		return nil, fmt.Errorf("cannot create gRIBI server, %v", err)
	}
	spb.RegisterGRIBIServer(s, ts)
	d.gribiAddr = l.Addr().String()
	d.gribiSrv = ts
	go s.Serve(l)
	return s.GracefulStop, nil
}

// GRIBIAddr returns the address that the gRIBI server is listening on.
func (d *Device) GRIBIAddr() string {
	return d.gribiAddr
}

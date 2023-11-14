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

// Binary stubrtr is a basic gRIBI stub server. Unlike the more fully featured
// device it does not have other dependencies outside of gRIBIgo, making it
// more suitable for direct gRIBI testing or debugging.
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"strings"

	log "github.com/golang/glog"
	"github.com/openconfig/gribigo/server"
	"github.com/openconfig/gribigo/testcommon"
	"google.golang.org/grpc"

	"net/http"
	_ "net/http/pprof"

	spb "github.com/openconfig/gribi/v1/proto/service"
)

var (
	certFile = flag.String("cert", "", "cert is the path to the server TLS certificate file")
	keyFile  = flag.String("key", "", "key is the path to the server TLS key file")
	addr     = flag.String("addr", ":9340", "gribi listen address")
	vrfs     = flag.String("vrfs", "NON-DEFAULT-VRF", "additional VRFs to initialise on the server")
)

func main() {
	flag.Parse()

	if *certFile == "" || *keyFile == "" {
		log.Exitf("must specify a TLS certificate and key file")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	creds, err := testcommon.TLSCredsFromFile(*certFile, *keyFile)
	if err != nil {
		log.Exitf("cannot initialise TLS, got: %v", err)
	}

	opts := []server.ServerOpt{}
	vrfList := strings.Split(*vrfs, ",")
	if len(vrfList) != 0 {
		opts = append(opts, server.WithVRFs(vrfList))
	}

	stop, err := startgRIBI(ctx, *addr, creds, opts...)
	defer stop()

	go func() {
		log.Infof("%v", http.ListenAndServe("localhost:6060", nil))
	}()
	<-ctx.Done()
}

func startgRIBI(ctx context.Context, addr string, creds *testcommon.TLSCred, opt ...server.ServerOpt) (func(), error) {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("cannot create gRPC server for gRIBI, %v", err)
	}

	s := grpc.NewServer(grpc.Creds(creds.C))
	ts, err := server.New(opt...)
	if err != nil {
		return nil, fmt.Errorf("cannot create gRIBI server, %v", err)
	}
	spb.RegisterGRIBIServer(s, ts)

	go s.Serve(l)
	log.Infof("listening on %s", l.Addr().String())
	return s.GracefulStop, nil
}

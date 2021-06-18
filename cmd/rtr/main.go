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

package main

import (
	"context"
	"flag"

	log "github.com/golang/glog"
	"github.com/openconfig/gribigo/device"

	"net/http"
	_ "net/http/pprof"
)

var (
	certFile = flag.String("cert", "", "cert is the path to the server TLS certificate file")
	keyFile  = flag.String("key", "", "key is the path to the server TLS key file")
)

func main() {
	flag.Parse()

	if *certFile == "" || *keyFile == "" {
		log.Exitf("must specify a TLS certificate and key file")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	creds, err := device.TLSCredsFromFile(*certFile, *keyFile)
	if err != nil {
		log.Exitf("cannot initialise TLS, got: %v", err)
	}
	d, err := device.New(ctx, creds)
	if err != nil {
		log.Exitf("cannot start device, %v", err)
	}
	log.Infof("listening on:\n\tgRIBI: %s\n\tgNMI: %s", d.GRIBIAddr(), d.GNMIAddr())
	go func() {
		log.Infof("%v", http.ListenAndServe("localhost:6060", nil))
	}()
	<-ctx.Done()
}

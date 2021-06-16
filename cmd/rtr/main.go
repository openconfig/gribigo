package main

import (
	"context"
	"flag"

	log "github.com/golang/glog"
	"github.com/openconfig/gribigo/device"
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

	ctx := context.Background()
	creds, err := device.TLSCredsFromFile(*certFile, *keyFile)
	if err != nil {
		log.Exitf("cannot initialise TLS, got: %v", err)
	}
	d, cancel, err := device.New(context.Background(), creds)
	defer cancel()
	if err != nil {
		log.Exitf("cannot start device, %v", err)
	}
	log.Infof("listening on:\n\tgRIBI: %s\n\tgNMI: %s", d.GRIBIAddr(), d.GNMIAddr())
	<-ctx.Done()
}

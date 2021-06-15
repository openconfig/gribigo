package main

import (
	"context"
	"flag"

	log "github.com/golang/glog"
	"github.com/openconfig/gribigo/device"
)

func main() {
	flag.Parse()
	ctx := context.Background()
	d, cancel, err := device.New(ctx)
	defer cancel()
	if err != nil {
		log.Exitf("cannot start device, %v", err)
	}
	log.Infof("listening on:\n\tgRIBI: %s\n\tgNMI: %s", d.GRIBIAddr(), d.GNMIAddr())
	<-ctx.Done()
}

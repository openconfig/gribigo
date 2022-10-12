package main

import (
	"context"
	"flag"
	"testing"
	"time"

	log "github.com/golang/glog"
	"github.com/openconfig/gribigo/device"
	"github.com/openconfig/gribigo/fluent"
	"github.com/openconfig/gribigo/ocrt"
	"github.com/openconfig/gribigo/replay"
	"github.com/openconfig/gribigo/server"
	"github.com/openconfig/gribigo/testcommon"
	"github.com/openconfig/ygot/ygot"
)

var (
	input = flag.String("in", "../testdata/5ms-10ops.txtpb", "file to replay messages from.")
)

func main() {
	flag.Parse()
	msgs, err := replay.FromFile(*input)
	if err != nil {
		log.Exitf("cannot read messages, %v", err)
	}

	ts, err := replay.Timeseries(msgs, 5*time.Millisecond)
	if err != nil {
		log.Exitf("cannot build timeseries, %v", err)
	}

	sched, err := replay.Schedule(ts)
	if err != nil {
		log.Exitf("cannot form schedule, %v", err)
	}

	creds, err := device.TLSCredsFromFile(testcommon.TLSCreds())
	if err != nil {
		log.Fatalf("cannot load credentials, got err: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())

	defer cancel()

	cfg := &ocrt.Device{}
	cfg.GetOrCreateNetworkInstance(server.DefaultNetworkInstanceName).Type = ocrt.NetworkInstanceTypes_NETWORK_INSTANCE_TYPE_DEFAULT_INSTANCE
	jsonConfig, err := ygot.Marshal7951(cfg)
	if err != nil {
		log.Exitf("cannot create configuration for device, error: %v", err)
	}

	d, err := device.New(ctx, creds, device.DeviceConfig(jsonConfig))
	if err != nil {
		log.Exitf("cannot start server, %v", err)
	}

	t := &testing.T{}

	c := fluent.NewClient()
	c.Connection().WithTarget(d.GRIBIAddr())
	replay.Do(ctx, t, c, sched, 100)
}

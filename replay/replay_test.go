package main

import (
	"context"
	"testing"

	"github.com/openconfig/gribigo/device"
	"github.com/openconfig/gribigo/fluent"
	"github.com/openconfig/gribigo/ocrt"
	"github.com/openconfig/gribigo/server"
	"github.com/openconfig/gribigo/testcommon"
	"github.com/openconfig/ygot/ygot"
)

func TestCreateReplay(t *testing.T) {
	creds, err := device.TLSCredsFromFile(testcommon.TLSCreds())
	if err != nil {
		t.Fatalf("cannot load credentials, got err: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())

	defer cancel()

	cfg := &ocrt.Device{}
	cfg.GetOrCreateNetworkInstance(server.DefaultNetworkInstanceName).Type = ocrt.NetworkInstanceTypes_NETWORK_INSTANCE_TYPE_DEFAULT_INSTANCE
	jsonConfig, err := ygot.Marshal7951(cfg)
	if err != nil {
		t.Fatalf("cannot create configuration for device, error: %v", err)
	}

	d, err := device.New(ctx, creds, device.DeviceConfig(jsonConfig))
	if err != nil {
		t.Fatalf("cannot start server, %v", err)
	}

	c := fluent.NewClient()
	c.Connection().WithTarget(d.GRIBIAddr())

}

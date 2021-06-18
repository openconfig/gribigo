package device

import (
	"context"
	"testing"

	"github.com/openconfig/gribigo/compliance"
	"github.com/openconfig/gribigo/testcommon"
)

func TestDevice(t *testing.T) {
	devCh := make(chan string, 1)
	errCh := make(chan error, 1)

	creds, err := TLSCredsFromFile(testcommon.TLSCreds())
	if err != nil {
		t.Fatalf("cannot load TLS credentials, %v", err)
	}
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		d, stop, err := New(ctx, creds)
		if err != nil {
			errCh <- err
		}
		devCh <- d.GRIBIAddr()
		<-ctx.Done()
		stop()
	}()
	select {
	case err := <-errCh:
		t.Fatalf("got unexpected error from device, got: %v", err)
	case addr := <-devCh:
		compliance.AddIPv4EntrySuccess(addr, t)
	}
}

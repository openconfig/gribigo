// Package gnmit is a single-target gNMI collector implementation that can be
// used as an on-device/fake device implementation. It supports the Subscribe RPC
// using the libraries from openconfig/gnmi.
package gnmit

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/openconfig/gnmi/cache"
	"github.com/openconfig/gnmi/subscribe"
	"google.golang.org/grpc"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
)

var (
	// metadataUpdatePeriod is the period of time after which the metadata for the collector
	// is updated to the client.
	metadataUpdatePeriod = time.Duration(30 * time.Second)
	// sizeUpdatePeriod is the period of time after which the storage size information for
	// the collector is updated to the client.
	sizeUpdatePeriod = time.Duration(30 * time.Second)
)

// periodic runs the function fn every period.
func periodic(period time.Duration, fn func()) {
	if period == 0 {
		return
	}
	t := time.NewTicker(period)
	defer t.Stop()
	for range t.C {
		fn()
	}
}

// New returns a new collector that listens on the specified addr (in the form host:port),
// supporting a single downstream target named hostname. sendMeta controls whether the
// metadata *other* than meta/sync and meta/connected is sent by the collector.
//
// New returns the new collector, the address it is listening on in the form hostname:port
// or any errors encounted whilst setting it up.
func New(ctx context.Context, addr string, hostname string, sendMeta bool, opts ...grpc.ServerOption) (*Collector, string, error) {
	c := &Collector{
		inCh: make(chan *gpb.SubscribeResponse),
		name: hostname,
	}

	srv := grpc.NewServer(opts...)
	c.cache = cache.New([]string{hostname})
	t := c.cache.GetTarget(hostname)

	if sendMeta {
		go periodic(metadataUpdatePeriod, c.cache.UpdateMetadata)
		go periodic(sizeUpdatePeriod, c.cache.UpdateSize)
	}
	t.Connect()

	// start our single collector from the input channel.
	go func() {
		for {
			select {
			case msg := <-c.inCh:
				if err := c.handleUpdate(msg); err != nil {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	subscribeSrv, err := subscribe.NewServer(c.cache)
	if err != nil {
		return nil, "", fmt.Errorf("could not instantiate gNMI server: %v", err)
	}
	gpb.RegisterGNMIServer(srv, subscribeSrv)
	// Forward streaming updates to clients.
	c.cache.SetClient(subscribeSrv.Update)
	// Register listening port and start serving.
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, "", fmt.Errorf("failed to listen: %v", err)
	}

	go srv.Serve(lis)
	c.stopFn = srv.Stop
	return c, lis.Addr().String(), nil
}

// Stop halts the running collector.
func (c *Collector) Stop() {
	c.stopFn()
}

// handleUpdate handles an input gNMI SubscribeResponse that is received by
// the target.
func (c *Collector) handleUpdate(resp *gpb.SubscribeResponse) error {
	t := c.cache.GetTarget(c.name)
	switch v := resp.Response.(type) {
	case *gpb.SubscribeResponse_Update:
		t.GnmiUpdate(v.Update)
	case *gpb.SubscribeResponse_SyncResponse:
		t.Sync()
	case *gpb.SubscribeResponse_Error:
		return fmt.Errorf("error in response: %s", v)
	default:
		return fmt.Errorf("unknown response %T: %s", v, v)
	}
	return nil
}

// Collector is a basic gNMI target that supports only the Subscribe
// RPC, and acts as a cache for exactly one target.
type Collector struct {
	cache *cache.Cache
	// name is the hostname of the client.
	name string
	// inCh is a channel use to write new SubscribeResponses to the client.
	inCh chan *gpb.SubscribeResponse
	// stopFn is the function used to stop the server.
	stopFn func()
}

// TargetUpdate provides an input gNMI SubscribeResponse to update the
// cache and clients with.
func (c *Collector) TargetUpdate(m *gpb.SubscribeResponse) {
	c.inCh <- m
}

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
	metadataUpdatePeriod = time.Duration(30 * time.Second)
	sizeUpdatePeriod     = time.Duration(30 * time.Second)
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

func New(ctx context.Context, port int, sendMeta bool) (*Collector, string, error) {
	c := &Collector{
		addr: fmt.Sprintf("localhost:%d", port),
		inCh: make(chan *gpb.SubscribeResponse),
	}

	srv := grpc.NewServer()
	c.cache = cache.New([]string{"local"})
	t := c.cache.GetTarget("local")

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
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, "", fmt.Errorf("failed to listen: %v", err)
	}

	go srv.Serve(lis)
	c.stopFn = srv.Stop
	return c, lis.Addr().String(), nil
}

func (c *Collector) Stop() {
	c.stopFn()
}

func (c *Collector) handleUpdate(resp *gpb.SubscribeResponse) error {
	t := c.cache.GetTarget("local")
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
	cache  *cache.Cache
	addr   string
	inCh   chan *gpb.SubscribeResponse
	stopFn func()
}

// TargetUpdate provides an input gNMI SubscribeResponse to update the
// cache and clients with.
func (c *Collector) TargetUpdate(m *gpb.SubscribeResponse) {
	c.inCh <- m
}

package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	log "github.com/golang/glog"
	"github.com/openconfig/gribigo/device"
	"github.com/openconfig/gribigo/fluent"
	"github.com/openconfig/gribigo/ocrt"
	"github.com/openconfig/gribigo/server"
	"github.com/openconfig/gribigo/testcommon"
	"github.com/openconfig/ygot/ygot"
	"go.uber.org/atomic"
	"google.golang.org/grpc/binarylog"
	"google.golang.org/protobuf/proto"

	spb "github.com/openconfig/gribi/v1/proto/service"
	pb "google.golang.org/grpc/binarylog/grpc_binarylog_v1"
)

var electionID = &atomic.Uint64{}

func init() {
	electionID.Store(1)

	binarylog.SetSink(testSink)
}

var testSink = &testBinLogSink{}

type testBinLogSink struct {
	mu  sync.Mutex
	buf []*pb.GrpcLogEntry
}

func (s *testBinLogSink) Write(e *pb.GrpcLogEntry) error {
	s.mu.Lock()
	s.buf = append(s.buf, e)
	s.mu.Unlock()
	return nil
}

func (s *testBinLogSink) Close() error { return nil }

// Returns all client entris if client is true, otherwise return all server
// entries.
func (s *testBinLogSink) logEntries(client bool) []*pb.GrpcLogEntry {
	logger := pb.GrpcLogEntry_LOGGER_SERVER
	if client {
		logger = pb.GrpcLogEntry_LOGGER_CLIENT
	}
	var ret []*pb.GrpcLogEntry
	s.mu.Lock()
	for _, e := range s.buf {
		if e.Logger == logger {
			ret = append(ret, e)
		}
	}
	s.mu.Unlock()
	return ret
}

func (s *testBinLogSink) clear() {
	s.mu.Lock()
	s.buf = nil
	s.mu.Unlock()
}

func awaitTimeout(ctx context.Context, c *fluent.GRIBIClient, t testing.TB, timeout time.Duration) error {
	subctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return c.Await(subctx, t)
}

func main() {
	flag.Parse()
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

	c.Connection().WithRedundancyMode(fluent.ElectedPrimaryClient).WithInitialElectionID(electionID.Load(), 0).WithPersistence()
	c.Start(ctx, t)
	defer c.Stop(t)
	c.StartSending(ctx, t)
	if err := awaitTimeout(ctx, c, t, time.Minute); err != nil {
		log.Fatalf("got unexpected error from server - session negotiation, got: %v, want: nil", err)
	}

	for i := 0; i < 2; i++ {
		c.Modify().AddEntry(t, fluent.NextHopEntry().
			WithNetworkInstance(server.DefaultNetworkInstanceName).
			WithIndex(1).
			WithIPAddress(fmt.Sprintf("2.2.2.%d", i)))
		time.Sleep(5 * time.Millisecond)

		c.Modify().AddEntry(t, fluent.NextHopGroupEntry().
			WithNetworkInstance(server.DefaultNetworkInstanceName).
			WithID(1).
			AddNextHop(1, 1))
		time.Sleep(5 * time.Millisecond)

		c.Modify().AddEntry(t, fluent.IPv4Entry().
			WithPrefix(fmt.Sprintf("1.1.1.%d/32", i)).
			WithNetworkInstance(server.DefaultNetworkInstanceName).
			WithNextHopGroup(1).
			WithNextHopGroupNetworkInstance(server.DefaultNetworkInstanceName))
		time.Sleep(5 * time.Millisecond)
	}

	if err := awaitTimeout(ctx, c, t, time.Minute); err != nil {
		t.Fatalf("got unexpected error from server - entries, got: %v, want: nil", err)
	}

	f, err := os.Create("log")
	if err != nil {
		log.Fatalf("can't create file, %v", err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	for n, e := range testSink.buf {
		if e.Logger != pb.GrpcLogEntry_LOGGER_CLIENT {
			// avoid duplicate messages since we are both client and server.
			continue
		}
		if _, err := w.Write([]byte(fmt.Sprintf("%s\n", e))); err != nil {
			log.Fatalf("cannot write message %d, %v", n, err)
		}
	}
	w.Flush()

	for n, e := range testSink.buf {
		if e.Logger != pb.GrpcLogEntry_LOGGER_CLIENT {
			// avoid duplicate messages since we are both client and server.
			continue
		}
		fmt.Printf("msg %d: %s\n", n, e)
		if e.Type == pb.GrpcLogEntry_EVENT_TYPE_CLIENT_MESSAGE {
			p := &spb.ModifyRequest{}
			if err := proto.Unmarshal(e.GetMessage().GetData(), p); err != nil {
				log.Errorf("cannot unmarshal client message %d, err: %v", err)
			}
			fmt.Printf("\t%s\n", p)
		}
	}

}

// Package fluent defines a fluent-style API for a gRIBI client that
// can be called from testing frameworks such as ONDATRA.
package fluent

import (
	"context"
	"flag"
	"testing"

	log "github.com/golang/glog"
	"github.com/openconfig/gribigo/client"

	spb "github.com/openconfig/gribi/v1/proto/service"
)

// gRIBIClient stores internal state and arguments related to the gRIBI client
// that is exposed by the fluent API.
type gRIBIClient struct {
	// connection stores the configuration related to the connection.
	connection *gRIBIConnection
	// Internal state.
	c *client.Client
}

type gRIBIConnection struct {
	// targetAddr stores the address that is to be dialed by the client.
	targetAddr string
	// redundMode specifies the redundancy mode that the client is using,
	// this is set only at initialisation time and cannot be changed during
	// the lifetime of the session.
	redundMode RedundancyMode
	// electionID specifies the initial election ID for the client.
	electionID *spb.Uint128
	// persist indicates whether the client requests that the server persists
	// entries after it disconnects.
	persist bool
}

// NewClient returns a new gRIBI client instance, and is an entrypoint to this
// package.
func NewClient() *gRIBIClient {
	return &gRIBIClient{}
}

// Connection allows any parameters relating to gRIBI connections to be set through
// the gRIBI fluent API.
func (g *gRIBIClient) Connection() *gRIBIConnection {
	c := &gRIBIConnection{}
	g.connection = c
	return c
}

// WithTarget specifies the gRIBI target (server) address that is to be dialed,
// the addr argument supplied is the address of the server specified in the standard
// form of address:port.
func (g *gRIBIConnection) WithTarget(addr string) *gRIBIConnection {
	g.targetAddr = addr
	return g
}

func (g *gRIBIConnection) WithPersistence() *gRIBIConnection {
	g.persist = true
	return g
}

// RedundancyMode is a type used to indicate the redundancy modes supported in gRIBI.
type RedundancyMode int64

const (
	_ RedundancyMode = iota
	// AllPrimaryClients indicates that all clients should be treated as a primary
	// and the server should do reference counting and bestpath selection between
	// them using the standard mechansisms defined in gRIBI.
	AllPrimaryClients
	// ElectedPrimaryClient indicates that this client is treated as part of an
	// elected set of clients that have an external election process that assigns
	// a uint128 election ID.
	ElectedPrimaryClient
)

// WithRedundancyMode specifies the redundancy mode that is to be used by the client
// from the enumerated types specified in the RedundancyMode.
func (g *gRIBIConnection) WithRedundancyMode(m RedundancyMode) *gRIBIConnection {
	g.redundMode = m
	return g
}

// WithInitialElectionID specifies the election ID that is to be used to start the
// connection. It is not sent until a Modify RPC has been opened to the client. The
// arguments specify the high and low 64-bit integers that from the uint128.
func (g *gRIBIConnection) WithInitialElectionID(low, high uint64) *gRIBIConnection {
	g.electionID = &spb.Uint128{
		Low:  low,
		High: high,
	}
	return g
}

// Start connects the gRIBI client to the target using the specified context as
// the connection parameters. The dial to the target is blocking, so Start() will
// not return until a connection is successfully made. Any error in parsing the
// specified arguments required for connections is raised using the supplied
// testing.TB.
func (g *gRIBIClient) Start(ctx context.Context, t testing.TB) {
	flag.Parse()
	t.Helper()
	if g.connection.targetAddr == "" {
		t.Fatalf("cannot dial without specifying target address")
	}

	opts := []client.Opt{}
	switch g.connection.redundMode {
	case AllPrimaryClients:
		opts = append(opts, client.AllPrimaryClients())
	case ElectedPrimaryClient:
		if g.connection.electionID == nil {
			t.Fatalf("client must specify Election ID in elected primary mode")
		}
		opts = append(opts, client.ElectedPrimaryClient(g.connection.electionID))
	}

	if g.connection.persist {
		opts = append(opts, client.PersistEntries())
	}

	log.V(2).Infof("setting client parameters to %+v", opts)
	c, err := client.New(opts...)
	if err != nil {
		t.Fatalf("cannot create new client, %v", err)
	}
	g.c = c

	log.V(2).Infof("dialing %s", g.connection.targetAddr)
	if err := c.Dial(ctx, g.connection.targetAddr); err != nil {
		t.Fatalf("cannot dial target, %v", err)
	}
}

// StartSending specifies that the Modify stream to the target should be made, and
// the client should start to send any queued messages to the target. Any error
// encountered is reported using the supplied testing.TB.
func (g *gRIBIClient) StartSending(ctx context.Context, t testing.TB) {
	t.Helper()
	if err := g.c.Connect(ctx); err != nil {
		t.Fatalf("cannot connect Modify request, %v", err)
	}
	g.c.StartSending()
}

// Await waits until the underlying gRIBI client has completed its work to return -
// complete is defined as both the send and pending queue being empty.
func (g *gRIBIClient) Await(ctx context.Context, t testing.TB) {
	g.c.AwaitConverged()
}

// Results returns the transaction results from the client. If the client is not converged
// it will return a partial set of results from transactions that have completed, otherwise
// it will return the complete set of results received from the server.
func (g *gRIBIClient) Results(t testing.TB) []*client.OpResult {
	r, err := g.c.Results()
	if err != nil {
		t.Fatalf("did not get valid results, %v", err)
	}
	return r
}

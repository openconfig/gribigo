// Package fluent defines a fluent-style API for a gRIBI client that
// can be called from testing frameworks such as ONDATRA.
package fluent

import (
	"context"
	"flag"
	"fmt"

	log "github.com/golang/glog"
	"github.com/openconfig/gribigo/client"

	spb "github.com/openconfig/gribi/v1/proto/service"
)

// gRIBIClient stores internal state and arguments related to the gRIBI client
// that is exposed by the fluent API.
type gRIBIClient struct {
	// targetAddr stores the address that is to be dialed by the client.
	targetAddr string
	// redundMode specifies the redundancy mode that the client is using,
	// this is set only at initialisation time and cannot be changed during
	// the lifetime of the session.
	redundMode RedundancyMode
	// electionID specifies the current election ID for the client, it must
	// be set before connecting in the case that the client is using an elected
	// master mode, and can be mutated during the lifetime of the client.
	electionID *spb.Uint128

	// Internal state.
	c *client.Client
}

// NewClient returns a new gRIBI client instance, and is an entrypoint to this
// package.
func NewClient() *gRIBIClient {
	return &gRIBIClient{}
}

// WithTarget specifies the gRIBI target (server) address that is to be dialed,
// the addr argument supplied is the address of the server specified in the standard
// form of address:port.
func (g *gRIBIClient) WithTarget(addr string) *gRIBIClient {
	g.targetAddr = addr
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
func (g *gRIBIClient) WithRedundancyMode(m RedundancyMode) *gRIBIClient {
	g.redundMode = m
	return g
}

// WithInitialElectionID specifies the election ID that is to be used to start the
// connection. It is not sent until a Modify RPC has been opened to the client. The
// arguments specify the high and low 64-bit integers that from the uint128.
func (g *gRIBIClient) WithInitialElectionID(low, high uint64) *gRIBIClient {
	g.electionID = &spb.Uint128{
		Low:  low,
		High: high,
	}
	return g
}

// Start connects the gRIBI client to the target using the specified context as
// the connection parameters. The dial to the target is blocking, so Start() will
// not return until a connection is successfully made. Any error in parsing the
// specified arguments required for connections will be returned to the caller.
func (g *gRIBIClient) Start(ctx context.Context) error {
	flag.Parse()
	if g.targetAddr == "" {
		return fmt.Errorf("cannot dial without specifying target address")
	}

	opts := []client.ClientOpt{}
	switch g.redundMode {
	case AllPrimaryClients:
		opts = append(opts, client.AllPrimaryClients())
	case ElectedPrimaryClient:
		if g.electionID == nil {
			return fmt.Errorf("client must specify Election ID in elected primary mode")
		}
		opts = append(opts, client.ElectedPrimaryClient(g.electionID))
	}

	log.V(2).Infof("setting client parameters to %v", opts)
	c, err := client.New(opts...)
	if err != nil {
		return fmt.Errorf("cannot create new client, %v", err)
	}
	g.c = c

	log.V(2).Infof("dialing %s", g.targetAddr)
	if err := c.Dial(ctx, g.targetAddr); err != nil {
		return fmt.Errorf("cannot dial target, %v", err)
	}

	return nil
}

// StartSending specifies that the Modify stream to the target should be made, and
// the client should start to send any queued messages to the target.
func (g *gRIBIClient) StartSending(ctx context.Context) error {
	if err := g.c.Connect(ctx); err != nil {
		return fmt.Errorf("cannot connect Modify request, %v", err)
	}
	g.c.StartSending()
	return nil
}

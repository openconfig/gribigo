// Package client defines a gRIBI stub client implementation in Go.
package client

import (
	"fmt"

	spb "github.com/openconfig/gribi/v1/proto/service"
	"google.golang.org/grpc"
)

// Client is a wrapper for the gRIBI client.
type Client struct {
	// state stores the configured state of the client, used to store
	// initial immutable parameters, as well as mutable parameters that
	// may change during the clients's lifetime.
	state *clientState

	// c is the current gRIBI client.
	c spb.GRIBIClient
}

type clientState struct {
	// sessParams stores the session parameters that are associated with
	// the client, they will be resent as the first message if the client
	// reconnects to the server.
	SessParams *spb.SessionParameters
	// electionID stores the current election ID for this client in the
	// case that the client participates in a group of SINGLE_PRIMARY
	// clients.
	ElectionID *spb.Uint128
}

// ClientOpt is an interface that is implemented for all options that
// can be supplied when creating a new client. This captures parameters
// that are sent at the start of a gRIBI session that
type ClientOpt interface {
	isClientOpt()
}

// NewClient creates a new gRIBI client with the specified set of options. The options
// provided control parameters that live for the lifetime of the client, such as those
// that are within the session parameters. A new client, or error, is returned.
func NewClient(opts ...ClientOpt) (*Client, error) {
	c := &Client{}
	s, err := handleParams(opts...)
	if err != nil {
		return nil, fmt.Errorf("cannot create client, error with parameters, %v", err)
	}
	c.state = s
	return c, nil
}

// handleParams takes the set of gRIBI client options that are provided and uses them
// to populate the session parameters that they are translated into. It returns a
// populate SessionParameters protobuf along with any errors when parsing the supplied
// set of options.
func handleParams(opts ...ClientOpt) (*clientState, error) {
	s := &clientState{
		SessParams: &spb.SessionParameters{},
	}
	for _, o := range opts {
		switch v := o.(type) {
		case *allPrimaryClients:
			if s.ElectionID != nil {
				return nil, fmt.Errorf("cannot specify both ALL_PRIMARY and SINGLE_PRIMARY redundancy modes")
			}
			s.SessParams.Redundancy = spb.SessionParameters_ALL_PRIMARY
		case *electedPrimaryClient:
			s.SessParams.Redundancy = spb.SessionParameters_SINGLE_PRIMARY
			s.ElectionID = v.electionID
		}
	}
	return s, nil
}

// DialOpt specifies options that can be used when dialing the gRIBI server
// specified by the client.
type DialOpt interface {
	isDialOpt()
}

// Connect dials the server specified in the addr string, using the specified
// set of dial options.
func (c *Client) Dial(addr string, opts ...DialOpt) error {
	// TODO(robjs): translate any options within the dial options here, we may
	// want to consider just accepting some gRPC dialoptions directly.
	conn, err := grpc.Dial(addr, grpc.WithInsecure())
	if err != nil {
		return fmt.Errorf("cannot dial remote system, %v", err)
	}
	c.c = spb.NewGRIBIClient(conn)
	return nil
}

// AllPrimaryClients is an option that is used when creating a new client that
// specifies that this client will be part of a group of clients that are all
// considered primary sources of routing information.
//
// In this mode of operation, the server is expected to store all entries that
// are written to it over gRIBI regardless of their source client. If multiple
// clients write the same entry, selection amongst them is done based on the
// lowest client identifier (the remote address when expressed as a 128-bit
// number) as per the explanation in
// https://github.com/openconfig/gribi/blob/master/doc/motivation.md#tying-injected-entries-to-the-liveliness-of-grpc-client
func AllPrimaryClients() *allPrimaryClients {
	return &allPrimaryClients{}
}

type allPrimaryClients struct{}

func (allPrimaryClients) isClientOpt() {}

// ElectedPrimaryClient is an option used when creating a new client that
// specifies that this client will be part of a group of clients elect a
// master amongst them, such that exactly one client is considered the primary
// source of routing information. The server is not expected to know any of
// the details of this election process - but arbitrates based on a supplied
// election ID (expressed as a 128-bit number). The client with the highest
// election ID is considered the primary and hence has active entries within
// the device's RIB.
//
// The initial election ID to be used is stored
func ElectedPrimaryClient(initialID *spb.Uint128) *electedPrimaryClient {
	return &electedPrimaryClient{electionID: initialID}
}

type electedPrimaryClient struct {
	electionID *spb.Uint128
}

func (electedPrimaryClient) isClientOpt() {}

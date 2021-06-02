// Package client defines a gRIBI stub client implementation in Go.
package client

import (
	"context"
	"fmt"
	"io"
	"sync"

	log "github.com/golang/glog"
	"google.golang.org/grpc"

	spb "github.com/openconfig/gribi/v1/proto/service"
)

// Client is a wrapper for the gRIBI client.
type Client struct {
	// state stores the configured state of the client, used to store
	// initial immutable parameters, as well as mutable parameters that
	// may change during the clients's lifetime.
	state *clientState

	// c is the current gRIBI client.
	c spb.GRIBIClient

	// qs is the set of queues that are associated with the current
	// client.
	qs *clientQs

	// run indicates that RPCs should continue to run, when set
	// to false, all goroutines that are serving RPCs shut down.
	run bool
	// sErr stores sending errors that cause termination of the sending
	// GoRoutine.
	sErr error
	// rErr stores receiving errors that cause termination of the receiving
	// GoRoutine.
	rErr error
}

// clientState is used to store the configured (immutable) state of the client.
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

// New creates a new gRIBI client with the specified set of options. The options
// provided control parameters that live for the lifetime of the client, such as those
// that are within the session parameters. A new client, or error, is returned.
func New(opts ...ClientOpt) (*Client, error) {
	c := &Client{}

	s, err := handleParams(opts...)
	if err != nil {
		return nil, fmt.Errorf("cannot create client, error with parameters, %v", err)
	}
	c.state = s

	c.qs = &clientQs{
		sendq: []*spb.ModifyRequest{},
		pendq: map[int]bool{},
		// Channels are blocking by default, but we want some ability to have
		// a backlog on the sender side, we'll panic if this buffer is completely
		// full, so we may need to handle congestion management in the future.
		modifyCh: make(chan *spb.ModifyRequest, 1000),
		resultq:  []*spb.ModifyResponse{},
	}

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
func (c *Client) Dial(ctx context.Context, addr string, opts ...DialOpt) error {
	// TODO(robjs): translate any options within the dial options here, we may
	// want to consider just accepting some gRPC dialoptions directly.
	conn, err := grpc.DialContext(ctx, addr, grpc.WithInsecure(), grpc.WithBlock())
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

// Connect establishes a Modify RPC to the client and sends the initial session
// parameters/election ID if required. The Modify RPC is stored within the client
// such that it can be used for subsequent calls - such that Connect must be called
// before subsequent operations that should send to the target.
//
// An error is returned if the Modify RPC cannot be established or there is an error
// response to the initial messages that are sent on the connection.
func (c *Client) Connect(ctx context.Context) error {
	if c.state.SessParams != nil {
		c.Q(&spb.ModifyRequest{
			Params: c.state.SessParams,
		})
	}

	if c.state.ElectionID != nil {
		c.Q(&spb.ModifyRequest{
			ElectionId: c.state.ElectionID,
		})
	}

	stream, err := c.c.Modify(ctx)
	if err != nil {
		return fmt.Errorf("cannot open Modify RPC, %v", err)
	}

	go func() {
		for {
			if !c.run {
				log.V(2).Infof("shutting down recv goroutine")
				return
			}
			in, err := stream.Recv()
			if err == io.EOF {
				// reading is done, so write should shut down too.
				c.run = false
				return
			}
			if err != nil {
				log.Errorf("got error receiving message, %v", err)
				c.rErr = err
				return
			}
			log.V(2).Infof("received message on Modify stream: %s", in)
		}
	}()

	go func() {
		for {
			if !c.run {
				log.V(2).Infof("shutting down send goroutine")
				return
			}
			// read from the channel
			m := <-c.qs.modifyCh
			if err := stream.Send(m); err != nil {
				log.V(2).Infof("sent Modify message %s", m)
			}
		}
	}()

	return nil
}

// clientQs defines the set of queues that are used for the Modify RPC.
type clientQs struct {
	// sendMu protects the send queue.
	sendMu sync.RWMutex
	// sendq contains any ModifyRequest messages that are queued to be
	// sent to the target. The send queue is emptied after the client is
	// instructed to start sending messages.
	sendq []*spb.ModifyRequest

	// pendMu protects the pending queue.
	pendMu sync.RWMutex
	// pendq stores the pending transactions to the target. Once a message
	// is taken from the sendq, and sent over the Modify stream, an entry
	// in the pendq is created for each AFTOperation within the ModifyRequest.
	// The key for the pendq is the ID of the operation to speed up lookups.
	// The value stored in the pendq is a bool - such that the library discards
	// the contents of a Modify request after it was sent.
	//
	// TODO(robjs): consider whether this should be map[int]*spb.ModifyRequest
	// since this would allow more introspection of /what/ the pending operations
	// are.
	pendq map[int]bool

	// modifyCh is the channel that is used to write to the goroutine that is
	// the sole source of writes onto the modify stream.
	modifyCh chan *spb.ModifyRequest

	// resultMu protects the results queue.
	resultMu sync.RWMutex
	// resultq stores the responses that are received from the target. When a
	// ModifyResponse is received from the target, the entry is removed from the
	// pendq (based on the ID) for any included AFTResult, and the result
	// is written to the result queue.
	resultq []*spb.ModifyResponse

	// sending indicates whether the client will empty the sendq. By default,
	// messages are queued into the sendq and not sent to the target.
	sending bool
}

// Q enqueues a ModifyRequest to be sent to the target.
func (c *Client) Q(m *spb.ModifyRequest) {
	if !c.qs.sending {
		c.qs.sendMu.Lock()
		defer c.qs.sendMu.Unlock()
		log.V(2).Infof("appended %s to sending queue", m)
		c.qs.sendq = append(c.qs.sendq, m)
		return
	}
	log.V(2).Infof("sending %s directly to queue", m)
	c.qs.modifyCh <- m
}

// StartSending toggles the client to begin sending messages that are in the send
// queue (enqued by Q) to the connection established by Connect.
func (c *Client) StartSending() {
	c.qs.sending = true

	// take the initial set of messages that were enqueued and queue them.
	c.qs.sendMu.Lock()
	defer c.qs.sendMu.Unlock()
	for _, m := range c.qs.sendq {
		log.V(2).Infof("sending %s to modify channel", m)
		c.qs.modifyCh <- m
	}
	c.qs.sendq = []*spb.ModifyRequest{}
}

// Pending returns the set of AFTOperations that are pending response from the
// target.
func (c *Client) Pending() []int {
	c.qs.pendMu.RLock()
	defer c.qs.pendMu.RUnlock()
	ret := []int{}
	for i := range c.qs.pendq {
		ret = append(ret, i)
	}
	return ret
}

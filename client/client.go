// Package client defines a gRIBI stub client implementation in Go.
package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	log "github.com/golang/glog"
	"google.golang.org/grpc"

	spb "github.com/openconfig/gribi/v1/proto/service"
)

var (
	unixTS = time.Now().UnixNano
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

	// shut indicates that RPCs should continue to run, when set
	// to true, all goroutines that are serving RPCs shut down.
	shut bool

	// sendErrMu protects the sendErr slice.
	sendErrMu sync.RWMutex
	// sErr stores sending errors that cause termination of the sending
	// GoRoutine.
	sendErr []error

	// readErrMu protects the readErr slice.
	readErrMu sync.RWMutex
	// rErr stores receiving errors that cause termination of the receiving
	// GoRoutine.
	readErr []error

	// userFns stores the set of functions that have been provided by the
	// client user as alternative implementations than the base client. This
	// allows a client to insert custom checks at particular points in the
	// lifecycle of a connection.
	userFns *userFunctions
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

type userFunctions struct {
	// postRecvFn is a function that is run after every message is received
	// after reading a message from the Modify stream from the server. It
	// takes no arguments and returns an error which is considered non-fatal
	// to the connection.
	modifyPostRecvFn func() error

	// TODO(robjs): consider the other callbacks that we want here, it is likely
	// that we also should have:
	// 		-  modifyRecvHandler -- replacing c.handleModifyResponse
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
		pendq: map[uint64]*PendingOp{},
		// Channels are blocking by default, but we want some ability to have
		// a backlog on the sender side, we'll panic if this buffer is completely
		// full, so we may need to handle congestion management in the future.
		modifyCh: make(chan *spb.ModifyRequest, 1000),
		resultq:  []*OpResult{},
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

	// TODO(robjs): if we made these functions not niladic, and
	// supplied to the client, then we could allow the user to
	// overload functions that read and write - and hence do
	// lower-layer operations to the gRIBI server directly.
	// Modify this code to do this (make these just be default
	// handler functions, they could still be inline).
	go func() {
		for {
			if c.shut {
				log.V(2).Infof("shutting down recv goroutine")
				return
			}
			in, err := stream.Recv()
			if err == io.EOF {
				// reading is done, so write should shut down too.
				c.shut = true
				return
			}
			if err != nil {
				log.Errorf("got error receiving message, %v", err)
				c.addReadErr(err)
				return
			}
			log.V(2).Infof("received message on Modify stream: %s", in)

			if err := c.handleModifyResponse(in); err != nil {
				log.Errorf("got error processing message received from server, %v", err)
				c.addReadErr(err)
			}
		}
	}()

	go func() {
		for {
			if c.shut {
				log.V(2).Infof("shutting down send goroutine")
				return
			}
			// read from the channel
			m := <-c.qs.modifyCh
			if err := stream.Send(m); err != nil {
				log.Errorf("got error sending message, %v", err)
				c.addSendErr(err)
				return
			}
			log.V(2).Infof("sent Modify message %s", m)

			if err := c.handleModifyRequest(m); err != nil {
				log.Errorf("got error processing message that was sent, %v", err)
				c.addSendErr(err)
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
	// TODO(robjs): currently, the implementation we have here just handles the
	// positive case that we get an acknowledged transaction - consider whether
	// we want the ability to have the client handle timeouts and inject these
	// as failed per the controller implementation.
	pendq map[uint64]*PendingOp

	// modifyCh is the channel that is used to write to the goroutine that is
	// the sole source of writes onto the modify stream.
	modifyCh chan *spb.ModifyRequest

	// resultMu protects the results queue.
	resultMu sync.RWMutex
	// resultq stores the responses that are received from the target. When a
	// ModifyResponse is received from the target, the entry is removed from the
	// pendq (based on the ID) for any included AFTResult, and the result
	// is written to the result queue.
	resultq []*OpResult

	// sending indicates whether the client will empty the sendq. By default,
	// messages are queued into the sendq and not sent to the target.
	sending bool
}

// PendingOp stores details pertaining to a pending request in the client.
type PendingOp struct {
	// Timestamp is the timestamp that the operation was queued at.
	Timestamp int64
	// Op is the operation that the pending request pertains to.
	Op *spb.AFTOperation
}

// OpResult stores details pertaining to a result that was received from
// the server.
type OpResult struct {
	// Timestamp is the timestamp that the result was received.
	Timestamp uint64
	// Latency is the latency of the request from the server. This is calculated
	// based on the time that the request was sent to the client (became pending)
	// and the time that the response was received from the server. It is expressed
	// in nanoseconds.
	Latency uint64

	// CurrentServerElectionID indicates that the message that was received from the server
	// was an ModifyResponse sent in response to an updated election ID, its value is the
	// current master election ID (maximum election ID seen from any client) reported from
	// the server.
	CurrentServerElectionID *spb.Uint128

	// OperationID indicates that the message that was received from the server was a
	// ModifyResponse sent in response to an AFTOperation, its value is the ID of the
	// operation to which it corresponds.
	OperationID uint64

	// TODO(robjs): add additional information about what the result was here.
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

// handleModifyRequest performs any required post-processing after having sent a
// ModifyRequest onto the gRPC channel to the server. Particularly, this ensures
// that pending transactions are enqueued into the pending queue where they have
// a specified ID.
func (c *Client) handleModifyRequest(m *spb.ModifyRequest) error {
	// Add any pending operations to the pending queue.
	for _, o := range m.Operation {
		if err := c.addPending(o); err != nil {
			return err
		}
	}
	return nil
}

// handleModifyResponse performs any required post-processing after having received
// a ModifyResponse from the gRPC channel from the server. Particularly, this
// ensures that pending transactions are dequeued into the results queue. An error
// is returned if the post processing is not possible.
func (c *Client) handleModifyResponse(m *spb.ModifyResponse) error {
	resPop := m.Result != nil
	elecPop := m.ElectionId != nil
	sessPop := m.SessionParamsResult != nil
	pop := 0
	for _, v := range []bool{resPop, elecPop, sessPop} {
		if v {
			pop++
		}
	}
	if pop == 0 || pop > 1 {
		return fmt.Errorf("invalid returned message, ElectionID, Result, and SessionParametersResult are mutually exclusive, got: %s", m)
	}

	if m.ElectionId != nil {
		// This is an update from the server in response to an updated master election ID.
		c.addResult(&OpResult{
			CurrentServerElectionID: m.ElectionId,
		})
		return nil
	}

	// TODO(robjs): Add handling of other response types.
	return nil
}

// addResult adds the operation o to the result queue of the client.
func (c *Client) addResult(o *OpResult) {
	c.qs.resultMu.Lock()
	defer c.qs.resultMu.Unlock()
	c.qs.resultq = append(c.qs.resultq, o)
}

// addPending adds the operation specified by op to the pending transaction queue
// on the client. This queue stores the operations that have been sent to the server
// but have not yet been reported on. It returns an error if the pending transaction
// cannot be added.
func (c *Client) addPending(op *spb.AFTOperation) error {
	c.qs.pendMu.Lock()
	defer c.qs.pendMu.Unlock()
	if c.qs.pendq[op.Id] != nil {
		return fmt.Errorf("could not enqueue operation %d, duplicate pending ID", op.Id)
	}
	c.qs.pendq[op.Id] = &PendingOp{
		Timestamp: unixTS(),
		Op:        op,
	}
	return nil
}

// addReadErr adds an error experienced when reading from the server to the readErr
// slice on the client.
func (c *Client) addReadErr(err error) {
	c.readErrMu.Lock()
	defer c.readErrMu.Unlock()
	c.readErr = append(c.readErr, err)
}

// addSendErr adds an error experienced when writing to the server to the sendErr
// slice on the client.
func (c *Client) addSendErr(err error) {
	c.sendErrMu.Lock()
	defer c.sendErrMu.Unlock()
	c.sendErr = append(c.sendErr, err)
}

// Pending returns the set of AFTOperations that are pending response from the
// target.
func (c *Client) Pending() (map[uint64]*PendingOp, error) {
	if c.qs == nil {
		return nil, errors.New("invalid (nil) queues in client")
	}
	c.qs.pendMu.RLock()
	defer c.qs.pendMu.RUnlock()
	ret := map[uint64]*PendingOp{}
	for _, o := range c.qs.pendq {
		if o.Op == nil {
			return nil, fmt.Errorf("invalid nil operation in pending queue, %v", o)
		}
		ret[o.Op.Id] = o
	}
	return ret, nil
}

// PendingLen returns the length of the pending queue.
func (c *Client) PendingLen() int {
	c.qs.pendMu.RLock()
	defer c.qs.pendingMu.RUnlock()
	return len(c.qs.pendq)
}

// SendLen returns the length of the send queue.
func (c *Client) SendLen() int {
	c.qs.sendMu.RLock()
	defer c.qs.sendMu.RUnlock()
	return len(c.qs.sendq)
}

// Results returns the set of ModifyResponses that have been received from the
// target.
func (c *Client) Results() ([]*OpResult, error) {
	if c.qs == nil {
		return nil, errors.New("invalid (nil) queues in client")
	}
	c.qs.resultMu.RLock()
	defer c.qs.resultMu.RUnlock()
	return append(make([]*OpResult, 0, len(c.qs.resultq)), c.qs.resultq...), nil
}

// ClientStatus is the overview status of the client, timestamped according to the
// time at which the state was retrieved.
type ClientStatus struct {
	// Timestamp expressed in nanoseconds since the epoch that the status was retrieved.
	Timestamp int64
	// PendingTransactions is a map keyed by the uint64 operation ID
	PendingTransactions map[uint64]*PendingOp
	Results             []*OpResult
	SendErrs            []error
	ReadErrs            []error
}

// Status returns a composite status of the client at the time that the caller specified.
func (c *Client) Status() (*ClientStatus, error) {
	pt, err := c.Pending()
	if err != nil {
		return nil, fmt.Errorf("invalid pending queue, %v", err)
	}
	r, err := c.Results()
	if err != nil {
		return nil, fmt.Errorf("invalid results queue, %v", err)
	}
	cs := &ClientStatus{
		// we allow unixTS to be overloaded to allow us to unit test this function
		// by overloading it.
		Timestamp:           unixTS(),
		PendingTransactions: pt,
		Results:             r,
	}

	c.sendErrMu.RLock()
	defer c.sendErrMu.RUnlock()
	cs.SendErrs = append(make([]error, 0, len(c.sendErr)), c.sendErr...)

	c.readErrMu.RLock()
	defer c.readErrMu.RUnlock()
	cs.ReadErrs = append(make([]error, 0, len(c.readErr)), c.readErr...)

	return cs, nil
}

// SetModifyPostRecvCallback sets a function that is called after each ModifyResponse
// is received in the client.
func (c *Client) SetModifyPostRecvCallback(fn func() error) error {
	if c.userFns == nil {
		c.userFns = &userFunctions{}
	}
	c.userFns.modifyPostRecvFn = fn
	return nil
}

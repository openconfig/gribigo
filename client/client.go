// Package client defines a gRIBI stub client implementation in Go.
package client

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	log "github.com/golang/glog"
	"go.uber.org/atomic"
	"google.golang.org/grpc"
	"lukechampine.com/uint128"

	spb "github.com/openconfig/gribi/v1/proto/service"
)

var (
	// unixTS is a function that returns a timestamp in nanoseconds for the current time.
	// It can be overloaded in unit tests to ensure that deterministic output is received.
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
	shut *atomic.Bool

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

	// awaiting is a read-write mutex that is used to ensure that we know
	// when any operation that might affect the convergence state of the
	// client is in-flight. Functions that are performing operations that may
	// result in a change take a read-lock, functions that are dependent upon
	// the client being in a consistent state take a write-lock on this mutex.
	awaiting sync.RWMutex
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

// Opt is an interface that is implemented for all options that
// can be supplied when creating a new client. This captures parameters
// that are sent at the start of a gRIBI session that
type Opt interface {
	isClientOpt()
}

// New creates a new gRIBI client with the specified set of options. The options
// provided control parameters that live for the lifetime of the client, such as those
// that are within the session parameters. A new client, or error, is returned.
func New(opts ...Opt) (*Client, error) {
	c := &Client{
		shut: atomic.NewBool(false),
	}

	s, err := handleParams(opts...)
	if err != nil {
		return nil, fmt.Errorf("cannot create client, error with parameters, %v", err)
	}
	c.state = s

	c.qs = &clientQs{
		sendq: []*spb.ModifyRequest{},
		pendq: &pendingQueue{
			Ops: map[uint64]*PendingOp{},
		},

		// modifyCh is unbuffered so that where needed, writes can be blocking.
		modifyCh: make(chan *spb.ModifyRequest),
		resultq:  []*OpResult{},

		sending: atomic.NewBool(false),
	}

	return c, nil
}

// handleParams takes the set of gRIBI client options that are provided and uses them
// to populate the session parameters that they are translated into. It returns a
// populate SessionParameters protobuf along with any errors when parsing the supplied
// set of options.
func handleParams(opts ...Opt) (*clientState, error) {
	if len(opts) == 0 {
		return &clientState{}, nil
	}
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
		case *persistEntries:
			s.SessParams.Persistence = spb.SessionParameters_PRESERVE
		}
	}
	return s, nil
}

// DialOpt specifies options that can be used when dialing the gRIBI server
// specified by the client.
type DialOpt interface {
	isDialOpt()
}

// Dial dials the server specified in the addr string, using the specified
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

// PersistEntries indicates that the client should request that the server
// persists entries after it has disconnected. By default, a gRIBI server will
// purge all entries when the client disconnects. This persistence mode may
// only be used when the redundancy mode is AllPrimaryClients.
func PersistEntries() *persistEntries {
	return &persistEntries{}
}

type persistEntries struct{}

func (persistEntries) isClientOpt() {}

// Connect establishes a Modify RPC to the client and sends the initial session
// parameters/election ID if required. The Modify RPC is stored within the client
// such that it can be used for subsequent calls - such that Connect must be called
// before subsequent operations that should send to the target.
//
// An error is returned if the Modify RPC cannot be established or there is an error
// response to the initial messages that are sent on the connection.
func (c *Client) Connect(ctx context.Context) error {

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

	// res takes a received modify response and error, and returns
	// a bool indicating that the loop within whcih it is called should
	// exit.
	rec := func(in *spb.ModifyResponse, err error) bool {
		log.V(2).Infof("received message on Modify stream: %s", in)
		c.awaiting.RLock()
		defer c.awaiting.RUnlock()
		if err == io.EOF {
			// reading is done, so write should shut down too.
			c.shut.Store(true)
			return true
		}
		if err != nil {
			log.Errorf("got error receiving message, %v", err)
			c.addReadErr(err)
			return true
		}
		if err := c.handleModifyResponse(in); err != nil {
			log.Errorf("got error processing message received from server, %v", err)
			c.addReadErr(err)
			return true
		}

		return false
	}

	go func() {
		for {
			if c.shut.Load() {
				log.V(2).Infof("shutting down recv goroutine")
				return
			}
			if done := rec(stream.Recv()); done {
				return
			}
		}
	}()

	// is handles an input modify request and returns a bool when
	// the loop within which it is called should exit.
	is := func(m *spb.ModifyRequest) bool {
		c.awaiting.RLock()
		defer c.awaiting.RUnlock()
		if err := stream.Send(m); err != nil {
			log.Errorf("got error sending message, %v", err)
			c.addSendErr(err)
			return true
		}
		log.V(2).Infof("sent Modify message %s", m)
		return false
	}

	go func() {
		for {
			if c.shut.Load() {
				log.V(2).Infof("shutting down send goroutine")
				return
			}

			// read from the channel
			if done := is(<-c.qs.modifyCh); done {
				return
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
	pendq *pendingQueue

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
	sending *atomic.Bool
}

// pendingQueue provides a queue type that determines the set of pending
// operations for the client. Since pending operations are added and removed
// based on receiving a ModifyRequest or ModifyResponse in the client, there is
// no case in which we need to individually access these elements, so they are
// to be protected by a single mutex.
type pendingQueue struct {
	// ops is the set of AFT operations that are pending in the client.
	Ops map[uint64]*PendingOp
	// Election is the pending election that has been sent to the client, there can
	// only be one pending update - since each election replaces the previous one. A
	// single response is expected to clear all the election updates that have been sent
	// to the server.
	Election *ElectionReqDetails

	// SessionParams indicates that there is a pending SessionParameters
	// request sent to the server. There can only be one such request since
	// we only send the request at the start of the connection, and MUST NOT
	// send it again.
	SessionParams *SessionParamReqDetails
}

// ElectionReqDetails stores the details of an individual election update that was sent
// to the server.
type ElectionReqDetails struct {
	// Timestamp is the time at which the update was sent, specified in nanoseconds
	// since the epoch.
	Timestamp int64
	// ID is the election ID that was sent to the server. This can be used to determine
	// whether the response shows that this client actually became the master.
	ID *spb.Uint128
}

// isPendingRequest implements the PendingRequest interface for ElectionReqDetails.
func (*ElectionReqDetails) isPendingRequest() {}

// SessionParamReqDetails stores the details of an individual session parameters update
// that was sent to the server.
type SessionParamReqDetails struct {
	// Timestamp is the time at which the update was sent, specified in nanoseconds
	// since the epoch.
	Timestamp int64
	// Outgoing is the parameters that were sent to the server.
	Outgoing *spb.SessionParameters
}

// isPendingRequest implements the PendingRequest interface for SessionParamReqDetails.
func (*SessionParamReqDetails) isPendingRequest() {}

// Len returns the length of the pending queue.
func (p *pendingQueue) Len() int {
	if p == nil {
		return 0
	}
	var i int
	if p.SessionParams != nil {
		i++
	}
	if p.Election != nil {
		i++
	}

	return i + len(p.Ops)
}

// PendingRequest is an interface implemented by all types that can be reported back
// to callers describing a pending request in the client.
type PendingRequest interface {
	isPendingRequest()
}

// PendingOp stores details pertaining to a pending request in the client.
type PendingOp struct {
	// Timestamp is the timestamp that the operation was queued at.
	Timestamp int64
	// Op is the operation that the pending request pertains to.
	Op *spb.AFTOperation
}

// isPendingRequest implements the PendingRequest interface for the PendingOp type.
func (*PendingOp) isPendingRequest() {}

// OpResult stores details pertaining to a result that was received from
// the server.
type OpResult struct {
	// Timestamp is the timestamp that the result was received.
	Timestamp int64
	// Latency is the latency of the request from the server. This is calculated
	// based on the time that the request was sent to the client (became pending)
	// and the time that the response was received from the server. It is expressed
	// in nanoseconds.
	Latency int64

	// CurrentServerElectionID indicates that the message that was received from the server
	// was an ModifyResponse sent in response to an updated election ID, its value is the
	// current master election ID (maximum election ID seen from any client) reported from
	// the server.
	CurrentServerElectionID *spb.Uint128

	// SessionParameters contains the parameters that were received from the server in
	// response to the parameters sent to the server.
	SessionParameters *spb.SessionParametersResult

	// OperationID indicates that the message that was received from the server was a
	// ModifyResponse sent in response to an AFTOperation, its value is the ID of the
	// operation to which it corresponds.
	OperationID uint64

	// ClientError describes an error that is internal to the client.
	ClientError string

	// TODO(robjs): add additional information about what the result was here.
}

// String returns a string for an OpResult for debugging purposes.
func (o *OpResult) String() string {
	buf := &bytes.Buffer{}
	buf.WriteString("<")
	buf.WriteString(fmt.Sprintf("%d (%d nsec):", o.Timestamp, o.Latency))

	if v := o.CurrentServerElectionID; v != nil {
		e := uint128.New(v.Low, v.High)
		buf.WriteString(fmt.Sprintf(" ElectionID: %s", e))
	}

	if v := o.OperationID; v != 0 {
		buf.WriteString(fmt.Sprintf(" AFTOperation { ID: %d }", v))
	}

	if v := o.SessionParameters; v != nil {
		buf.WriteString(fmt.Sprintf(" SessionParameterResult: OK (%s)", v.String()))
	}

	if v := o.ClientError; v != "" {
		buf.WriteString(fmt.Sprintf(" With Error: %s", v))
	}
	buf.WriteString(">")

	return buf.String()
}

// Q enqueues a ModifyRequest to be sent to the target.
func (c *Client) Q(m *spb.ModifyRequest) {

	if err := c.handleModifyRequest(m); err != nil {
		log.Errorf("got error processing message that was to be sent, %v", err)
		c.addSendErr(err)
	}

	if !c.qs.sending.Load() {
		c.qs.sendMu.Lock()
		defer c.qs.sendMu.Unlock()
		log.V(2).Infof("appended %s to sending queue", m)
		c.qs.sendq = append(c.qs.sendq, m)
		return
	}
	log.V(2).Infof("sending %s directly to queue", m)

	c.awaiting.RLock()
	defer c.awaiting.RUnlock()
	c.qs.modifyCh <- m
}

// StartSending toggles the client to begin sending messages that are in the send
// queue (enqued by Q) to the connection established by Connect.
func (c *Client) StartSending() {
	c.qs.sending.Store(true)

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

	// take the initial set of messages that were enqueued and queue them.
	c.qs.sendMu.Lock()
	defer c.qs.sendMu.Unlock()
	for _, m := range c.qs.sendq {
		log.V(2).Infof("sending %s to modify channel", m)
		c.Q(m)
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
		if err := c.addPendingOp(o); err != nil {
			return err
		}
	}

	if m.ElectionId != nil {
		c.updatePendingElection(m.ElectionId)
	}

	if m.Params != nil {
		c.pendingSessionParams(m.Params)
	}

	return nil
}

// handleModifyResponse performs any required post-processing after having received
// a ModifyResponse from the gRPC channel from the server. Particularly, this
// ensures that pending transactions are dequeued into the results queue. An error
// is returned if the post processing is not possible.
func (c *Client) handleModifyResponse(m *spb.ModifyResponse) error {
	if m == nil {
		return errors.New("invalid nil modify response returned")
	}

	resPop := m.Result != nil
	elecPop := m.ElectionId != nil
	sessPop := m.SessionParamsResult != nil
	pop := 0
	for _, v := range []bool{resPop, elecPop, sessPop} {
		if v {
			pop++
		}
	}
	if pop > 1 {
		return fmt.Errorf("invalid returned message, ElectionID, Result, and SessionParametersResult are mutually exclusive, got: %s", m)
	}

	// At this point we know we will have something to add to the results, so ensure that we
	// hold the results lock. This also stops the client from converging.
	c.qs.resultMu.Lock()
	defer c.qs.resultMu.Unlock()

	if m.ElectionId != nil {
		er := c.clearPendingElection()
		er.CurrentServerElectionID = m.ElectionId
		// This is an update from the server in response to an updated master election ID.
		c.qs.resultq = append(c.qs.resultq, er)
	}

	if m.SessionParamsResult != nil {
		sr := c.clearPendingSessionParams()
		sr.SessionParameters = m.SessionParamsResult
		c.qs.resultq = append(c.qs.resultq, sr)
	}

	// TODO(robjs): add handling of received operations.

	return nil
}

// isConverged indicates whether the client is converged.
func (c *Client) isConverged() bool {
	c.qs.sendMu.RLock()
	defer c.qs.sendMu.RUnlock()
	c.qs.pendMu.RLock()
	defer c.qs.pendMu.RUnlock()

	return len(c.qs.sendq) == 0 && c.qs.pendq.Len() == 0
}

// addPendingOp adds the operation specified by op to the pending transaction queue
// on the client. This queue stores the operations that have been sent to the server
// but have not yet been reported on. It returns an error if the pending transaction
// cannot be added.
func (c *Client) addPendingOp(op *spb.AFTOperation) error {
	c.qs.pendMu.Lock()
	defer c.qs.pendMu.Unlock()
	if c.qs.pendq.Ops[op.Id] != nil {
		return fmt.Errorf("could not enqueue operation %d, duplicate pending ID", op.Id)
	}
	c.qs.pendq.Ops[op.Id] = &PendingOp{
		Timestamp: unixTS(),
		Op:        op,
	}
	return nil
}

// updatePendingElection adds the election ID specified by id to the pending transaction
// queue on the client. There can only be a single pending election, and hence the
// election ID is updated in place.
func (c *Client) updatePendingElection(id *spb.Uint128) {
	c.qs.pendMu.Lock()
	defer c.qs.pendMu.Unlock()
	c.qs.pendq.Election = &ElectionReqDetails{
		Timestamp: unixTS(),
		ID:        id,
	}
}

// clearPendingElection clears the pending election ID and returns a result determining
// the latency and timestamp. If there is no pending election, a result describing an error
// is returned.
func (c *Client) clearPendingElection() *OpResult {
	c.qs.pendMu.Lock()
	defer c.qs.pendMu.Unlock()
	e := c.qs.pendq.Election
	if e == nil {
		return &OpResult{
			Timestamp:   unixTS(),
			ClientError: "received a election update when there was none pending",
		}
	}

	n := unixTS()
	c.qs.pendq.Election = nil
	return &OpResult{
		Timestamp: n,
		Latency:   n - e.Timestamp,
	}
}

// pendingSessionParams updates the client's outgoing queue to indicate that there
// is a pending session parameters request.
func (c *Client) pendingSessionParams(out *spb.SessionParameters) {
	c.qs.pendMu.Lock()
	defer c.qs.pendMu.Unlock()
	c.qs.pendq.SessionParams = &SessionParamReqDetails{
		Timestamp: unixTS(),
		Outgoing:  out,
	}
}

// clearPendingSessionParams clears the pending session parameters request in the client
// and returns an OpResult describing the timing of the response.
func (c *Client) clearPendingSessionParams() *OpResult {
	c.qs.pendMu.Lock()
	defer c.qs.pendMu.Unlock()
	e := c.qs.pendq.SessionParams

	if e == nil {
		return &OpResult{
			Timestamp:   unixTS(),
			ClientError: "received a session parameter result when there was none pending",
		}
	}

	c.qs.pendq.SessionParams = nil
	n := unixTS()
	return &OpResult{
		Timestamp: n,
		Latency:   n - e.Timestamp,
	}
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
// target, it returns a slice of PendingRequest interfaces which describes the contents
// of the pending queues.
func (c *Client) Pending() ([]PendingRequest, error) {
	if c.qs == nil {
		return nil, errors.New("invalid (nil) queues in client")
	}
	c.qs.pendMu.RLock()
	defer c.qs.pendMu.RUnlock()
	ret := []PendingRequest{}

	ids := []uint64{}
	for k, o := range c.qs.pendq.Ops {
		if o == nil || o.Op == nil {
			return nil, errors.New("invalid (nil) operation in pending queue")
		}
		ids = append(ids, k)
	}

	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, v := range ids {
		ret = append(ret, c.qs.pendq.Ops[v])
	}
	if c.qs.pendq.Election != nil {
		ret = append(ret, c.qs.pendq.Election)
	}
	if c.qs.pendq.SessionParams != nil {
		ret = append(ret, c.qs.pendq.SessionParams)
	}
	return ret, nil
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
	// PendingTransactions is the slice of pending operations on the client.
	PendingTransactions []PendingRequest
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

// hasErrors reports whether there are errors that have been encountered
// in the client. It returns two slices of errors containing the send
// and recv errors
func (c *Client) hasErrors() ([]error, []error) {
	c.readErrMu.RLock()
	defer c.readErrMu.RUnlock()
	c.sendErrMu.RLock()
	defer c.sendErrMu.RUnlock()
	if len(c.readErr) != 0 || len(c.sendErr) != 0 {
		return c.sendErr, c.readErr
	}
	return nil, nil
}

// ClientError encapsulates send and receive errors for the client.
type ClientErr struct {
	Send, Recv []error
}

// Error implements the error interface.
func (c *ClientErr) Error() string { return fmt.Sprintf("errors: send: %v, recv: %v", c.Send, c.Recv) }

// AwaitConverged waits until the client is converged and writes to the supplied
// channel. The function blocks until such time as the client returns.
func (c *Client) AwaitConverged() error {
	for {
		// we need to check here that no-one is doing an operation that we might care about
		// impacting convergence. this is:
		//  - sending a message
		//	- post-processing a sent message
		//	- reading a message
		//	- post-procesing a read message
		// we do this by holding the awaiting mutex.
		done, err := func() (bool, error) {
			c.awaiting.Lock()
			defer c.awaiting.Unlock()
			if sendE, recvE := c.hasErrors(); len(sendE) != 0 || len(recvE) != 0 {
				return true, &ClientErr{Send: sendE, Recv: recvE}
			}
			if c.isConverged() {
				return true, nil
			}
			return false, nil
		}()
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
}

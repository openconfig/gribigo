// Copyright 202oogle LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package server defines the basic elements of a gRIBI server that uses
// an in-memory data store for the AFT contents.
package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	log "github.com/golang/glog"
	"github.com/google/uuid"
	"github.com/openconfig/gribigo/rib"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/prototext"
	"lukechampine.com/uint128"

	spb "github.com/openconfig/gribi/v1/proto/service"
)

const (
	// DefaultNetworkInstanceName specifies the name of the default network instance on the system.
	DefaultNetworkInstanceName = "DEFAULT"
)

// unixTS is used to determine the current unix timestamp in nanoseconds since the
// epoch. It is defined such that it can be overloaded by unit tests.
var unixTS = time.Now().UnixNano

// Server implements the gRIBI service.
type Server struct {
	*spb.UnimplementedGRIBIServer

	// csMu protects the cs map.
	csMu sync.RWMutex
	// cs stores the state for clients that are connected to the server
	// this allows the server perform operations such as ensuring consistency
	// across different connected clients. The key of the map is a unique string
	// identifying each client, which in this implementation is a UUID generated
	// at connection time. The client ID is scoped as such to ensure that we don't
	// have any state as to where the current connection came from (source address,
	// or TCP session handle for example).
	//
	// This design is acceptable because there is no case in which we have an
	// operation within gRIBI whereby a client references some state that is
	// associated with a prior connection that it had.
	cs map[string]*clientState

	// elecMu protects the curElecID and curMaster values.
	elecMu sync.RWMutex
	// curElecID stores the current electionID for cases where the server is
	// operating in SINGLE_PRIMARY mode.
	curElecID *spb.Uint128
	// curMaster stores the current master's UUID.
	curMaster string

	// masterRIB is the single gRIBI RIB that is used for a server that runs with
	// a single elected master, where a single RIB is written to by all clients.
	masterRIB *rib.RIB
}

// clientState stores information that relates to a specific client
// connected to the gRIBI server.
type clientState struct {
	// params stores parameters that are associated with a single
	// client of the server. These parameters are advertised as the
	// first message on a Modify stream. It is an error to send
	// parameters in any other context (i.e., after other ModifyRequest
	// messages have been sent, or to adjust these parameters).
	params *clientParams
	// setParams indicates whether the parameters have been explicitly
	// written.
	setParams bool
	// lastElecID stores the last election ID that the client
	// sent to the server. This is used to validate whether the election
	// ID in an operation matches the expected election ID.
	lastElecID *spb.Uint128
}

// DeepCopy returns a copy of the clientState struct.
func (cs *clientState) DeepCopy() *clientState {
	if cs.params == nil {
		return &clientState{}
	}
	return &clientState{
		params: cs.params.DeepCopy(),
	}
}

// clientParams stores parameters that are set as part of the Modify RPC
// initial handshake for a particular client.
type clientParams struct {
	// Persist indicates whether the client's AFT entries should be
	// persisted even after the client disconnects.
	Persist bool

	// ExpectElecID indicates whether the client expects to send
	// election IDs (i.e., the ClientRedundancy is SINGLE_PRIMARY).
	ExpectElecID bool

	// FIBAck indicates whether the client expects FIB-level
	// acknowledgements.
	FIBAck bool
}

// DeepCopy returns a copy of the clientParams struct.
func (cp *clientParams) DeepCopy() *clientParams {
	return &clientParams{
		Persist:      cp.Persist,
		ExpectElecID: cp.ExpectElecID,
		FIBAck:       cp.FIBAck,
	}
}

// Equal returns true if the candidate clientParams n is equal to the receiver cp.
func (cp *clientParams) Equal(n *clientParams) bool {
	return cp.Persist == n.Persist && cp.FIBAck == n.FIBAck && cp.ExpectElecID == n.ExpectElecID
}

// ServerOpt is an interface that is implemented by any options to the gRIBI server.
type ServerOpt interface {
	isServerOpt()
}

// WithPostChangeRIBHook specifies that the given function that is of type rib.RIBHook function
// should be executed after each change in the RIB.
func WithPostChangeRIBHook(fn rib.RIBHookFn) *postChangeRibHook {
	return &postChangeRibHook{fn: fn}
}

// ribHook is the internal implementation of the WithRIBHook option.
type postChangeRibHook struct {
	fn rib.RIBHookFn
}

// isServerOpt implements the ServerOpt interface.
func (*postChangeRibHook) isServerOpt() {}

// hasPostChangeRIBHook extracts the ribHook option from the supplied ServerOpt, returning nil
// if one is not found. It will return only the first argument if multiple are specified.
func hasPostChangeRIBHook(opt []ServerOpt) *postChangeRibHook {
	for _, o := range opt {
		if v, ok := o.(*postChangeRibHook); ok {
			return v
		}
	}
	return nil
}

// WithResolvedEntryHook is a Server option that allows a function to be run for
// each entry that can be fully resolved within the RIB (e.g., IPv4Entry).
func WithRIBResolvedEntryHook(fn rib.ResolvedEntryFn) *resolvedEntryHook {
	return &resolvedEntryHook{fn: fn}
}

// resolvedEntryHook is the internal implementation of the WithRIBResolvedEntryHook
// option.
type resolvedEntryHook struct {
	fn rib.ResolvedEntryFn
}

// isServerOpt implements the ServerOpt interface.
func (r *resolvedEntryHook) isServerOpt() {}

// hasResolvedEntryHook returns the resolvedEntryHook from the specified options
// if one exists. It will return only the first argument if multiple are specified.
func hasResolvedEntryHook(opt []ServerOpt) *resolvedEntryHook {
	for _, o := range opt {
		if v, ok := o.(*resolvedEntryHook); ok {
			return v
		}
	}
	return nil
}

// DisableRIBCheckFn specifies that the consistency checking functions should
// be disabled for the RIB. It is useful for a testing RIB that does not need
// to have working references.
func DisableRIBCheckFn() *disableCheckFn { return &disableCheckFn{} }

// disableCheckFn is the internal implementation of DisableRIBCheckFn.
type disableCheckFn struct{}

// isServerOpt implements the ServerOpt interface
func (*disableCheckFn) isServerOpt() {}

// hasDisableCheckFn checks whether the ServerOpt slice supplied contains the
// disableCheckFn option.
func hasDisableCheckFn(opt []ServerOpt) bool {
	for _, o := range opt {
		if _, ok := o.(*disableCheckFn); ok {
			return true
		}
	}
	return false
}

// WithVRFs specifies that the server should be initialised with the L3VRF
// network instances specified in the names list. Each is created in the
// server's RIB such that it can be referenced.
func WithVRFs(names []string) *withVRFs { return &withVRFs{names: names} }

// withVRFs is the internal implementation of WithVRFs that can be read by the
// server.
type withVRFs struct {
	names []string
}

// isServerOpt implements the ServerOpt interface.
func (*withVRFs) isServerOpt() {}

// hasWithVRFs checks whether the ServerOpt slice supplied contains the withVRFs
// option and returns it if so.
func hasWithVRFs(opt []ServerOpt) []string {
	for _, o := range opt {
		if v, ok := o.(*withVRFs); ok {
			return v.names
		}
	}
	return nil
}

// New creates a new gRIBI server.
func New(opt ...ServerOpt) (*Server, error) {
	ribOpt := []rib.RIBOpt{}
	if hasDisableCheckFn(opt) {
		ribOpt = append(ribOpt, rib.DisableRIBCheckFn())
	}

	s := &Server{
		cs: map[string]*clientState{},
		// TODO(robjs): when we implement support for ALL_PRIMARY then we might not
		// want to create a new RIB by default.
		masterRIB: rib.New(DefaultNetworkInstanceName, ribOpt...),
	}

	if v := hasPostChangeRIBHook(opt); v != nil {
		s.masterRIB.SetPostChangeHook(v.fn)
	}

	if v := hasResolvedEntryHook(opt); v != nil {
		s.masterRIB.SetResolvedEntryHook(v.fn)
	}

	if vrfs := hasWithVRFs(opt); vrfs != nil {
		for _, n := range vrfs {
			if err := s.masterRIB.AddNetworkInstance(n); err != nil {
				return nil, fmt.Errorf("cannot create network instance %s, %v", n, err)
			}
		}
	}

	return s, nil
}

// Modify implements the gRIBI Modify RPC.
func (s *Server) Modify(ms spb.GRIBI_ModifyServer) error {
	// Initiate the per client state for this client.
	cid := uuid.New().String()
	log.V(2).Infof("creating client with ID %s", cid)
	if err := s.newClient(cid); err != nil {
		return err
	}

	resultChan := make(chan *spb.ModifyResponse)
	errCh := make(chan error)
	go func() {
		// Store whether this is the first message on the Modify RPC, some options - like the session
		// parameters can only be set as the first message.
		var gotmsg bool
		for {
			in, err := ms.Recv()
			if err == io.EOF {
				errCh <- nil
				return
			}
			if err != nil {
				errCh <- status.Errorf(codes.Unknown, "error reading message from client, %v", err)
				return
			}
			log.V(2).Infof("received message %s on Modify channel", in)

			var (
				res       *spb.ModifyResponse
				skipWrite bool
			)

			switch {
			case in == nil:
				log.Errorf("received nil message on Modify channel")
				skipWrite = true
			case in.Params != nil:
				var err error
				if res, err = s.checkParams(cid, in.Params, gotmsg); err != nil {
					// Invalid parameters is a fatal error, so we take down the Modify RPC.
					errCh <- err
					return
				}
				if err := s.updateParams(cid, in.Params); err != nil {
					// Not being able to update parameters is a fatal error, so we take down
					// the Modify RPC.
					errCh <- err
					return
				}
			case in.ElectionId != nil:
				var err error
				res, err = s.runElection(cid, in.ElectionId)
				if err != nil {
					errCh <- err
					return
				}
			case in.Operation != nil:
				s.doModify(cid, in.Operation, resultChan, errCh)
				skipWrite = true
			default:
				errCh <- status.Errorf(codes.Unimplemented, "unimplemented handling of message %s", in)
				return
			}

			gotmsg = true
			// write the results to result channel.
			if !skipWrite {
				resultChan <- res
			}
		}
	}()

	go func() {
		for {
			res := <-resultChan
			// update that we have received at least one message.
			if err := ms.Send(res); err != nil {
				errCh <- status.Errorf(codes.Internal, "cannot write message to client channel, %s", res)
				return
			}
		}
	}()

	err := <-errCh

	// when this client goes away, we need to clean up its state.
	s.deleteClient(cid)

	return err
}

// Get implements the gRIBI Get RPC.
func (s *Server) Get(req *spb.GetRequest, stream spb.GRIBI_GetServer) error {
	msgCh := make(chan *spb.GetResponse)
	errCh := make(chan error)
	doneCh := make(chan struct{})
	stopCh := make(chan struct{})

	// defer a function to stop the goroutine and close all channels, since this will be called
	// when we exit, then it will stop the goroutine that we started to do
	// the get in the case that we exit due to some error.
	defer func() {
		// Non-blocking write to the stopCh, since if the goroutine has
		// already returned then it won't be listening and we'll deadlock.
		select {
		case stopCh <- struct{}{}:
		default:
		}
	}()

	go s.doGet(req, msgCh, doneCh, stopCh, errCh)

	var done bool

	for !done {
		select {
		case <-doneCh:
			done = true
		case err := <-errCh:
			return status.Errorf(codes.Internal, "cannot generate GetResponse, %v", err)
		case r := <-msgCh:
			if err := stream.Send(r); err != nil {
				return status.Errorf(codes.Internal, "cannot write message to client channel, %v", err)
			}
		}
	}
	return nil
}

// Flush implements the gRIBI Flush RPC - used for removing entries from the server.
func (s *Server) Flush(ctx context.Context, req *spb.FlushRequest) (*spb.FlushResponse, error) {
	if err := s.checkFlushRequest(req); err != nil {
		return nil, err
	}

	nis := []string{}
	switch t := req.GetNetworkInstance().(type) {
	case *spb.FlushRequest_All:
		nis = s.masterRIB.KnownNetworkInstances()
	case *spb.FlushRequest_Name:
		if _, ok := s.masterRIB.NetworkInstanceRIB(t.Name); !ok {
			return nil, addFlushErrDetailsOrReturn(status.Newf(codes.InvalidArgument, "could not find network instance %s", t.Name), &spb.FlushResponseError{
				Status: spb.FlushResponseError_INVALID_NETWORK_INSTANCE,
			})
		}
		nis = []string{t.Name}
	}

	msgs := []string{}
	for _, n := range nis {
		niR, ok := s.masterRIB.NetworkInstanceRIB(n)
		if !ok {
			// non-fatal, this means that a network instnace that we checked for
			// was removed during the flush.
			log.Errorf("network instance %s was removed during Flush", n)
			continue
		}
		if err := niR.Flush(); err != nil {
			msgs = append(msgs, fmt.Sprintf("cannot flush RIB for network instance, %s", n))
		}
	}

	if len(msgs) != 0 {
		det := &bytes.Buffer{}
		for _, msg := range msgs {
			det.WriteString(fmt.Sprintf("%s\n", msg))
		}
		// Always use codes.Internal here since any error (not being able to flush, or
		// not finding an NI that we already checked existed is some internal logic
		// error).
		return nil, status.Errorf(codes.Internal, det.String())
	}

	return &spb.FlushResponse{
		Timestamp: unixTS(),
	}, nil
}

// newClient creates a new client context within the server using the specified string
// ID.
func (s *Server) newClient(id string) error {
	s.csMu.Lock()
	defer s.csMu.Unlock()
	if s.cs[id] != nil {
		return status.Errorf(codes.Internal, "cannot create new client with duplicate ID, %s", id)
	}
	s.cs[id] = &clientState{
		// Set to the default set of parameters.
		params: &clientParams{},
	}

	return nil
}

// deleteClient removes the client with the specified id from the server. It does not return
// an error if the client cannot be deleted, since this action is performed when the other
// side has already gone away, so there is no error we can return to them.
func (s *Server) deleteClient(id string) {
	s.csMu.Lock()
	defer s.csMu.Unlock()
	delete(s.cs, id)
}

// updateParams writes the parameters for the client specified by id to the server state
// based on the received session parameters supplied in params. It returns errors if
// the client is undefined, or the parameters have been set previously. It does not
// ensure that the parameters are consistent with other clients on the server - but
// rather solely updates for a particular client.
func (s *Server) updateParams(id string, params *spb.SessionParameters) error {
	cparam := &clientParams{}

	cparam.ExpectElecID = (params.Redundancy == spb.SessionParameters_SINGLE_PRIMARY)
	cparam.Persist = (params.Persistence == spb.SessionParameters_PRESERVE)
	cparam.FIBAck = (params.AckType == spb.SessionParameters_RIB_AND_FIB_ACK)

	s.csMu.Lock()
	defer s.csMu.Unlock()
	p := s.cs[id]
	if p == nil || p.params == nil {
		return status.Errorf(codes.Internal, "cannot update parameters for a client with no state, %s", id)
	}
	if p.setParams {
		return addModifyErrDetailsOrReturn(status.New(codes.FailedPrecondition, "cannot modify SessionParameters"), &spb.ModifyRPCErrorDetails{
			Reason: spb.ModifyRPCErrorDetails_MODIFY_NOT_ALLOWED,
		})
	}
	s.cs[id].setParams = true
	s.cs[id].params = cparam
	return nil
}

// addModifyErrDetailsOrReturn takes an input status (s), and ModifyRPCErrorDetails proto (d) and appends
// d to s. If an error is encountered, s is returned, otherwise the appended version is returned. The return
// type is a Go error which can be returned directly.
func addModifyErrDetailsOrReturn(s *status.Status, d *spb.ModifyRPCErrorDetails) error {
	ns, err := s.WithDetails(d)
	if err != nil {
		return s.Err()
	}
	return ns.Err()
}

// checkParams validates that the parameters that were supplied by the client with the specified
// ID are valid within the overall server context. It returns the ModifyResponse that should be sent
// to the client, or the populated error.
func (s *Server) checkParams(id string, p *spb.SessionParameters, gotMsg bool) (*spb.ModifyResponse, error) {
	if p == nil {
		return nil, status.Newf(codes.Internal, "invalid nil parameters when checking for client %s, got: %v", id, p).Err()
	}

	if gotMsg {
		// TODO(robjs): the spec should spell out that this can't be sent after the first message is
		// received, whatever the type.
		return nil, addModifyErrDetailsOrReturn(status.New(codes.FailedPrecondition, "cannot send session parameters after another message"), &spb.ModifyRPCErrorDetails{
			Reason: spb.ModifyRPCErrorDetails_MODIFY_NOT_ALLOWED,
		})
	}

	// TODO(robjs): confirm with folks what their thoughts are on whether we support persistence
	// other than DELETE in ALL_PRIMARY. I think that we should not support this since it means
	// that we need to externalise the client ID (so that the client can delete its old entries, but
	// not others).
	if p.Redundancy == spb.SessionParameters_ALL_PRIMARY && p.Persistence == spb.SessionParameters_PRESERVE {
		return nil, addModifyErrDetailsOrReturn(status.New(codes.FailedPrecondition, "cannot have ALL_PRIMARY client with persistence PRESERVE"), &spb.ModifyRPCErrorDetails{
			Reason: spb.ModifyRPCErrorDetails_UNSUPPORTED_PARAMS,
		})
	}

	// The fake server supports both RIB and FIB ACKing (given that we do not have any real
	// FIB, they are basically the same :-)). It does not (currently) support ALL_PRIMARY
	// mode of operations.
	if p.Redundancy == spb.SessionParameters_ALL_PRIMARY {
		return nil, addModifyErrDetailsOrReturn(status.Newf(codes.Unimplemented, "ALL_PRIMARY redundancy are not supported"), &spb.ModifyRPCErrorDetails{
			Reason: spb.ModifyRPCErrorDetails_UNSUPPORTED_PARAMS,
		})
	}

	// The fake server does not (currently) support delete, so we just return an error
	// if the client is asking for anything other than persisting the entries.
	if p.Persistence == spb.SessionParameters_DELETE {
		return nil, addModifyErrDetailsOrReturn(status.Newf(codes.Unimplemented, "persistence modes other than PRESERVE are not supported"), &spb.ModifyRPCErrorDetails{
			Reason: spb.ModifyRPCErrorDetails_UNSUPPORTED_PARAMS,
		})
	}

	cp := &clientParams{
		FIBAck:       p.GetAckType() == spb.SessionParameters_RIB_AND_FIB_ACK,
		ExpectElecID: p.GetRedundancy() == spb.SessionParameters_SINGLE_PRIMARY,
		Persist:      p.GetPersistence() == spb.SessionParameters_PRESERVE,
	}

	consistent, err := s.checkClientsConsistent(id, cp)
	if err != nil {
		return nil, status.Newf(codes.Internal, "got unexpected error checking for consistency, %v", err).Err()
	}
	if !consistent {
		return nil, addModifyErrDetailsOrReturn(status.Newf(codes.FailedPrecondition, "client %s is not consistent with other clients", id), &spb.ModifyRPCErrorDetails{
			Reason: spb.ModifyRPCErrorDetails_PARAMS_DIFFER_FROM_OTHER_CLIENTS,
		})
	}

	if err := s.setClientParams(id, cp); err != nil {
		return nil, status.Errorf(codes.Internal, "internal error setting parameters, %v", err)
	}

	return &spb.ModifyResponse{
		SessionParamsResult: &spb.SessionParametersResult{
			Status: spb.SessionParametersResult_OK,
		},
	}, nil
}

// checkClientsConsistent ensures that the client described by id and the parameters p is consistent
// with other clients in the server.
func (s *Server) checkClientsConsistent(id string, p *clientParams) (bool, error) {
	if p == nil {
		return false, fmt.Errorf("unexpected nil parameters for client %s", id)
	}

	s.csMu.RLock()
	defer s.csMu.RUnlock()
	for cid, state := range s.cs {
		if id == cid {
			continue
		}
		if state == nil || state.params == nil {
			return false, fmt.Errorf("client %s has invalid nil parameter state", cid)
		}

		if !state.params.Equal(p) {
			return false, nil
		}
	}
	return true, nil
}

// setClientParams sets the parameters for client with id to have the specified parameters.
func (s *Server) setClientParams(id string, p *clientParams) error {
	s.csMu.Lock()
	defer s.csMu.Unlock()
	if s.cs[id] == nil {
		return fmt.Errorf("cannot find client %s, known clients: %v", id, s.cs)
	}
	s.cs[id].params = p
	return nil
}

// isNewMaster takes two election IDs and determines whether the candidate (cand) is a new
// master given the existing master (exist). It returns:
//  - a bool which indicates whether the candidate is the new master
//  - a bool which indicates whether this is an election ID with the same election ID
//  - an error indicating whether this was an invalid update
func isNewMaster(cand, exist *spb.Uint128) (bool, bool, error) {
	if exist == nil {
		return true, false, nil
	}
	if cand.High > exist.High {
		return true, false, nil
	}
	if cand.Low > exist.Low {
		return true, false, nil
	}

	// Per comments in gribi.proto - if the two values are equal, then we accept the new
	// candidate as the master, this allows for reconnections.
	if cand.High == exist.High && cand.Low == exist.Low {
		return true, true, nil
	}
	// TODO(robjs): currently this is not specified in the spec, since this is the
	// election ID going backwards, but it seems like we could not return an error here
	// since it might just be a stale client telling us that they think that they're the
	// master. However, we could return an error just to say "hey, you're somehow out of sync
	// with the master election system".
	return false, false, nil
}

// getClientState returns the client state for the client with the specified id, along with
// whether the client was found.
func (s *Server) getClientState(id string) (*clientState, bool) {
	s.csMu.RLock()
	defer s.csMu.RUnlock()
	cs, ok := s.cs[id]
	return cs, ok
}

// storeClientElectionID stores the latest election ID for a client into the
// server specified. It returns true if the election ID was stored.
func (s *Server) storeClientElectionID(id string, elecID *spb.Uint128) bool {
	s.csMu.Lock()
	defer s.csMu.Unlock()
	cs, ok := s.cs[id]
	if !ok {
		return false
	}
	cs.lastElecID = elecID
	return true
}

// getClientStateCopy returns a copy of the state for the client with the specified ID, since
// the state of a client is immutable after the initial creation, we never allow a client to
// get a copy of the pointer so that they could change this. It returns a copy of the clientState
// an error.
func (s *Server) getClientStateCopy(id string) (*clientState, error) {
	s.csMu.RLock()
	defer s.csMu.RUnlock()
	if s.cs[id] == nil {
		return nil, fmt.Errorf("unknown client %s", id)
	}
	return s.cs[id].DeepCopy(), nil
}

// runElection runs an election on the server and checks whether the client with the specified id is
// the new master.
func (s *Server) runElection(id string, elecID *spb.Uint128) (*spb.ModifyResponse, error) {
	cs, err := s.getClientStateCopy(id)
	if err != nil {
		return nil, err
	}
	if !cs.params.ExpectElecID {
		return nil, addModifyErrDetailsOrReturn(status.Newf(codes.FailedPrecondition, "client ID %s does not expect elections", id), &spb.ModifyRPCErrorDetails{
			Reason: spb.ModifyRPCErrorDetails_ELECTION_ID_IN_ALL_PRIMARY,
		})
	}

	// If the election ID that we received is 0, then this is an invalid value
	// in the input message, return an error to the client.
	if inputID, zero := uint128.New(elecID.Low, elecID.High), uint128.New(0, 0); zero.Cmp(inputID) == 0 {
		return nil, status.Newf(codes.InvalidArgument, "client ID %s, zero is an invalid election ID", id).Err()
	}

	// At this point, we store the latest election ID that we've seen from this
	// client, even if it does not win the election. This allows us to check
	// that the client has the same election ID as it has reported to us in
	// subsequent transactions.
	if !s.storeClientElectionID(id, elecID) {
		return nil, status.Newf(codes.Internal, "cannot store election ID %s for client %s", elecID, id).Err()
	}

	s.elecMu.RLock()
	defer s.elecMu.RUnlock()
	nm, _, err := isNewMaster(elecID, s.curElecID)
	if err != nil {
		return nil, err
	}

	if nm {
		s.curElecID = elecID
		s.curMaster = id
	}

	return &spb.ModifyResponse{
		ElectionId: s.curElecID,
	}, nil
}

// getElection returns the details of the current election on the server.
func (s *Server) getElection() *electionDetails {
	s.elecMu.RLock()
	defer s.elecMu.RUnlock()
	return &electionDetails{
		master: s.curMaster,
		ID:     s.curElecID,
	}
}

// doModify implements a modify operation for a specific input set of AFTOperation
// messages for the client with the specified cid. It writes the result to the supplied
// ModifyResponse channel when successful, or writes the error to the supplied errCh.
func (s *Server) doModify(cid string, ops []*spb.AFTOperation, resCh chan *spb.ModifyResponse, errCh chan error) {
	cs, ok := s.getClientState(cid)
	switch {
	case !ok:
		errCh <- status.Newf(codes.Internal, "operation received for unknown client, %s", cid).Err()
		return
	case cs.params == nil || !cs.params.ExpectElecID || !cs.params.Persist:
		// these are parameters that we do not support.
		errCh <- addModifyErrDetailsOrReturn(
			status.New(codes.Unimplemented, "unsupported parameters for client"),
			&spb.ModifyRPCErrorDetails{
				Reason: spb.ModifyRPCErrorDetails_UNSUPPORTED_PARAMS,
			})
		return
	}

	elec := s.getElection()
	elec.clientLatest = cs.lastElecID
	elec.client = cid

	for _, o := range ops {
		ni := o.GetNetworkInstance()
		if ni == "" {
			resCh <- &spb.ModifyResponse{
				Result: []*spb.AFTResult{{
					Id:     o.Id,
					Status: spb.AFTResult_FAILED,
					ErrorDetails: &spb.AFTErrorDetails{
						ErrorMessage: `invalid network instance name "" specified`,
					},
				}},
			}
		}
		if _, ok := s.masterRIB.NetworkInstanceRIB(ni); !ok {
			// this is an unknown network instance, we should not return
			// an error to the client since we do not want the connection
			// to be torn down.
			log.Errorf("rejected operation %s since it is an unknown network-instance, %s", o, ni)
			resCh <- &spb.ModifyResponse{
				Result: []*spb.AFTResult{{
					Id:     o.Id,
					Status: spb.AFTResult_FAILED,
					ErrorDetails: &spb.AFTErrorDetails{
						ErrorMessage: fmt.Sprintf(`unknown network instance "%s" specified`, ni),
					},
				}},
			}
			return
		}

		// We do not try and modify entries within the operation in parallel
		// with each other since this may cause us to duplicate ACK on particular
		// operations - for example, if there are two next-hops that are within a
		// single next-hop-group, then both of them will cause the NHG to be
		// installed in the RIB. If we do this then we might end up ACKing
		// one twice if there was >1 different entry that mde a NHG resolvable.
		// For a SINGLE_PRIMARY client serialising into a single Modify channel
		// ensures that we do not end up with this occuring - but going forward
		// for ALL_PRIMARY this situation will need to handled likely by creating
		// some form of lock on each transaction as it is attempted, or building
		// a more intelligent RIB structure to track missing dependencies.
		res, err := modifyEntry(s.masterRIB, ni, o, cs.params.FIBAck, elec)
		switch {
		case err != nil:
			errCh <- err
		default:
			resCh <- res
		}
	}

}

// electionDetails provides a summary of a single election from the perspective of one client.
type electionDetails struct {
	// master is the clientID of the client that is master after the election.
	master string
	// electionID is the ID of the latest election.
	ID *spb.Uint128
	// client is the ID of the client that the query is being done on behalf of.
	client string
	// clientLatest is the latest electionID that the client provided us with.
	clientLatest *spb.Uint128
}

// modifyEntry performs the specified modify operation, op, on the RIB, r, within the network
// instance ni. The client's request ACK mode is specified by fibACK. The details of the
// current election on the server is described in election.
// The results are returned as a ModifyResponse and an error which must be a status.Status.
func modifyEntry(r *rib.RIB, ni string, op *spb.AFTOperation, fibACK bool, election *electionDetails) (*spb.ModifyResponse, error) {
	if op == nil {
		return nil, status.Newf(codes.Internal, "invalid nil operation received").Err()
	}

	res, ok, err := checkElectionForModify(op.Id, op.ElectionId, election)
	if err != nil {
		return nil, err
	}
	if !ok {
		return res, err
	}

	if r == nil {
		return nil, status.New(codes.Internal, "invalid RIB state").Err()
	}

	niR, ok := r.NetworkInstanceRIB(ni)
	if !ok || !niR.IsValid() {
		return nil, status.Newf(codes.Internal, "invalid RIB state for network instance name: '%s'", ni).Err()
	}

	results := []*spb.AFTResult{}
	okACK := spb.AFTResult_RIB_PROGRAMMED
	// TODO(robjs): today we just say anything that hit
	// the RIB hit the FIB. We need to add a feedback loop
	// that checks this.
	if fibACK {
		okACK = spb.AFTResult_FIB_PROGRAMMED
	}

	var (
		oks, faileds []*rib.OpResult
		ribFatalErr  error
	)

	switch op.Op {
	case spb.AFTOperation_ADD, spb.AFTOperation_REPLACE:
		// AddEntry handles replaces, since an ADD can be an explicit replace. It checks
		// whether the entry was an explicit replace from the op, and if so errors if the
		// entry does not already exist.
		log.V(2).Infof("calling AddEntry for operation ID %d", op.GetId())
		oks, faileds, ribFatalErr = r.AddEntry(ni, op)
	case spb.AFTOperation_DELETE:
		oks, faileds, ribFatalErr = r.DeleteEntry(ni, op)
	default:
		return &spb.ModifyResponse{
			Result: []*spb.AFTResult{
				{
					Id:     op.GetId(),
					Status: spb.AFTResult_FAILED,
					ErrorDetails: &spb.AFTErrorDetails{
						ErrorMessage: fmt.Sprintf("unsupported operation type supplied, %s", op.Op),
					},
				},
			},
		}, nil
	}

	if ribFatalErr != nil {
		// RIB action returned fatal error for the connection.
		return nil, addModifyErrDetailsOrReturn(
			status.Newf(codes.Unimplemented, "fatal error processing operation %s, error: %v", op.Op, ribFatalErr),
			&spb.ModifyRPCErrorDetails{
				Reason: spb.ModifyRPCErrorDetails_UNKNOWN,
			},
		)
	}

	for _, ok := range oks {
		log.V(2).Infof("received OK for %d in operation %s", ok.ID, prototext.Format(op))
		results = append(results, &spb.AFTResult{
			Id:     ok.ID,
			Status: okACK,
		})
	}

	for _, fail := range faileds {
		log.Errorf("returning failed to client because the RIB declared it failed, %v", fail)
		results = append(results, &spb.AFTResult{
			Id:     fail.ID,
			Status: spb.AFTResult_FAILED,
			// TODO(robjs): add somewhere for the error that we provide to be
			// returned.
		})
	}

	return &spb.ModifyResponse{
		Result: results,
	}, nil
}

// checkElectionForModify checks whether the operation with ID opID, and election ID opElecID
// with the server that has the election context described by election should proceed. It returns
//  - a ModifyResponse which is to be sent to the client
//  - a bool determining whether the modify should proceed
//  - an error that is returned to the client.
// The bool is set to true in the case that a non-fatal error (e.g., an error whereby the
// operation is just ignored) is encountered.
// Any returned error is considered fatal to the Modify RPC and can be sent directly back
// to the client.
func checkElectionForModify(opID uint64, opElecID *spb.Uint128, election *electionDetails) (*spb.ModifyResponse, bool, error) {
	// check whether the election ID is the current one.
	if opElecID == nil {
		// this is an error since we only support election IDs.
		return nil, false, addModifyErrDetailsOrReturn(
			status.Newf(codes.FailedPrecondition, "no specified election ID when it was required"),
			&spb.ModifyRPCErrorDetails{
				// TODO(robjs): we probably need to define what happens here, no specified error code.
			})
	}

	switch {
	case election == nil, election.master == "", election.ID == nil:
		return nil, false, status.Newf(codes.Internal, "invalid election state in server, details of election: %+v", election).Err()
	case election.clientLatest == nil:
		// This client might not have sent us an election ID yet, which means that they are in
		// the wrong.
		return nil, false, addModifyErrDetailsOrReturn(
			status.Newf(codes.FailedPrecondition, "client has not yet specified an election ID"),
			&spb.ModifyRPCErrorDetails{},
		)
	case election.client != election.master:
		// this client is not the elected master.
		log.Errorf("returning failed to client %s (id: %s), because they are not the elected master (%s is, id: %s)", election.client, election.clientLatest, election.master, election.ID)
		return &spb.ModifyResponse{
			Result: []*spb.AFTResult{{
				Id:     opID,
				Status: spb.AFTResult_FAILED,
			}},
		}, false, nil
	}

	thisID := uint128.New(opElecID.Low, opElecID.High)
	// check that this client sent us the same ID as we had before.
	currentClientID := uint128.New(election.clientLatest.Low, election.clientLatest.High)
	if thisID.Cmp(currentClientID) != 0 {
		log.Errorf("returning failed to client because operation election ID %s != their latest election ID %s (master is: %s with ID %s)", opElecID, election.clientLatest, election.master, election.ID)
		return &spb.ModifyResponse{
			Result: []*spb.AFTResult{{
				Id:     opID,
				Status: spb.AFTResult_FAILED,
			}},
		}, false, nil
	}

	// This is a belt and braces check -- it's not clear that we need to do it. Since we
	// checked that the master that is stored in the server is this client. However, we do
	// an additional check to ensure that the current ID that we are storing is definitely
	// the same as the one that we just received.
	currentID := uint128.New(election.ID.Low, election.ID.High)
	switch {
	case thisID.Cmp(currentID) > 0:
		// this value is greater than the known master ID. Return an error
		return nil, false, status.Newf(codes.FailedPrecondition, "specified election ID was greater than existing election, %s > %s", thisID, currentID).Err()
	case thisID.Cmp(currentID) < 0:
		// this value is less as the current master ID, ignore this transaction.
		// note that since we don't respond here, the client at the other side
		// is just going to have a pending transaction forever.
		//
		// TODO(robjs): should we add an error code here?
		log.Errorf("returning failed to client because operation election ID %s < the master election ID %s (master: %s)", opElecID, election.ID, election.master)
		return &spb.ModifyResponse{
			Result: []*spb.AFTResult{{
				Id:     opID,
				Status: spb.AFTResult_FAILED,
			}},
		}, false, nil
	}
	return nil, true, nil
}

// doGet implents the Get RPC for the gRIBI server. It handles the input GetRequest, writing
// the set of GetResponses to the specified msgCh. When the Get is done, the function writes to
// doneCh such that the caller knows that the work that is being done is complete. If a message
// is received on stopCh the function returns. Any errors that are experienced are written to
// errCh.
func (s *Server) doGet(req *spb.GetRequest, msgCh chan *spb.GetResponse, doneCh, stopCh chan struct{}, errCh chan error) {
	// Any time we return we return we tell the done channel that we're complete.
	defer func() {
		doneCh <- struct{}{}
	}()

	if req == nil {
		errCh <- status.Errorf(codes.InvalidArgument, "invalid nil GetRequest received")
		return
	}

	netInstances := []string{}
	switch nireq := req.NetworkInstance.(type) {
	case *spb.GetRequest_Name:
		if nireq.Name == "" {
			errCh <- status.Errorf(codes.InvalidArgument, `invalid string "" returned for NetworkInstance name in GetRequest`)
			return
		}
		netInstances = append(netInstances, nireq.Name)
	case *spb.GetRequest_All:
		netInstances = s.masterRIB.KnownNetworkInstances()
	}

	filter := map[spb.AFTType]bool{}
	switch v := req.Aft; v {
	case spb.AFTType_ALL, spb.AFTType_IPV4, spb.AFTType_NEXTHOP, spb.AFTType_NEXTHOP_GROUP:
		filter[v] = true
	default:
		errCh <- status.Errorf(codes.Unimplemented, "AFTs other than IPv4, IPv6, NHG and NH are unimplemented, requested: %s", v)
	}

	for _, ni := range netInstances {
		netInst, ok := s.masterRIB.NetworkInstanceRIB(ni)
		if !ok {
			errCh <- status.Errorf(codes.InvalidArgument, "invalid network instance %s specified", ni)
			return
		}

		if err := netInst.GetRIB(filter, msgCh, stopCh); err != nil {
			errCh <- err
			return
		}
	}
}

// addFlushErrDetailsOrReturn
func addFlushErrDetailsOrReturn(s *status.Status, d *spb.FlushResponseError) error {
	se, err := s.WithDetails(d)
	if err != nil {
		return s.Err()
	}
	return se.Err()
}

// checkFlushRequest ensures that the FlushRequest that was supplied from a client is valid - particularly,
// validating that the election parameters are consistent, and correct, and that the Flush should be
// completed.
func (s *Server) checkFlushRequest(req *spb.FlushRequest) error {
	if req.GetNetworkInstance() == nil {
		return addFlushErrDetailsOrReturn(status.Newf(codes.FailedPrecondition, "unspecified network instance"), &spb.FlushResponseError{
			Status: spb.FlushResponseError_UNSPECIFIED_NETWORK_INSTANCE,
		})
	}
	if req.GetOverride() != nil {
		// The election ID should not be compared, regardless of whether
		// there are SINGLE_PRIMARY clients on the server.
		return nil
	}

	id := req.GetId()
	switch {
	case id == nil && s.curElecID == nil:
		// We are in ALL_PRIMARY mode and not given an election ID, which is fine.
		return nil
	case id == nil && s.curElecID != nil:
		// We are in SINGLE_PRIMARY mode but we were not given an election behaviour.
		return addFlushErrDetailsOrReturn(status.Newf(codes.FailedPrecondition, "unsupported election behaviour, client in SINGLE_PRIMARY mode"), &spb.FlushResponseError{
			Status: spb.FlushResponseError_UNSPECIFIED_ELECTION_BEHAVIOR,
		})
	case id != nil && s.curElecID == nil:
		return addFlushErrDetailsOrReturn(status.Newf(codes.FailedPrecondition, "received election ID in ALL_PRIMARY mode"), &spb.FlushResponseError{
			Status: spb.FlushResponseError_ELECTION_ID_IN_ALL_PRIMARY,
		})
	}

	// If the Flush specified an ID, then we need to check that it is valid according
	// to the logic that is defined in the specification - it must be either equal to
	// or higher than the value that is the current master,
	candidate := uint128.New(id.Low, id.High)

	if uint128.New(0, 0).Equals(candidate) {
		return addFlushErrDetailsOrReturn(status.Newf(codes.InvalidArgument, "zero is an invalid election ID"), &spb.FlushResponseError{
			Status: spb.FlushResponseError_INVALID_ELECTION_ID,
		})
	}

	existing := uint128.New(s.curElecID.Low, s.curElecID.High)
	if candidate.Cmp(existing) < 0 {
		return addFlushErrDetailsOrReturn(status.Newf(codes.FailedPrecondition, "election ID specified (%v) is not primary", candidate), &spb.FlushResponseError{
			Status: spb.FlushResponseError_NOT_PRIMARY,
		})
	}

	return nil
}

// FakeServer is a wrapper around the server with functions to enable testing
// to be performed more easily, for example, injecting specific state.
type FakeServer struct {
	*Server
}

// NewFake returns a new version of the fake server. This implementation wraps
// the Server implementation with functions to insert specific state into
// the server without a need to use the public APIs.
func NewFake(opt ...ServerOpt) (*FakeServer, error) {
	s, err := New(opt...)
	if err != nil {
		return nil, err
	}
	return &FakeServer{Server: s}, nil
}

// InjectRIB allows a client to inject a RIB into the server as though it was
// received from the master client.
func (f *FakeServer) InjectRIB(r *rib.RIB) {
	f.Server.masterRIB = r
}

// InjectElectionID allows a client to set an initial election ID on the server as
// though it was received from the client.
func (f *FakeServer) InjectElectionID(id *spb.Uint128) {
	f.Server.curElecID = id
}

// Package server defines the basic elements of a gRIBI server that uses
// an in-memory data store for the AFT contents.
package server

import (
	"fmt"
	"io"
	"sync"

	log "github.com/golang/glog"
	"github.com/google/uuid"
	"github.com/openconfig/gribigo/rib"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"lukechampine.com/uint128"

	spb "github.com/openconfig/gribi/v1/proto/service"
)

const (
	// DefaultNIName specifies the name of the default network instance on the system.
	DefaultNIName = "DEFAULT"
)

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

// ServerOpt is an option that can be provided to the gRIBI Server.
type ServerOpt interface {
	isServerOpt()
}

// WithRIBHook specifies that the server should be created with a function which is called
// after every RIB change event.
func WithRIBHook(r rib.RIBHookFn) *ribHook {
	return &ribHook{fn: r}
}

// ribHook stores a function that is to be called on the RIB hook.
type ribHook struct {
	fn rib.RIBHookFn
}

// isServerOpt implements the ServerOpt interface.
func (*ribHook) isServerOpt() {}

// hasRIBHook returns the WithRIBHook function from the supplied ServerOpt slice,
// or nil if one does not exist.
func hasRIBHook(opts []ServerOpt) rib.RIBHookFn {
	for _, o := range opts {
		if v, ok := o.(*ribHook); ok {
			return v.fn
		}
	}
	return nil
}

// New creates a new gRIBI server.
func New(opts ...ServerOpt) *Server {
	r := rib.New(DefaultNIName)
	if f := hasRIBHook(opts); f != nil {
		r.SetHook(f)
	}

	return &Server{
		cs: map[string]*clientState{},
		// TODO(robjs): when we implement support for ALL_PRIMARY then we might not
		// want to create a new RIB by default.
		masterRIB: r,
	}
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
				return
			}
			if err != nil {
				errCh <- status.Errorf(codes.Unknown, "error reading message from client, %v", err)
			}
			log.V(2).Infof("received message %s on Modify channel", in)

			var (
				res       *spb.ModifyResponse
				skipWrite bool
			)

			switch {
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

	return <-errCh
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
		FIBAck:       p.GetAckType() == spb.SessionParameters_RIB_ACK,
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
	if cand.High == exist.High && cand.Low == exist.Low {
		return false, true, nil
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

	var wg sync.WaitGroup
	for _, o := range ops {
		switch o.Op {
		case spb.AFTOperation_ADD:
			ni := o.GetNetworkInstance()
			if ni == "" {
				ni = DefaultNIName
			}
			r, ok := s.masterRIB.NI(ni)
			if !ok {
				// this is an unknown network instance, we should not return
				// an error to the client since we do not want the connection
				// to be torn down.
				log.Errorf("rejected operation %s since it is an unknown network-instance, %s", o, ni)
				resCh <- &spb.ModifyResponse{
					Result: []*spb.AFTResult{{
						Id:     o.Id,
						Status: spb.AFTResult_FAILED,
					}},
				}
				return
			}
			wg.Add(1)
			go func(op *spb.AFTOperation) {
				res, err := addEntry(r, op, cs.params.FIBAck, elec)
				switch {
				case err != nil:
					errCh <- err
				default:
					resCh <- res
				}
				wg.Done()
			}(o)
		default:
			errCh <- addModifyErrDetailsOrReturn(
				status.Newf(codes.Unimplemented, "error processing operation %s, unsupported", o.Op),
				&spb.ModifyRPCErrorDetails{
					Reason: spb.ModifyRPCErrorDetails_UNKNOWN,
				},
			)
		}
	}
	wg.Wait()
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

// addEntry adds the specified entry op to the RIB, r. The client's requested ACK mode by fibACK. The
// details of the current election on the server is described in election.
// The results are returned as a SubscribeResponse and an error which must be a status.Status.
func addEntry(r *rib.RIBHolder, op *spb.AFTOperation, fibACK bool, election *electionDetails) (*spb.ModifyResponse, error) {
	if op == nil {
		return nil, status.Newf(codes.Internal, "invalid nil operation received").Err()
	}

	if r == nil || !r.IsValid() {
		return nil, status.Newf(codes.Internal, "invalid RIB state for network instance name: '%s'", op.GetNetworkInstance()).Err()
	}

	res, ok, err := checkElectionForModify(op.Id, op.ElectionId, election)
	if err != nil {
		return nil, err
	}
	if !ok {
		return res, err
	}

	result := &spb.AFTResult{Id: op.Id}
	switch t := op.GetEntry().(type) {
	case *spb.AFTOperation_Ipv4:
		err := r.AddIPv4(t.Ipv4)
		switch {
		case err != nil:
			log.Errorf("adding IPv4 entry failed, reason: %v", err)
			result.Status = spb.AFTResult_FAILED
		case fibACK:
			result.Status = spb.AFTResult_FIB_PROGRAMMED
		default:
			result.Status = spb.AFTResult_RIB_PROGRAMMED
		}
	default:
		return nil, status.Newf(codes.Unimplemented, "unsupported AFT operation type %T", t).Err()
	}

	return &spb.ModifyResponse{
		Result: []*spb.AFTResult{result},
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

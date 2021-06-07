// Package server defines the basic elements of a gRIBI server that uses
// an in-memory data store for the AFT contents.
package server

import (
	"io"
	"sync"

	log "github.com/golang/glog"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	spb "github.com/openconfig/gribi/v1/proto/service"
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

// New creates a new gRIBI server.
func New() *Server {
	return &Server{
		cs: map[string]*clientState{},
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

	// Store whether this is the first message on the channel, some options - like the session
	// parameters can only be set as the first message.
	var gotmsg bool
	for {
		in, err := ms.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return status.Errorf(codes.Unknown, "error reading message from client, %v", err)
		}
		log.V(2).Infof("received message %s on Modify channel", in)

		var res *spb.ModifyResponse
		switch {
		case in.Params != nil:
			var err error
			if res, err = s.checkParams(in.Params, gotmsg); err != nil {
				// Invalid parameters is a fatal error, so we take down the Modify RPC.
				return err
			}
			if err := s.updateParams(cid, in.Params); err != nil {
				// Not being able to update parameters is a fatal error, so we take down
				// the Modify RPC.
				return err
			}
		case in.ElectionId != nil:
			var err error
			res, err = s.runElection(cid, in.ElectionId)
			if err != nil {
				return err
			}
		default:
			return status.Errorf(codes.Unimplemented, "unimplemented handling of message %s", in)
		}

		// update that we have received at least one message.
		gotmsg = true
		if err := ms.Send(res); err != nil {
			return status.Errorf(codes.Internal, "cannot write message to client channel, %s", res)
		}

	}
}

// newClient creates a new client context within the server using the specified string
// ID.
func (s *Server) newClient(id string) error {
	s.csMu.Lock()
	defer s.csMu.Unlock()
	if s.cs[id] != nil {
		return status.Errorf(codes.Internal, "cannot create new client with duplicate ID, %s", id)
	}
	s.cs[id] = &clientState{}

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
	if p == nil {
		return status.Errorf(codes.Internal, "cannot update parameters for a client with no state, %s", id)
	}
	if p.params != nil {
		var err error
		me := status.New(codes.FailedPrecondition, "cannot modify SessionParameters")
		if me, err = me.WithDetails(&spb.ModifyRPCErrorDetails{
			Reason: spb.ModifyRPCErrorDetails_MODIFY_NOT_ALLOWED,
		}); err != nil {
			return me.Err()
		}
		return me.Err()
	}
	s.cs[id].params = cparam
	return nil
}

// checkParams validates that teh parameters that were supplied by the client are valid
// within the overall server context. It returns the ModifyResponse that should be sent
// to the client, or the populated error.
func (s *Server) checkParams(p *spb.SessionParameters, gotMsg bool) (*spb.ModifyResponse, error) {
	if gotMsg {
		return nil, status.Errorf(codes.FailedPrecondition, "must send SessionParameters as the first request on a Modify channel")
	}

	// TODO(robjs): Check that parameters are self consistent.
	// TODO(robjs): Check that the client is consistent with other clients on the system.

	return &spb.ModifyResponse{
		SessionParamsResult: &spb.SessionParametersResult{
			Status: spb.SessionParametersResult_OK,
		},
	}, nil
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

func (s *Server) runElection(id string, elecID *spb.Uint128) (*spb.ModifyResponse, error) {
	// TODO(robjs): check that the client is one that we actually need to do elections for.
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

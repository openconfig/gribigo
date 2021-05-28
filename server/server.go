// Package server defines the basic elements of a gRIBI server that uses
// an in-memory data store for the AFT contents.
package server

import (
	"io"
	"sync"

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
	// persist indicates whether the client's AFT entries should be
	// persisted even after the client disconnects.
	persist bool
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
	if err := s.newClient(uuid.New().String()); err != nil {
		return err
	}

	for {
		in, err := ms.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return status.Errorf(codes.Unknown, "error reading message from client, %v", err)
		}
		// TODO(robjs): implement message handling
		_ = in
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

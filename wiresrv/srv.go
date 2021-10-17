// Package wiresrv implements a server for the gRPC WireServer service. It implements
// a dataplane over gRPC - receiving packets in the form of a byte slice and handing
// them to a callback function.
//
// Currently, one wiresrv is required per "port" that is exposed - there is no
// multiplexing of streams.
package wiresrv

import (
	"io"

	log "github.com/golang/glog"
	"github.com/openconfig/gribigo/proto/wire"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Server struct {
	*wire.UnimplementedWireServerServer

	// h is a function called for each input packet received via the
	// wire service.
	h func([]byte) error

	// txQ is the queue of packets waiting to be transmitted.
	txQ chan []byte
}

// New returns a wire server with the specified handler function callback
// as the input.
func New(handler func([]byte) error) *Server {
	return &Server{
		h:   handler,
		txQ: make(chan []byte),
	}
}

func (s *Server) Stream(stream wire.WireServer_StreamServer) error {
	errCh := make(chan error)
	go func() {
		for {
			in, err := stream.Recv()
			if err == io.EOF {
				return
			}
			if err != nil {
				errCh <- status.Errorf(codes.Unknown, "error reading message from wire stream, %v", err)
			}

			if c := in.GetControl(); c != nil {
				log.Errorf("received unimplemented WireControl message, %v", c)
			}

			log.Infof("received message, %s", in)
			s.h(in.GetData().GetRaw())
		}
	}()

	// send a packet, write to server.
	go func() {
		for {
			pkt := <-s.txQ
			log.Infof("sending packet %v", pkt)
			if err := stream.Send(&wire.WireStream{
				Message: &wire.WireStream_Data{
					Data: &wire.Data{
						Raw: pkt,
					},
				},
			}); err != nil {
				errCh <- status.Errorf(codes.Internal, "could not send packet, %v", err)
				return
			}
		}
	}()

	return <-errCh
}

// TODO(robjs): implement a queue for the txQ.

func (s *Server) Q(pkt []byte) {
	log.Infof("enqueueing packet %v", pkt)
	s.txQ <- pkt
}

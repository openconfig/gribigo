// Package testcommon is a library which implements common helpers for testing gRIBI.
package testcommon

import (
	"fmt"
	"net"

	log "github.com/golang/glog"

	"github.com/openconfig/gribigo/server"
	"google.golang.org/grpc"

	spb "github.com/openconfig/gribi/v1/proto/service"
)

// Server starts a new gRIBI server using the fake from this repository and returns
// a function to start the server listening, a string indicating its listen address.
func Server() (func(), string) {
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		// We panic here since this allows the test code to be much cleaner :-)
		panic(fmt.Sprintf("cannot create server, %v", err))
	}

	s := grpc.NewServer()
	spb.RegisterGRIBIServer(s, server.New(nil))
	log.Infof("new server listening at %s", l.Addr().String())
	return func() {
		if err := s.Serve(l); err != nil {
			panic(fmt.Sprintf("server listen failed, %v", err))
		}
	}, l.Addr().String()
}

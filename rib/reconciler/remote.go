package reconciler

import (
	"context"
	"fmt"

	"github.com/openconfig/gribigo/client"
	"github.com/openconfig/gribigo/rib"

	spb "github.com/openconfig/gribi/v1/proto/service"
)

// RemoteRIB implements the RIBTarget interface and wraps a remote gRIBI RIB.
// The contents are accessed via the gRIBI gRPC API.
type RemoteRIB struct {
	c *client.Client

	defaultName string
}

// NewRemoteRIB returns a new remote gRIBI RIB. The context supplied is used to
// dial the remote gRIBI server at the address 'addr'. the 'defName' argument
// is used to identify the name of the default network instance on the server.
func NewRemoteRIB(ctx context.Context, defName, addr string) (*RemoteRIB, error) {
	gc, err := client.New()
	if err != nil {
		return nil, fmt.Errorf("cannot create gRIBI client, %v", err)
	}

	r := &RemoteRIB{
		c:           gc,
		defaultName: defName,
	}

	if err := r.c.Dial(ctx, addr); err != nil {
		return nil, fmt.Errorf("cannot dial remote server, %v", err)
	}
	return r, nil
}

// NewRemoteRIBWithStub creates a new remote RIB using the specified default network
// instance name, and the provided stub client. It returns an error if the
// client cannot be created.
func NewRemoteRIBWithStub(defName string, stub spb.GRIBIClient) (*RemoteRIB, error) {
	c, err := client.New()
	if err != nil {
		return nil, fmt.Errorf("cannot create gRIBI client, %v", err)
	}
	if err := c.UseStub(stub); err != nil {
		return nil, fmt.Errorf("cannot set gRIBI client within wrapper client, %v", err)
	}
	return &RemoteRIB{
		c:           c,
		defaultName: defName,
	}, nil
}

// CleanUp closes the remote connection to the gRIBI server.
func (r *RemoteRIB) CleanUp() {
	r.c.Close()
}

// Get retrieves the contents of the remote gRIBI server's RIB and returns it as a
// gRIBIgo RIB struct. The context is used for a Get RPC call to the remote server.
func (r *RemoteRIB) Get(ctx context.Context) (*rib.RIB, error) {
	resp, err := r.c.Get(ctx, &spb.GetRequest{
		NetworkInstance: &spb.GetRequest_All{
			All: &spb.Empty{},
		},
		Aft: spb.AFTType_ALL,
	})
	if err != nil {
		return nil, fmt.Errorf("cannot get remote RIB, %v", err)
	}

	// We always disable the RIB checking function because we want to see entries that have
	// not got valid references so that we can reconcile them.
	remRIB, err := rib.FromGetResponses(r.defaultName, []*spb.GetResponse{resp}, rib.DisableRIBCheckFn())
	if err != nil {
		return nil, fmt.Errorf("cannot build remote RIB from responses, %v", err)
	}

	return remRIB, nil
}

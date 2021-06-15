// Package fluent defines a fluent-style API for a gRIBI client that
// can be called from testing frameworks such as ONDATRA.
package fluent

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"testing"

	log "github.com/golang/glog"
	"github.com/openconfig/gribigo/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	aftpb "github.com/openconfig/gribi/v1/proto/gribi_aft"
	spb "github.com/openconfig/gribi/v1/proto/service"
	wpb "github.com/openconfig/ygot/proto/ywrapper"
)

// gRIBIClient stores internal state and arguments related to the gRIBI client
// that is exposed by the fluent API.
type gRIBIClient struct {
	// connection stores the configuration related to the connection.
	connection *gRIBIConnection
	// Internal state.
	c *client.Client

	// opCount is the count of AFTOperations that has been sent by the client,
	// used to populate the ID of AFTOperation messages automatically.
	opCount uint64
	// currentElectionID is the current electionID that the client should use.
	currentElectionID *spb.Uint128
}

type gRIBIConnection struct {
	// targetAddr stores the address that is to be dialed by the client.
	targetAddr string
	// redundMode specifies the redundancy mode that the client is using,
	// this is set only at initialisation time and cannot be changed during
	// the lifetime of the session.
	redundMode RedundancyMode
	// electionID specifies the initial election ID for the client.
	electionID *spb.Uint128
	// persist indicates whether the client requests that the server persists
	// entries after it disconnects.
	persist bool

	// parent is a pointer to the parent of the gRIBIConnection.
	parent *gRIBIClient
}

// NewClient returns a new gRIBI client instance, and is an entrypoint to this
// package.
func NewClient() *gRIBIClient {
	return &gRIBIClient{
		opCount: 1,
	}
}

// Connection allows any parameters relating to gRIBI connections to be set through
// the gRIBI fluent API.
func (g *gRIBIClient) Connection() *gRIBIConnection {
	c := &gRIBIConnection{parent: g}
	g.connection = c
	return c
}

// WithTarget specifies the gRIBI target (server) address that is to be dialed,
// the addr argument supplied is the address of the server specified in the standard
// form of address:port.
func (g *gRIBIConnection) WithTarget(addr string) *gRIBIConnection {
	g.targetAddr = addr
	return g
}

func (g *gRIBIConnection) WithPersistence() *gRIBIConnection {
	g.persist = true
	return g
}

// RedundancyMode is a type used to indicate the redundancy modes supported in gRIBI.
type RedundancyMode int64

const (
	_ RedundancyMode = iota
	// AllPrimaryClients indicates that all clients should be treated as a primary
	// and the server should do reference counting and bestpath selection between
	// them using the standard mechansisms defined in gRIBI.
	AllPrimaryClients
	// ElectedPrimaryClient indicates that this client is treated as part of an
	// elected set of clients that have an external election process that assigns
	// a uint128 election ID.
	ElectedPrimaryClient
)

// WithRedundancyMode specifies the redundancy mode that is to be used by the client
// from the enumerated types specified in the RedundancyMode.
func (g *gRIBIConnection) WithRedundancyMode(m RedundancyMode) *gRIBIConnection {
	g.redundMode = m
	return g
}

// WithInitialElectionID specifies the election ID that is to be used to start the
// connection. It is not sent until a Modify RPC has been opened to the client. The
// arguments specify the high and low 64-bit integers that from the uint128.
func (g *gRIBIConnection) WithInitialElectionID(low, high uint64) *gRIBIConnection {
	eid := &spb.Uint128{
		Low:  low,
		High: high,
	}
	g.electionID = eid
	// also set the current election ID for subsequent operations.
	g.parent.currentElectionID = eid
	return g
}

// Start connects the gRIBI client to the target using the specified context as
// the connection parameters. The dial to the target is blocking, so Start() will
// not return until a connection is successfully made. Any error in parsing the
// specified arguments required for connections is raised using the supplied
// testing.TB.
func (g *gRIBIClient) Start(ctx context.Context, t testing.TB) {
	flag.Parse()
	t.Helper()
	if g.connection.targetAddr == "" {
		t.Fatalf("cannot dial without specifying target address")
	}

	opts := []client.Opt{}
	switch g.connection.redundMode {
	case AllPrimaryClients:
		opts = append(opts, client.AllPrimaryClients())
	case ElectedPrimaryClient:
		if g.connection.electionID == nil {
			t.Fatalf("client must specify Election ID in elected primary mode")
		}
		opts = append(opts, client.ElectedPrimaryClient(g.connection.electionID))
	}

	if g.connection.persist {
		opts = append(opts, client.PersistEntries())
	}

	log.V(2).Infof("setting client parameters to %+v", opts)
	c, err := client.New(opts...)
	if err != nil {
		t.Fatalf("cannot create new client, %v", err)
	}
	g.c = c

	log.V(2).Infof("dialing %s", g.connection.targetAddr)
	if err := c.Dial(ctx, g.connection.targetAddr); err != nil {
		t.Fatalf("cannot dial target, %v", err)
	}
}

// StartSending specifies that the Modify stream to the target should be made, and
// the client should start to send any queued messages to the target. Any error
// encountered is reported using the supplied testing.TB.
func (g *gRIBIClient) StartSending(ctx context.Context, t testing.TB) {
	t.Helper()
	if err := g.c.Connect(ctx); err != nil {
		t.Fatalf("cannot connect Modify request, %v", err)
	}
	g.c.StartSending()
}

// Await waits until the underlying gRIBI client has completed its work to return -
// complete is defined as both the send and pending queue being empty, or an error
// being hit by the client. It returns an error in the case that there were errors
// reported.
func (g *gRIBIClient) Await(ctx context.Context, t testing.TB) error {
	if err := g.c.AwaitConverged(); err != nil {
		return err
	}
	return nil
}

// Results returns the transaction results from the client. If the client is not converged
// it will return a partial set of results from transactions that have completed, otherwise
// it will return the complete set of results received from the server.
func (g *gRIBIClient) Results(t testing.TB) []*client.OpResult {
	r, err := g.c.Results()
	if err != nil {
		t.Fatalf("did not get valid results, %v", err)
	}
	return r
}

// TODO(robjs): add an UpdateElectionID method such that we can set the election ID
// after it has initially been created.

// Modify wraps methods that trigger operations within the gRIBI Modify RPC.
func (g *gRIBIClient) Modify() *gRIBIModify {
	return &gRIBIModify{parent: g}
}

// gRIBIModify provides a wrapper for methods associated with the gRIBI Modify RPC.
type gRIBIModify struct {
	// parent is a pointer to the parent of the gRIBI modify.
	parent *gRIBIClient
}

// AddEntry creates an operation adding a new entry to the server.
func (g *gRIBIModify) AddEntry(t testing.TB, entries ...gRIBIEntry) *gRIBIModify {
	m, err := g.entriesToModifyRequest(spb.AFTOperation_ADD, entries)
	if err != nil {
		t.Fatalf("cannot build modify request: %v", err)
	}
	g.parent.c.Q(m)
	return g
}

// entriesToModifyRequest creates a ModifyRequest from a set of input entries.
func (g *gRIBIModify) entriesToModifyRequest(op spb.AFTOperation_Operation, entries []gRIBIEntry) (*spb.ModifyRequest, error) {
	m := &spb.ModifyRequest{}
	for _, e := range entries {
		ep, err := e.proto()
		if err != nil {
			return nil, fmt.Errorf("cannot build entry protobuf, got err: %v", err)
		}
		ep.Op = op

		// ID was unset, so use the library maintained count. Note, that clients
		// should not use both explictly and manually specified IDs. To this end
		// initially we do not allow this API to be used for anything other than
		// automatically set values.
		if ep.Id != 0 {
			return nil, fmt.Errorf("cannot use explicitly set operation IDs for a message, got: %d, want: 0", ep.Id)
		}

		ep.Id = g.parent.opCount
		g.parent.opCount++

		if g.parent == nil {
			return nil, errors.New("invalid nil parent")
		}

		// If the election ID wasn't explicitly set then write the current one
		// to the message if this is a client that requires it.
		if g.parent.connection != nil && g.parent.connection.redundMode == ElectedPrimaryClient && ep.ElectionId == nil {
			ep.ElectionId = g.parent.currentElectionID
		}

		m.Operation = append(m.Operation, ep)
	}
	return m, nil
}

// gRIBIEntry is an entry implemented for all types that can be returned
// as a gRIBI entry.
type gRIBIEntry interface {
	// proto returns the specified entry as an AFTOperation protobuf.
	proto() (*spb.AFTOperation, error)
}

type ipv4Entry struct {
	// pb is the gRIBI IPv4Entry that is being composed.
	pb *aftpb.Afts_Ipv4EntryKey
	// ni is the network instance to which the IPv4Entry is applied.
	ni string
}

// IPv4Entry returns a new gRIBI IPv4Entry builder.
func IPv4Entry() *ipv4Entry {
	return &ipv4Entry{
		pb: &aftpb.Afts_Ipv4EntryKey{
			Ipv4Entry: &aftpb.Afts_Ipv4Entry{},
		},
	}
}

// WithPrefix sets the prefix of the IPv4Entry to the specified value, which
// must be a valid IPv4 prefix in the form prefix/mask.
func (i *ipv4Entry) WithPrefix(p string) *ipv4Entry {
	i.pb.Prefix = p
	return i
}

// WithNetworkInstance specifies the network instance to which the IPv4Entry
// is being applied.
func (i *ipv4Entry) WithNetworkInstance(n string) *ipv4Entry {
	i.ni = n
	return i
}

// WithNextHopGroup specifies the next-hop group that the IPv4Entry points to.
func (i *ipv4Entry) WithNextHopGroup(u uint64) *ipv4Entry {
	i.pb.Ipv4Entry.NextHopGroup = &wpb.UintValue{Value: u}
	return i
}

// proto implements the gRIBIEntry interface, returning a gRIBI AFTOperation. ID
// and ElectionID are explicitly not populated such that they can be populated by
// the function (e.g., AddEntry) to which they are an argument.
func (i *ipv4Entry) proto() (*spb.AFTOperation, error) {
	return &spb.AFTOperation{
		NetworkInstance: i.ni,
		Entry: &spb.AFTOperation_Ipv4{
			Ipv4: i.pb,
		},
	}, nil
}

// modifyError is a type that can be used to build a gRIBI Modify error.
type modifyError struct {
	Reason spb.ModifyRPCErrorDetails_Reason
	Code   codes.Code
}

// ModifyError allows a gRIBI ModifyError to be constructed.
func ModifyError() *modifyError {
	return &modifyError{}
}

// ModifyErrReason is a type used to express reasons for errors within the Modify RPC.
type ModifyErrReason int64

const (
	_ ModifyErrReason = iota
	// Unsupported parameters indicates that the server does not support the client parameters.
	UnsupportedParameters
	// ModifyParamsNotAllowed indicates that the client tried to modify the parameters after they
	// were set.
	ModifyParamsNotAllowed
	// ParamsDiffereFromOtherClients indicates that the parameters specified are inconsistent
	// with other clients that are connected to the server.
	ParamsDifferFromOtherClients
	// ElectionIDNotAllowed indicates that a client tried to send an election ID in a context
	// within which it was not allowed.
	ElectionIDNotAllowed
)

var reasonMap = map[ModifyErrReason]spb.ModifyRPCErrorDetails_Reason{
	UnsupportedParameters:        spb.ModifyRPCErrorDetails_UNSUPPORTED_PARAMS,
	ModifyParamsNotAllowed:       spb.ModifyRPCErrorDetails_MODIFY_NOT_ALLOWED,
	ParamsDifferFromOtherClients: spb.ModifyRPCErrorDetails_PARAMS_DIFFER_FROM_OTHER_CLIENTS,
	ElectionIDNotAllowed:         spb.ModifyRPCErrorDetails_ELECTION_ID_IN_ALL_PRIMARY,
}

// WithReason specifies the reason for the modify error from the enumeration
// in the protobuf.
func (m *modifyError) WithReason(r ModifyErrReason) *modifyError {
	m.Reason = reasonMap[r]
	return m
}

// WithCode specifies the well known code that is expected in the error.
func (m *modifyError) WithCode(c codes.Code) *modifyError {
	m.Code = c
	return m
}

// AsStatus returns the modifyError as a status.Status.
func (m *modifyError) AsStatus(t testing.TB) *status.Status {
	s := status.New(m.Code, "")
	var err error
	s, err = s.WithDetails(&spb.ModifyRPCErrorDetails{
		Reason: m.Reason,
	})
	if err != nil {
		t.Fatalf("cannot build error, %v", err)
	}
	return s
}

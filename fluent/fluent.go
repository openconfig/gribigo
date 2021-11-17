// Copyright 2021 Google LLC
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

// Package fluent defines a fluent-style API for a gRIBI client that
// can be called from testing frameworks such as ONDATRA.
package fluent

import (
	"context"
	"errors"
	"fmt"
	"testing"

	log "github.com/golang/glog"
	"github.com/openconfig/gribigo/client"
	"github.com/openconfig/gribigo/constants"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	aftpb "github.com/openconfig/gribi/v1/proto/gribi_aft"
	enums "github.com/openconfig/gribi/v1/proto/gribi_aft/enums"
	spb "github.com/openconfig/gribi/v1/proto/service"
	wpb "github.com/openconfig/ygot/proto/ywrapper"
)

// GRIBIClient stores internal state and arguments related to the gRIBI client
// that is exposed by the fluent API.
type GRIBIClient struct {
	// connection stores the configuration related to the connection.
	connection *gRIBIConnection
	// Internal state.
	c *client.Client
	// ctx is the context being used to dial.
	ctx context.Context

	// opCount is the count of AFTOperations that has been sent by the client,
	// used to populate the ID of AFTOperation messages automatically.
	opCount uint64
	// currentElectionID is the current electionID that the client should use.
	currentElectionID *spb.Uint128
}

type gRIBIConnection struct {
	// targetAddr stores the address that is to be dialed by the client.
	targetAddr string
	// stub is a gRPC GRIBIClient stub implementation that could be used
	// alternatively in lieu of targetAddr.
	stub spb.GRIBIClient
	// redundMode specifies the redundancy mode that the client is using,
	// this is set only at initialisation time and cannot be changed during
	// the lifetime of the session.
	redundMode RedundancyMode
	// electionID specifies the initial election ID for the client.
	electionID *spb.Uint128
	// persist indicates whether the client requests that the server persists
	// entries after it disconnects.
	persist bool
	// fibACK indicates whether the client requests that the server sends
	// a FIB ACK rather than a RIB ACK.
	fibACK bool

	// parent is a pointer to the parent of the gRIBIConnection.
	parent *GRIBIClient
}

// NewClient returns a new gRIBI client instance, and is an entrypoint to this
// package.
func NewClient() *GRIBIClient {
	return &GRIBIClient{}
}

// Connection allows any parameters relating to gRIBI connections to be set through
// the gRIBI fluent API.
func (g *GRIBIClient) Connection() *gRIBIConnection {
	if g.connection != nil {
		return g.connection
	}
	c := &gRIBIConnection{parent: g}
	g.connection = c
	return c
}

// WithTarget specifies the gRIBI target (server) address that is to be dialed,
// the addr argument supplied is the address of the server specified in the standard
// form of address:port.
func (g *gRIBIConnection) WithTarget(addr string) *gRIBIConnection {
	g.targetAddr = addr
	g.stub = nil
	return g
}

// WithStub specifies the gRPC GRIBIClient stub for use with this
// connection, in lieu of a gRIBI target (server) address.
func (g *gRIBIConnection) WithStub(stub spb.GRIBIClient) *gRIBIConnection {
	g.targetAddr = ""
	g.stub = stub
	return g
}

// WithPersistence specifies that the gRIBI server should maintain the RIB
// state after the client disconnects.
func (g *gRIBIConnection) WithPersistence() *gRIBIConnection {
	g.persist = true
	return g
}

// WithFIBACK indicates that the gRIBI server should send an ACK after the
// entry has been programmed into the FIB.
func (g *gRIBIConnection) WithFIBACK() *gRIBIConnection {
	g.fibACK = true
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
func (g *GRIBIClient) Start(ctx context.Context, t testing.TB) {
	t.Helper()
	if c := g.connection; c.targetAddr == "" && c.stub == nil {
		t.Fatalf("cannot dial without specifying target address or stub")
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

	if g.connection.fibACK {
		opts = append(opts, client.FIBACK())
	}

	log.V(2).Infof("setting client parameters to %+v", opts)
	c, err := client.New(opts...)
	if err != nil {
		t.Fatalf("cannot create new client, %v", err)
	}
	g.c = c

	if g.connection.stub != nil {
		log.V(2).Infof("using stub %#v", g.connection.stub)
		c.UseStub(g.connection.stub)
	} else {
		log.V(2).Infof("dialing %s", g.connection.targetAddr)
		if err := c.Dial(ctx, g.connection.targetAddr); err != nil {
			t.Fatalf("cannot dial target, %v", err)
		}
	}

	g.ctx = ctx
}

// Stop specifies that the gRIBI client should stop sending operations,
// and subsequently disconnect from the server.
func (g *GRIBIClient) Stop(t testing.TB) {
	g.c.StopSending()
	if err := g.c.Close(); err != nil {
		t.Fatalf("cannot disconnect from server, %v", err)
	}
}

// StartSending specifies that the Modify stream to the target should be made, and
// the client should start to send any queued messages to the target. Any error
// encountered is reported using the supplied testing.TB.
func (g *GRIBIClient) StartSending(ctx context.Context, t testing.TB) {
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
func (g *GRIBIClient) Await(ctx context.Context, t testing.TB) error {
	if err := g.c.AwaitConverged(ctx); err != nil {
		return err
	}
	return nil
}

// Results returns the transaction results from the client. If the client is not converged
// it will return a partial set of results from transactions that have completed, otherwise
// it will return the complete set of results received from the server.
func (g *GRIBIClient) Results(t testing.TB) []*client.OpResult {
	r, err := g.c.Results()
	if err != nil {
		t.Fatalf("did not get valid results, %v", err)
	}
	return r
}

// Status returns the status of the client. It can be used to check whether there pending
// operations or whether errors have occurred in the client.
func (g *GRIBIClient) Status(t testing.TB) *client.ClientStatus {
	s, err := g.c.Status()
	if err != nil {
		t.Fatalf("did not get valid status, %v", err)
	}
	return s
}

// gRIBIGet is a container for arguments to the Get RPC.
type gRIBIGet struct {
	// parent is a reference to the parent client.
	parent *GRIBIClient
	// pb is the GetRequest protobuf.
	pb *spb.GetRequest
}

// Get is a wrapper for the gRIBI Get RPC which is used to retrieve the current
// entries that are in the active gRIBI RIB. Get returns only entries that have
// been succesfully installed according to the request's ACK type. It can be filtered
// according to network instance and AFT.
func (g *GRIBIClient) Get() *gRIBIGet {
	return &gRIBIGet{
		parent: g,
		pb:     &spb.GetRequest{},
	}
}

// AllNetworkInstance requests entries from all network instances.
func (g *gRIBIGet) AllNetworkInstances() *gRIBIGet {
	g.pb.NetworkInstance = &spb.GetRequest_All{
		All: &spb.Empty{},
	}
	return g
}

// WithNetworkInstance requests the specific network instance, ni, with the Get
// request.
func (g *gRIBIGet) WithNetworkInstance(ni string) *gRIBIGet {
	g.pb.NetworkInstance = &spb.GetRequest_Name{
		Name: ni,
	}
	return g
}

// AFT is an enumerated type describing the AFTs available within gRIBI.
type AFT int64

const (
	_ AFT = iota
	AllAFTs
	// IPv4 references the IPv4Entry AFT.
	IPv4
	// NextHopGroup references the NextHopGroupEntry AFT.
	NextHopGroup
	// NextHop references the NextHop AFT.
	NextHop
)

// aftMap provides mapping between the AFT enumerated type within the fluent
// package and that within the gRIBI protobuf.
var aftMap = map[AFT]spb.AFTType{
	AllAFTs:      spb.AFTType_ALL,
	IPv4:         spb.AFTType_IPV4,
	NextHopGroup: spb.AFTType_NEXTHOP_GROUP,
	NextHop:      spb.AFTType_NEXTHOP,
}

// WithAFT specifies the AFT for which the Get request is made. The AllAFTs
// value can be used to retrieve all AFTs.
func (g *gRIBIGet) WithAFT(a AFT) *gRIBIGet {
	g.pb.Aft = aftMap[a]
	return g
}

// Send issues Get RPC to the target and returns the results.
func (g *gRIBIGet) Send() (*spb.GetResponse, error) {
	return g.parent.c.Get(g.parent.ctx, g.pb)
}

// Modify wraps methods that trigger operations within the gRIBI Modify RPC.
func (g *GRIBIClient) Modify() *gRIBIModify {
	return &gRIBIModify{parent: g}
}

// gRIBIModify provides a wrapper for methods associated with the gRIBI Modify RPC.
type gRIBIModify struct {
	// parent is a pointer to the parent of the gRIBI modify.
	parent *GRIBIClient
}

// AddEntry creates an operation adding the set of entries specified to the server.
func (g *gRIBIModify) AddEntry(t testing.TB, entries ...GRIBIEntry) *gRIBIModify {
	m, err := g.entriesToModifyRequest(spb.AFTOperation_ADD, entries)
	if err != nil {
		t.Fatalf("cannot build modify request: %v", err)
	}
	g.parent.c.Q(m)
	return g
}

// DeleteEntry creates an operation deleting the set of entries specified from the server.
func (g *gRIBIModify) DeleteEntry(t testing.TB, entries ...GRIBIEntry) *gRIBIModify {
	m, err := g.entriesToModifyRequest(spb.AFTOperation_DELETE, entries)
	if err != nil {
		t.Fatalf("cannot build modify request, %v", err)
	}
	g.parent.c.Q(m)
	return g
}

// ReplaceEntry creates an operation replacing the set of entries specified on the server.
func (g *gRIBIModify) ReplaceEntry(t testing.TB, entries ...GRIBIEntry) *gRIBIModify {
	m, err := g.entriesToModifyRequest(spb.AFTOperation_REPLACE, entries)
	if err != nil {
		t.Fatalf("cannot build modify request, %v", err)
	}
	g.parent.c.Q(m)
	return g
}

// UpdateElectionID updates the election ID on the gRIBI Modify channel using value provided.
// The election ID is a uint128 made up of concatenating the low and high uint64 values provided.
func (g *gRIBIModify) UpdateElectionID(t testing.TB, low, high uint64) *gRIBIModify {
	g.parent.c.Q(&spb.ModifyRequest{
		ElectionId: &spb.Uint128{
			Low:  low,
			High: high,
		},
	})
	return g
}

// entriesToModifyRequest creates a ModifyRequest from a set of input entries.
func (g *gRIBIModify) entriesToModifyRequest(op spb.AFTOperation_Operation, entries []GRIBIEntry) (*spb.ModifyRequest, error) {
	m := &spb.ModifyRequest{}
	for _, e := range entries {
		ep, err := e.OpProto()
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

		if g.parent == nil {
			return nil, errors.New("invalid nil parent")
		}
		// increment before first use of the opCount so that we start at 1.
		g.parent.opCount++
		ep.Id = g.parent.opCount

		// If the election ID wasn't explicitly set then write the current one
		// to the message if this is a client that requires it.
		if g.parent.connection != nil && g.parent.connection.redundMode == ElectedPrimaryClient && ep.ElectionId == nil {
			ep.ElectionId = g.parent.currentElectionID
		}

		m.Operation = append(m.Operation, ep)
	}
	return m, nil
}

// GRIBIEntry is an entry implemented for all types that can be returned
// as a gRIBI entry.
type GRIBIEntry interface {
	// OpProto builds the entry as a new AFTOperation protobuf.
	OpProto() (*spb.AFTOperation, error)
	// EntryProto builds the entry as a new AFTEntry protobuf.
	EntryProto() (*spb.AFTEntry, error)
}

// ipv4Entry is the internal representation of a gRIBI IPv4Entry.
type ipv4Entry struct {
	// pb is the gRIBI IPv4Entry that is being composed.
	pb *aftpb.Afts_Ipv4EntryKey
	// ni is the network instance to which the IPv4Entry is applied.
	ni string
	// electionID is an explicit election ID to be used for an
	// operation using the entry.
	electionID *spb.Uint128
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

// WithNextHopGroupNetworkInstance specifies the network-instance within which
// the next-hop-group for the IPv4 entry should be resolved.
func (i *ipv4Entry) WithNextHopGroupNetworkInstance(n string) *ipv4Entry {
	i.pb.Ipv4Entry.NextHopGroupNetworkInstance = &wpb.StringValue{Value: n}
	return i
}

// WithMetadata specifies a byte slice that is stored as metadata alongside
// the entry on the gRIBI server.
func (i *ipv4Entry) WithMetadata(b []byte) *ipv4Entry {
	i.pb.Ipv4Entry.EntryMetadata = &wpb.BytesValue{Value: b}
	return i
}

// WithElectionID specifies an explicit election ID to be used for the Entry.
// The election ID is made up of the concatenation of the low and high uint64
// values provided.
func (i *ipv4Entry) WithElectionID(low, high uint64) *ipv4Entry {
	i.electionID = &spb.Uint128{
		Low:  low,
		High: high,
	}
	return i
}

// OpProto implements the gRIBIEntry interface, returning a gRIBI AFTOperation. ID
// and ElectionID are explicitly not populated such that they can be populated by
// the function (e.g., AddEntry) to which they are an argument.
func (i *ipv4Entry) OpProto() (*spb.AFTOperation, error) {
	return &spb.AFTOperation{
		NetworkInstance: i.ni,
		Entry: &spb.AFTOperation_Ipv4{
			Ipv4: proto.Clone(i.pb).(*aftpb.Afts_Ipv4EntryKey),
		},
		ElectionId: i.electionID,
	}, nil
}

// EntryProto implements the GRIBIEntry interface, building a gRIBI AFTEntry.
func (i *ipv4Entry) EntryProto() (*spb.AFTEntry, error) {
	return &spb.AFTEntry{
		NetworkInstance: i.ni,
		Entry: &spb.AFTEntry_Ipv4{
			Ipv4: proto.Clone(i.pb).(*aftpb.Afts_Ipv4EntryKey),
		},
	}, nil
}

// nextHopEntry is the internal representation of a next-hop Entry in gRIBI.
type nextHopEntry struct {
	// ni is the network instance that the next-hop entry is within.
	ni string
	// pb is the AFT protobuf representing the next-hop entry.
	pb *aftpb.Afts_NextHopKey

	// electionID is an explicit electionID to be used hen the next-hop entry
	// is programmed.
	electionID *spb.Uint128
}

// NextHopEntry returns a builder that can be used to build up a NextHop within
// gRIBI.
func NextHopEntry() *nextHopEntry {
	return &nextHopEntry{
		pb: &aftpb.Afts_NextHopKey{
			NextHop: &aftpb.Afts_NextHop{},
		},
	}
}

// WithIndex specifies the index of the next-hop entry.
func (n *nextHopEntry) WithIndex(i uint64) *nextHopEntry {
	n.pb.Index = i
	return n
}

// WithNetworkInstance specifies the network instance within which the next-hop
// is being created.
func (n *nextHopEntry) WithNetworkInstance(ni string) *nextHopEntry {
	n.ni = ni
	return n
}

// WithIPAddress specifies an IP address to be used for the next-hop. The IP
// address is resolved within the network instance specified by WithNextHopNetworkInstance.
func (n *nextHopEntry) WithIPAddress(addr string) *nextHopEntry {
	n.pb.NextHop.IpAddress = &wpb.StringValue{Value: addr}
	return n
}

// WithNextHopNetworkInstance specifies the network instance within which the next-hop
// should be resolved. If it is not specified, the next-hop is resolved in the network
// instance that the next-hop is installed in. If no other parameters are specified, the
// lookup uses the input packet within the specified network instance to determine the
// next-hop.
func (n *nextHopEntry) WithNextHopNetworkInstance(ni string) *nextHopEntry {
	n.pb.NextHop.NetworkInstance = &wpb.StringValue{Value: ni}
	return n
}

// DecapsulateHeader represents the enumerated set of headers that can be decapsulated
// from a packet.
type DecapsulateHeader int64

const (
	_ DecapsulateHeader = iota
	// IPinIP specifies that the header to be decpsulated is an IPv4 header, and is typically
	// used when IP-in-IP tunnels are created.
	IPinIP
)

var (
	// decapMap translates between the fluent DecapsulateHeader type and the generated
	// protobuf name.
	decapMap = map[DecapsulateHeader]enums.OpenconfigAftTypesEncapsulationHeaderType{
		IPinIP: enums.OpenconfigAftTypesEncapsulationHeaderType_OPENCONFIGAFTTYPESENCAPSULATIONHEADERTYPE_IPV4,
	}
)

// WithDecapsulateHeader specifies that the next-hop should apply an action to decapsulate
// the packet from the specified header, h.
func (n *nextHopEntry) WithDecapsulateHeader(h DecapsulateHeader) *nextHopEntry {
	n.pb.NextHop.DecapsulateHeader = decapMap[h]
	return n
}

// WithElectionID specifies an explicit election ID that is to be used hen the next hop
// is programmed in an AFTOperation. The electionID is a uint128 made up of concatenating
// the low and high uint64 values provided.
func (n *nextHopEntry) WithElectionID(low, high uint64) *nextHopEntry {
	n.electionID = &spb.Uint128{
		Low:  low,
		High: high,
	}
	return n
}

// TODO(robjs): add additional NextHopEntry fields.

// OpProto implements the GRIBIEntry interface, building a gRIBI AFTOperation. ID
// and ElectionID are explicitly not populated such that they can be populated by
// the function (e.g., AddEntry) to which they are an argument.
func (n *nextHopEntry) OpProto() (*spb.AFTOperation, error) {
	return &spb.AFTOperation{
		NetworkInstance: n.ni,
		Entry: &spb.AFTOperation_NextHop{
			NextHop: proto.Clone(n.pb).(*aftpb.Afts_NextHopKey),
		},
		ElectionId: n.electionID,
	}, nil
}

// EntryProto implements the GRIBIEntry interface, building a gRIBI AFTEntry.
func (n *nextHopEntry) EntryProto() (*spb.AFTEntry, error) {
	return &spb.AFTEntry{
		NetworkInstance: n.ni,
		Entry: &spb.AFTEntry_NextHop{
			NextHop: proto.Clone(n.pb).(*aftpb.Afts_NextHopKey),
		},
	}, nil
}

// nextHopGroupEntry is the internal representation of a next-hop-group Entry in gRIBI.
type nextHopGroupEntry struct {
	// ni is the network instance that the next-hop-group entry is within.
	ni string
	// pb is the AFT protobuf that describes the NextHopGroup entry.
	pb *aftpb.Afts_NextHopGroupKey

	// electionID is the explicit election ID to be used when this entry is used
	// in an AFTOperation.
	electionID *spb.Uint128
}

// NextHopGroupEntry returns a builder that can be used to build up a NextHopGroup within
// gRIBI.
func NextHopGroupEntry() *nextHopGroupEntry {
	return &nextHopGroupEntry{
		pb: &aftpb.Afts_NextHopGroupKey{
			NextHopGroup: &aftpb.Afts_NextHopGroup{},
		},
	}
}

// WithID specifies the index of the next-hop entry.
func (n *nextHopGroupEntry) WithID(i uint64) *nextHopGroupEntry {
	n.pb.Id = i
	return n
}

// WithNetworkInstance specifies the network instance within which the next-hop-group
// is being created.
func (n *nextHopGroupEntry) WithNetworkInstance(ni string) *nextHopGroupEntry {
	n.ni = ni
	return n
}

// WithBackupNHG specifies a backup next-hop-group that is to be used when the
// next-hop-group being created is not viable.
func (n *nextHopGroupEntry) WithBackupNHG(id uint64) *nextHopGroupEntry {
	n.pb.NextHopGroup.BackupNextHopGroup = &wpb.UintValue{Value: id}
	return n
}

// AddNextHop adds the specified nexthop index to the NextHopGroup with the specified weight.
func (n *nextHopGroupEntry) AddNextHop(index, weight uint64) *nextHopGroupEntry {
	n.pb.NextHopGroup.NextHop = append(n.pb.NextHopGroup.NextHop, &aftpb.Afts_NextHopGroup_NextHopKey{
		Index: index,
		NextHop: &aftpb.Afts_NextHopGroup_NextHop{
			Weight: &wpb.UintValue{Value: weight},
		},
	})
	return n
}

// WithElectionID specifies an explicit election ID that is to be used when the next hop group
// is programmed in an AFTOperation. The electionID is a uint128 made up of concatenating
// the low and high uint64 values provided.
func (n *nextHopGroupEntry) WithElectionID(low, high uint64) *nextHopGroupEntry {
	n.electionID = &spb.Uint128{
		Low:  low,
		High: high,
	}
	return n
}

// OpProto implements the GRIBIEntry interface, building a gRIBI AFTOperation. ID
// and ElectionID are explicitly not populated such that they can be populated by
// the function (e.g., AddEntry) to which they are an argument.
func (n *nextHopGroupEntry) OpProto() (*spb.AFTOperation, error) {
	return &spb.AFTOperation{
		NetworkInstance: n.ni,
		Entry: &spb.AFTOperation_NextHopGroup{
			NextHopGroup: proto.Clone(n.pb).(*aftpb.Afts_NextHopGroupKey),
		},
		ElectionId: n.electionID,
	}, nil
}

// EntryProto implements the GRIBIEntry interface, returning a gRIBI AFTEntry.
func (n *nextHopGroupEntry) EntryProto() (*spb.AFTEntry, error) {
	return &spb.AFTEntry{
		NetworkInstance: n.ni,
		Entry: &spb.AFTEntry_NextHopGroup{
			NextHopGroup: proto.Clone(n.pb).(*aftpb.Afts_NextHopGroupKey),
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

// reasonMap provides a mapping between the fluent readable modify error reason and
// the defined reason in the gRIBI protobuf.
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

// opResult is an internal representation of the client
// operation result that can be built up using the fluent API.
type opResult struct {
	r *client.OpResult
}

// OperationResult is a response that is received from the gRIBI server.
func OperationResult() *opResult {
	return &opResult{
		r: &client.OpResult{},
	}
}

// WithCurrentServerElectionID specifies a result that contains a response
// that set the election ID to the uint128 value represented by low and high.
func (o *opResult) WithCurrentServerElectionID(low, high uint64) *opResult {
	o.r.CurrentServerElectionID = &spb.Uint128{Low: low, High: high}
	return o
}

// WithSuccessfulSessionParams specifies that the server responded to a
// session parameters request with an OK response.
func (o *opResult) WithSuccessfulSessionParams() *opResult {
	o.r.SessionParameters = &spb.SessionParametersResult{
		Status: spb.SessionParametersResult_OK,
	}
	return o
}

// WithOperationID specifies the result was in response to a specific
// operation ID.
func (o *opResult) WithOperationID(i uint64) *opResult {
	o.r.OperationID = i
	return o
}

// WithIPv4Operation indicates that the result corresponds to
// an operation impacting the IPv4 prefix p which is of the form
// prefix/mask.
func (o *opResult) WithIPv4Operation(p string) *opResult {
	if o.r.Details == nil {
		o.r.Details = &client.OpDetailsResults{}
	}
	o.r.Details.IPv4Prefix = p
	return o
}

// WithNextHopGroupOperation indicates that the result correspodns to
// an operation impacting the next-hop-group with index i.
func (o *opResult) WithNextHopGroupOperation(i uint64) *opResult {
	if o.r.Details == nil {
		o.r.Details = &client.OpDetailsResults{}
	}
	o.r.Details.NextHopGroupID = i
	return o
}

// WithNextHopOperation indicates that the result corresponds to
// an operation impacting the next-hop with ID i.
func (o *opResult) WithNextHopOperation(i uint64) *opResult {
	if o.r.Details == nil {
		o.r.Details = &client.OpDetailsResults{}
	}
	o.r.Details.NextHopIndex = i
	return o
}

// WithOperationType indicates that the result corresponds to
// an operation with a specific type.
func (o *opResult) WithOperationType(c constants.OpType) *opResult {
	if o.r.Details == nil {
		o.r.Details = &client.OpDetailsResults{}
	}
	o.r.Details.Type = c
	return o
}

// ProgrammingResult is a fluent-style representation of the AFTResult Status
// enumeration in gRIBI.
type ProgrammingResult int64

const (
	// ProgrammingFailed indicates that the entry was not installed into the
	// RIB or FIB, and cannot be.
	ProgrammingFailed ProgrammingResult = iota
	// InstalledInRIB indicates that the entry was installed into the RIB. It
	// does not guarantee that the system is using the entry, and does not
	// guarantee that it is in hardware.
	InstalledInRIB
	// InstalledInFIB indicates that the entry was installed into the FIB. It
	// indicates that the system is using the entry and it is installed in
	// hardware.
	InstalledInFIB
)

// programmingResultMap maps the fluent-style programming result to the
// enumerated value in the protobuf.
var programmingResultMap = map[ProgrammingResult]spb.AFTResult_Status{
	ProgrammingFailed: spb.AFTResult_FAILED,
	InstalledInRIB:    spb.AFTResult_RIB_PROGRAMMED,
	InstalledInFIB:    spb.AFTResult_FIB_PROGRAMMED,
}

// WithProgrammingResult specifies an expected programming result for
// the operation result.
func (o *opResult) WithProgrammingResult(r ProgrammingResult) *opResult {
	o.r.ProgrammingResult = programmingResultMap[r]
	return o
}

// AsResult returns the operation result as a client OpResult for comparison.
func (o *opResult) AsResult() *client.OpResult {
	return o.r
}

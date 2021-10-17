// Code generated by protoc-gen-go-grpc. DO NOT EDIT.

package wire

import (
	context "context"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
// Requires gRPC-Go v1.32.0 or later.
const _ = grpc.SupportPackageIsVersion7

// WireServerClient is the client API for WireServer service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type WireServerClient interface {
	Stream(ctx context.Context, opts ...grpc.CallOption) (WireServer_StreamClient, error)
}

type wireServerClient struct {
	cc grpc.ClientConnInterface
}

func NewWireServerClient(cc grpc.ClientConnInterface) WireServerClient {
	return &wireServerClient{cc}
}

func (c *wireServerClient) Stream(ctx context.Context, opts ...grpc.CallOption) (WireServer_StreamClient, error) {
	stream, err := c.cc.NewStream(ctx, &WireServer_ServiceDesc.Streams[0], "/openconfig.kne.wire.WireServer/Stream", opts...)
	if err != nil {
		return nil, err
	}
	x := &wireServerStreamClient{stream}
	return x, nil
}

type WireServer_StreamClient interface {
	Send(*WireStream) error
	Recv() (*WireStream, error)
	grpc.ClientStream
}

type wireServerStreamClient struct {
	grpc.ClientStream
}

func (x *wireServerStreamClient) Send(m *WireStream) error {
	return x.ClientStream.SendMsg(m)
}

func (x *wireServerStreamClient) Recv() (*WireStream, error) {
	m := new(WireStream)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

// WireServerServer is the server API for WireServer service.
// All implementations must embed UnimplementedWireServerServer
// for forward compatibility
type WireServerServer interface {
	Stream(WireServer_StreamServer) error
	mustEmbedUnimplementedWireServerServer()
}

// UnimplementedWireServerServer must be embedded to have forward compatible implementations.
type UnimplementedWireServerServer struct {
}

func (UnimplementedWireServerServer) Stream(WireServer_StreamServer) error {
	return status.Errorf(codes.Unimplemented, "method Stream not implemented")
}
func (UnimplementedWireServerServer) mustEmbedUnimplementedWireServerServer() {}

// UnsafeWireServerServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to WireServerServer will
// result in compilation errors.
type UnsafeWireServerServer interface {
	mustEmbedUnimplementedWireServerServer()
}

func RegisterWireServerServer(s grpc.ServiceRegistrar, srv WireServerServer) {
	s.RegisterService(&WireServer_ServiceDesc, srv)
}

func _WireServer_Stream_Handler(srv interface{}, stream grpc.ServerStream) error {
	return srv.(WireServerServer).Stream(&wireServerStreamServer{stream})
}

type WireServer_StreamServer interface {
	Send(*WireStream) error
	Recv() (*WireStream, error)
	grpc.ServerStream
}

type wireServerStreamServer struct {
	grpc.ServerStream
}

func (x *wireServerStreamServer) Send(m *WireStream) error {
	return x.ServerStream.SendMsg(m)
}

func (x *wireServerStreamServer) Recv() (*WireStream, error) {
	m := new(WireStream)
	if err := x.ServerStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

// WireServer_ServiceDesc is the grpc.ServiceDesc for WireServer service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var WireServer_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "openconfig.kne.wire.WireServer",
	HandlerType: (*WireServerServer)(nil),
	Methods:     []grpc.MethodDesc{},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "Stream",
			Handler:       _WireServer_Stream_Handler,
			ServerStreams: true,
			ClientStreams: true,
		},
	},
	Metadata: "wire.proto",
}

// WireControlClient is the client API for WireControl service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type WireControlClient interface {
	Register(ctx context.Context, opts ...grpc.CallOption) (WireControl_RegisterClient, error)
}

type wireControlClient struct {
	cc grpc.ClientConnInterface
}

func NewWireControlClient(cc grpc.ClientConnInterface) WireControlClient {
	return &wireControlClient{cc}
}

func (c *wireControlClient) Register(ctx context.Context, opts ...grpc.CallOption) (WireControl_RegisterClient, error) {
	stream, err := c.cc.NewStream(ctx, &WireControl_ServiceDesc.Streams[0], "/openconfig.kne.wire.WireControl/Register", opts...)
	if err != nil {
		return nil, err
	}
	x := &wireControlRegisterClient{stream}
	return x, nil
}

type WireControl_RegisterClient interface {
	Send(*RegisterClientStream) error
	Recv() (*RegisterServerStream, error)
	grpc.ClientStream
}

type wireControlRegisterClient struct {
	grpc.ClientStream
}

func (x *wireControlRegisterClient) Send(m *RegisterClientStream) error {
	return x.ClientStream.SendMsg(m)
}

func (x *wireControlRegisterClient) Recv() (*RegisterServerStream, error) {
	m := new(RegisterServerStream)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

// WireControlServer is the server API for WireControl service.
// All implementations must embed UnimplementedWireControlServer
// for forward compatibility
type WireControlServer interface {
	Register(WireControl_RegisterServer) error
	mustEmbedUnimplementedWireControlServer()
}

// UnimplementedWireControlServer must be embedded to have forward compatible implementations.
type UnimplementedWireControlServer struct {
}

func (UnimplementedWireControlServer) Register(WireControl_RegisterServer) error {
	return status.Errorf(codes.Unimplemented, "method Register not implemented")
}
func (UnimplementedWireControlServer) mustEmbedUnimplementedWireControlServer() {}

// UnsafeWireControlServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to WireControlServer will
// result in compilation errors.
type UnsafeWireControlServer interface {
	mustEmbedUnimplementedWireControlServer()
}

func RegisterWireControlServer(s grpc.ServiceRegistrar, srv WireControlServer) {
	s.RegisterService(&WireControl_ServiceDesc, srv)
}

func _WireControl_Register_Handler(srv interface{}, stream grpc.ServerStream) error {
	return srv.(WireControlServer).Register(&wireControlRegisterServer{stream})
}

type WireControl_RegisterServer interface {
	Send(*RegisterServerStream) error
	Recv() (*RegisterClientStream, error)
	grpc.ServerStream
}

type wireControlRegisterServer struct {
	grpc.ServerStream
}

func (x *wireControlRegisterServer) Send(m *RegisterServerStream) error {
	return x.ServerStream.SendMsg(m)
}

func (x *wireControlRegisterServer) Recv() (*RegisterClientStream, error) {
	m := new(RegisterClientStream)
	if err := x.ServerStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

// WireControl_ServiceDesc is the grpc.ServiceDesc for WireControl service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var WireControl_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "openconfig.kne.wire.WireControl",
	HandlerType: (*WireControlServer)(nil),
	Methods:     []grpc.MethodDesc{},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "Register",
			Handler:       _WireControl_Register_Handler,
			ServerStreams: true,
			ClientStreams: true,
		},
	},
	Metadata: "wire.proto",
}

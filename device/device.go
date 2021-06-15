package device

import (
	"context"
	"fmt"
	"net"

	"github.com/openconfig/gribigo/gnmit"
	"github.com/openconfig/gribigo/rib"
	"github.com/openconfig/gribigo/server"
	"github.com/openconfig/gribigo/sysrib"
	"github.com/openconfig/ygot/ygot"
	"google.golang.org/grpc"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	spb "github.com/openconfig/gribi/v1/proto/service"
)

type Device struct {
	gribiAddr string
	gribiSrv  *server.Server
	gnmiAddr  string
	gnmiSrv   *gnmit.Collector

	gRIBIRIB *rib.RIB
	sysRIB   *sysrib.SysRIB
}

const (
	targetName    string = "DUT"
	defaultNIName string = "DEFAULT"
)

type DevOpt interface {
	isDevOpt()
}

type gRIBIPort struct {
	port int
}

func (*gRIBIPort) isDevOpt() {}

func GRIBIPort(i int) *gRIBIPort {
	return &gRIBIPort{port: i}
}

type gNMIPort struct {
	port int
}

func (*gNMIPort) isDevOpt() {}

func GNMIPort(i int) *gNMIPort {
	return &gNMIPort{port: i}
}

type deviceConfig struct {
	json []byte
}

func (*deviceConfig) isDevOpt() {}
func DeviceConfig(c []byte) *deviceConfig {
	return &deviceConfig{json: c}
}

func New(ctx context.Context, opts ...DevOpt) (*Device, func(), error) {
	var cancel func()
	ctx, cancel = context.WithCancel(ctx)
	d := &Device{}

	d.gRIBIRIB = rib.New(defaultNIName)
	sr, err := sysrib.NewSysRIBFromJSON(optDeviceCfg(opts))
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("cannot build system RIB, %v", err)
	}
	d.sysRIB = sr

	pch := func(o rib.OpType, ni string, data ygot.GoStruct) {
		_, _, _ = o, ni, data
		// write gNMI notifications
		go d.gnmiSrv.TargetUpdate(&gpb.SubscribeResponse{})
		// TODO manage within the system RIB

	}
	d.gRIBIRIB.SetHook(pch)

	if err := d.startgRIBI(ctx, optGRIBIPort(opts), d.gRIBIRIB); err != nil {
		cancel()
		return nil, nil, fmt.Errorf("cannot start gRIBI server, %v", err)
	}
	if err := d.startgNMI(ctx, optGNMIPort(opts)); err != nil {
		cancel()
		return nil, nil, fmt.Errorf("cannot start gNMI server, %v", err)
	}

	return d, cancel, nil
}

func optGRIBIPort(opt []DevOpt) int {
	for _, o := range opt {
		if v, ok := o.(*gRIBIPort); ok {
			return v.port
		}
	}
	return 0
}
func optGNMIPort(opt []DevOpt) int {
	for _, o := range opt {
		if v, ok := o.(*gNMIPort); ok {
			return v.port
		}
	}
	return 0
}
func optDeviceCfg(opt []DevOpt) []byte {
	for _, o := range opt {
		if v, ok := o.(*deviceConfig); ok {
			return v.json
		}
	}
	return nil
}

func (d *Device) startgRIBI(ctx context.Context, port int, ribMgr server.RIBManager) error {
	l, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", port))
	if err != nil {
		return fmt.Errorf("cannot create gRIBI server, %v", err)
	}

	s := grpc.NewServer()
	ts := server.New(ribMgr)
	spb.RegisterGRIBIServer(s, ts)
	d.gribiAddr = l.Addr().String()
	d.gribiSrv = ts
	go s.Serve(l)
	return nil
}

func (d *Device) startgNMI(ctx context.Context, port int) error {
	c, addr, err := gnmit.New(ctx, 0, targetName, true)
	if err != nil {
		return err
	}
	d.gnmiAddr = addr
	d.gnmiSrv = c
	return nil
}

package device

import (
	"context"
	"fmt"
	"net"

	log "github.com/golang/glog"
	"github.com/openconfig/gribigo/aft"
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
	if jcfg := optDeviceCfg(opts); jcfg != nil {
		sr, err := sysrib.NewSysRIBFromJSON(jcfg)
		if err != nil {
			cancel()
			return nil, nil, fmt.Errorf("cannot build system RIB, %v", err)
		}
		d.sysRIB = sr
	}

	pch := func(o rib.OpType, ts int64, ni string, data ygot.GoStruct) {
		fmt.Printf("hook called with %v on ni %s\n", o, ni)
		_, _, _ = o, ni, data
		// write gNMI notifications
		n, err := gnmiNoti(o, ts, ni, data)
		switch {
		case err != nil:
			log.Errorf("invalid notifications, %v", err)
		default:
			go d.gnmiSrv.TargetUpdate(&gpb.SubscribeResponse{
				Response: &gpb.SubscribeResponse_Update{
					Update: n,
				},
			})
		}
		// TODO(robjs): add to the system RIB here - we need to plumb
		// an error back to say that the FIB was not programmed.

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
	ts := server.New(server.WithRIBManager(ribMgr))
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

func (d *Device) GRIBIAddr() string {
	return d.gribiAddr
}

func gnmiNoti(t rib.OpType, ts int64, ni string, e ygot.GoStruct) (*gpb.Notification, error) {
	var ns []*gpb.Notification
	var err error
	switch t := e.(type) {
	case *aft.Afts_Ipv4Entry:
		pfx := []*gpb.PathElem{{
			Name: "network-instances",
		}, {
			Name: "network-instance",
			Key:  map[string]string{"name": ni},
		}, {
			Name: "afts",
		}, {
			Name: "ipv4-unicast",
		}, {
			Name: "ipv4-entry",
			Key:  map[string]string{"prefix": t.GetPrefix()},
		}}
		ns, err = ygot.TogNMINotifications(e, ts, ygot.GNMINotificationsConfig{
			UsePathElem:    true,
			PathElemPrefix: pfx,
		})
	case *aft.Afts_NextHopGroup:
		pfx := []*gpb.PathElem{{
			Name: "network-instances",
		}, {
			Name: "network-instance",
			Key:  map[string]string{"name": ni},
		}, {
			Name: "afts",
		}, {
			Name: "next-hop-groups",
		}, {
			Name: "next-hop-group",
			Key:  map[string]string{"id": fmt.Sprintf("%d", t.GetId())},
		}}
		ns, err = ygot.TogNMINotifications(e, ts, ygot.GNMINotificationsConfig{
			UsePathElem:    true,
			PathElemPrefix: pfx,
		})
	case *aft.Afts_NextHop:
		pfx := []*gpb.PathElem{{
			Name: "network-instances",
		}, {
			Name: "network-instance",
			Key:  map[string]string{"name": ni},
		}, {
			Name: "afts",
		}, {
			Name: "next-hops",
		}, {
			Name: "next-hop",
			Key:  map[string]string{"index": fmt.Sprintf("%d", t.GetIndex())},
		}}
		ns, err = ygot.TogNMINotifications(e, ts, ygot.GNMINotificationsConfig{
			UsePathElem:    true,
			PathElemPrefix: pfx,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("cannot generate notifications, %v", err)
	}
	ns[0].Atomic = true
	ns[0].Prefix.Target = "localhost"
	fmt.Printf("returning %s\n", ns[0])
	return ns[0], nil
}

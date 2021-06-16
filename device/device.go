package device

import (
	"context"
	"fmt"
	"net"

	log "github.com/golang/glog"
	"github.com/openconfig/gribigo/aft"
	"github.com/openconfig/gribigo/constants"
	"github.com/openconfig/gribigo/gnmit"
	"github.com/openconfig/gribigo/server"
	"github.com/openconfig/gribigo/sysrib"
	"github.com/openconfig/ygot/ygot"
	"google.golang.org/grpc"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	spb "github.com/openconfig/gribi/v1/proto/service"
)

// Device is a wrapper struct that contains all functionalities
// for containing a gRIBI and gNMI target that has a fake system
// RIB.
type Device struct {
	// gribiAddr is the address that the server is listening on
	// for gRIBI.
	gribiAddr string
	// gribiSrv is the gRIBI server.
	gribiSrv *server.Server

	// gnmiAddr is the address that the server is listening on
	// for gNMI.
	gnmiAddr string
	// gnmiSrv is the gNMI collector implementation.
	// TODO(robjs): implement Set support for gNMI.
	gnmiSrv *gnmit.Collector

	// sysRIB is the system RIB that is being programmed.
	sysRIB *sysrib.SysRIB
}

const (
	// targetName is the name that the device has in gNMI.
	// TODO(robjs): support dynamic naming so tha twe can run N different
	// fakes at the same time.
	targetName string = "DUT"
)

// DevOpt is an interface that is implemented by options that can be handed to New()
// for the device.
type DevOpt interface {
	isDevOpt()
}

// gRIBIAddr is the internal implementation that specifies the port that gRIBI should
// listen on.
type gRIBIAddr struct {
	host string
	port int
}

// isDevOpt implements the DevOpt interface.
func (*gRIBIAddr) isDevOpt() {}

// GRIBIPort is a device option that specifies that the port that should be listened on
// is i.
func GRIBIPort(host string, i int) *gRIBIAddr {
	return &gRIBIAddr{host: host, port: i}
}

// gNMIAddress is the internal implementation that specifies the port that gNMI should
// listen on.
type gNMIAddr struct {
	host string
	port int
}

// isDevOpt implements the DevOpt interface.
func (*gNMIAddr) isDevOpt() {}

// GNMIAddr specifies the host and port that the gNMI server should listen on.
func GNMIAddr(host string, i int) *gNMIAddr {
	return &gNMIAddr{host: host, port: i}
}

// deviceConfig is a wrapper for an input OpenConfig RFC7951-marshalled JSON
// configuration for the device.
type deviceConfig struct {
	// json is the contents of the JSON document (prior to unmarshal).
	json []byte
}

// isDevOpt marks deviceConfig as a device option.
func (*deviceConfig) isDevOpt() {}

// DeviceConfig sets the startup config of the device to c.
// Today we do not allow the configuration to be changed in flight, but this
// can be implemented in the future.
func DeviceConfig(c []byte) *deviceConfig {
	return &deviceConfig{json: c}
}

// New returns a new device with the specific context. It returns the device, a function
// to stop the servers, or any errors that are encountered.
func New(ctx context.Context, opts ...DevOpt) (*Device, func(), error) {
	var cancel func()
	ctx, cancel = context.WithCancel(ctx)
	d := &Device{}

	if jcfg := optDeviceCfg(opts); jcfg != nil {
		sr, err := sysrib.NewSysRIBFromJSON(jcfg)
		if err != nil {
			cancel()
			return nil, nil, fmt.Errorf("cannot build system RIB, %v", err)
		}
		d.sysRIB = sr
	}

	ribHookfn := func(o constants.OpType, ts int64, ni string, data ygot.GoStruct) {
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
		// This means that we need the server to be aware of the FIB programming
		// function, and be able to check with it whether something was programmed
		// or not. We can implement an interface that allows us to create and hand
		// that "checker" function to the server and write the contents here.
		// This will be needed to allow testing of failures of FIB programming.
	}

	gr := optGRIBIAddr(opts)
	gn := optGNMIAddr(opts)

	if err := d.startgRIBI(ctx, gr.host, gr.port, server.WithRIBHook(ribHookfn)); err != nil {
		cancel()
		return nil, nil, fmt.Errorf("cannot start gRIBI server, %v", err)
	}

	if err := d.startgNMI(ctx, gn.host, gn.port); err != nil {
		cancel()
		return nil, nil, fmt.Errorf("cannot start gNMI server, %v", err)
	}

	return d, cancel, nil
}

// optGRIBIAddr finds the first occurrence of the GRIBIAddr option in opts.
// If no GRIBIAddr option is found, the default of localhost:0 is returned.
func optGRIBIAddr(opts []DevOpt) *gRIBIAddr {
	for _, o := range opts {
		if v, ok := o.(*gRIBIAddr); ok {
			return v
		}
	}
	return &gRIBIAddr{host: "localhost", port: 0}
}

// optGNMIAddr finds the first occurrence of the GNMIAddr option in opts.
// If no GNMIAddr option is found, the default of localhost:0 is returned.
func optGNMIAddr(opts []DevOpt) *gNMIAddr {
	for _, o := range opts {
		if v, ok := o.(*gNMIAddr); ok {
			return v
		}
	}
	return &gNMIAddr{host: "localhost", port: 0}
}

// optDeviceCfg finds the first occurrence of the DeviceConfig option in opts.
func optDeviceCfg(opts []DevOpt) []byte {
	for _, o := range opts {
		if v, ok := o.(*deviceConfig); ok {
			return v.json
		}
	}
	return nil
}

// Start gRIBI starts the gRIBI server on the device on the specified host:port with the specified options.
// It returns an error if the server cannot be started.
func (d *Device) startgRIBI(ctx context.Context, host string, port int, opt ...server.ServerOpt) error {
	l, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return fmt.Errorf("cannot create gRIBI server, %v", err)
	}

	s := grpc.NewServer()
	ts := server.New(opt...)
	spb.RegisterGRIBIServer(s, ts)
	d.gribiAddr = l.Addr().String()
	d.gribiSrv = ts
	go s.Serve(l)
	return nil
}

// startgNMI starts the gNMI server on the specified host:port.
func (d *Device) startgNMI(ctx context.Context, host string, port int) error {
	c, addr, err := gnmit.New(ctx, fmt.Sprintf("%s:%d", host, port), targetName, true)
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

func (d *Device) GNMIAddr() string {
	return d.gnmiAddr
}

func gnmiNoti(t constants.OpType, ts int64, ni string, e ygot.GoStruct) (*gpb.Notification, error) {
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
	ns[0].Prefix.Target = targetName
	fmt.Printf("returning %s\n", ns[0])
	return ns[0], nil
}

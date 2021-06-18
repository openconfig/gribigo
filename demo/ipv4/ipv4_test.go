package ipv4

import (
	"flag"
	"testing"
	"time"

	"github.com/openconfig/gribigo/compliance"
)

var (
	addr = flag.String("addr", "", "address of the gRIBI target")
	run  = flag.Bool("run", false, "whether to run the tests, this stops this file causing failures in CI")
)

func TestDemo(t *testing.T) {
	flag.Parse()
	if !*run {
		return
	}
	if *addr == "" {
		t.Fatalf("must specify an address")
	}
	compliance.AddIPv4EntryRIBACK(*addr, t)
	time.Sleep(2 * time.Second)
}

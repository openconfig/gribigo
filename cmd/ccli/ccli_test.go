package ccli

import (
	"flag"
	"strings"
	"testing"

	"github.com/openconfig/gribigo/compliance"
	"github.com/openconfig/gribigo/negtest"
)

var (
	addr = flag.String("addr", "", "address of the gRIBI server in the format hostname:port")
)

func TestCompliance(t *testing.T) {
	flag.Parse()
	if *addr == "" {
		t.Fatalf("must specify gRIBI server address, got: %v", *addr)
	}

	for _, tt := range compliance.TestSuite {
		t.Run(tt.In.ShortName, func(t *testing.T) {
			if tt.FatalMsg != "" {
				if got := negtest.ExpectFatal(t, func(t testing.TB) {
					tt.In.Fn(*addr, t)
				}); !strings.Contains(got, tt.FatalMsg) {
					t.Fatalf("did not get expected fatal error, got: %s, want: %s", got, tt.FatalMsg)
				}
				return
			}

			if tt.ErrorMsg != "" {
				if got := negtest.ExpectError(t, func(t testing.TB) {
					tt.In.Fn(*addr, t)
				}); !strings.Contains(got, tt.ErrorMsg) {
					t.Fatalf("did not get expected error, got: %s, want: %s", got, tt.ErrorMsg)
				}
			}

			// Any unexpected error will be caught by being called directly on t from the fluent library.
			tt.In.Fn(*addr, t)
		})
	}
}

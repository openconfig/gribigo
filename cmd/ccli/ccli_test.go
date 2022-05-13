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

package ccli

import (
	"context"
	"crypto/tls"
	"flag"
	"os"
	"strings"
	"testing"

	log "github.com/golang/glog"

	"github.com/openconfig/gribigo/compliance"
	"github.com/openconfig/gribigo/fluent"
	"github.com/openconfig/gribigo/server"
	"github.com/openconfig/testt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	spb "github.com/openconfig/gribi/v1/proto/service"
)

var (
	addr                = flag.String("addr", "", "address of the gRIBI server in the format hostname:port")
	insecure            = flag.Bool("insecure", false, "dial insecure gRPC (no TLS)")
	skipVerify          = flag.Bool("skip_verify", true, "allow self-signed TLS certificate; not needed for -insecure")
	username            = flag.String("username", os.Getenv("USER"), "username to be sent as gRPC metadata")
	password            = flag.String("password", "", "password to be sent as gRPC metadata")
	initialElectionID   = flag.Uint("initial_electionid", 0, "initial election ID to be used")
	skipFIBACK          = flag.Bool("skip_fiback", false, "skip tests that rely on FIB ACK")
	skipSrvReorder      = flag.Bool("skip_reordering", false, "skip tests that rely on server side transaction reordering")
	skipImplicitReplace = flag.Bool("skip_implicit_replace", false, "skip tests for ADD operations that perform implicit replacement of existing entries")
	defaultNIName       = flag.String("default_ni_name", server.DefaultNetworkInstanceName, "default network instance name to be used for the server")
)

// flagCred implements credentials.PerRPCCredentials by populating the
// username and password metadata from flags.
type flagCred struct{}

// GetRequestMetadata is needed by credentials.PerRPCCredentials.
func (flagCred) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	return map[string]string{
		"username": *username,
		"password": *password,
	}, nil
}

// RequireTransportSecurity is needed by credentials.PerRPCCredentials.
func (flagCred) RequireTransportSecurity() bool {
	return false
}

func TestCompliance(t *testing.T) {
	if *addr == "" {
		log.Errorf("Must specify gRIBI server address, got: %v", *addr)
		return // Test is part of CI, so do not fail here.
	}

	if *initialElectionID != 0 {
		compliance.SetElectionID(uint64(*initialElectionID))
	}

	compliance.SetDefaultNetworkInstanceName(*defaultNIName)

	dialOpts := []grpc.DialOption{grpc.WithBlock()}
	if *insecure {
		dialOpts = append(dialOpts, grpc.WithInsecure())
	} else if *skipVerify {
		tlsc := credentials.NewTLS(&tls.Config{
			InsecureSkipVerify: *skipVerify,
		})
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(tlsc))
	}

	if *password != "" {
		dialOpts = append(dialOpts, grpc.WithPerRPCCredentials(flagCred{}))
	}

	for _, tt := range compliance.TestSuite {
		if skip := *skipFIBACK; skip && tt.In.RequiresFIBACK {
			continue
		}

		if skip := *skipSrvReorder; skip && tt.In.RequiresServerReordering {
			continue
		}

		if skip := *skipImplicitReplace; skip && tt.In.RequiresImplicitReplace {
			continue
		}

		t.Run(tt.In.ShortName, func(t *testing.T) {
			ctx := context.Background()
			conn, err := grpc.DialContext(ctx, *addr, dialOpts...)
			if err != nil {
				t.Fatalf("Could not dial gRPC: %v", err)
			}
			defer conn.Close()
			stub := spb.NewGRIBIClient(conn)

			secondConn, err := grpc.DialContext(ctx, *addr, dialOpts...)
			if err != nil {
				t.Fatalf("could not dial gRPC for second client: %v", err)
			}
			defer secondConn.Close()
			scStub := spb.NewGRIBIClient(conn)

			c := fluent.NewClient()
			c.Connection().WithStub(stub)

			sc := fluent.NewClient()
			sc.Connection().WithStub(scStub)

			opts := []compliance.TestOpt{
				compliance.SecondClient(sc),
			}

			if tt.FatalMsg != "" {
				if got := testt.ExpectFatal(t, func(t testing.TB) {
					tt.In.Fn(c, t, opts...)
				}); !strings.Contains(got, tt.FatalMsg) {
					t.Fatalf("Did not get expected fatal error, got: %s, want: %s", got, tt.FatalMsg)
				}
				return
			}

			if tt.ErrorMsg != "" {
				if got := testt.ExpectError(t, func(t testing.TB) {
					tt.In.Fn(c, t, opts...)
				}); !strings.Contains(strings.Join(got, " "), tt.ErrorMsg) {
					t.Fatalf("Did not get expected error, got: %s, want: %s", got, tt.ErrorMsg)
				}
			}

			// Any unexpected error will be caught by being called directly on t from the fluent library.
			tt.In.Fn(c, t, opts...)
		})
	}
}

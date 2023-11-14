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

// Package testcommon implements common helpers that can be used throughout
// the gRIBIgo package.
package testcommon

import (
	"path/filepath"
	"runtime"

	"google.golang.org/grpc/credentials"
)

// TLSCreds returns the path for the testcommon/testdata file for all other tests
// to use.
func TLSCreds() (string, string) {
	_, fn, _, _ := runtime.Caller(0)
	td := filepath.Join(filepath.Dir(fn), "testdata")
	return filepath.Join(td, "server.cert"), filepath.Join(td, "server.key")
}

// TLSCreds returns TLS credentials that can be used for a device.
type TLSCred struct {
	C credentials.TransportCredentials
}

// TLSCredsFromFile loads the credentials from the specified cert and key file
// and returns them such that they can be used for the gNMI and gRIBI servers.
func TLSCredsFromFile(certFile, keyFile string) (*TLSCred, error) {
	t, err := credentials.NewServerTLSFromFile(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	return &TLSCred{C: t}, nil
}

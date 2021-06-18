// Package testcommon implements common helpers that can be used throughout
// the gRIBIgo package.
package testcommon

import (
	"path/filepath"
	"runtime"
)

// TLSCreds returns the path for the testcommon/testdata file for all other tests
// to use.
func TLSCreds() (string, string) {
	_, fn, _, _ := runtime.Caller(0)
	td := filepath.Join(filepath.Dir(fn), "testdata")
	return filepath.Join(td, "server.cert"), filepath.Join(td, "server.key")
}

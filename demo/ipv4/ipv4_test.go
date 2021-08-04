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

package ipv4

import (
	"flag"
	"testing"
	"time"

	"github.com/openconfig/gribigo/compliance"
	"github.com/openconfig/gribigo/fluent"
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
	c := fluent.NewClient()
	c.Connection().WithTarget(*addr)
	compliance.AddIPv4EntryRIBACK(c, t)
	time.Sleep(2 * time.Second)
}

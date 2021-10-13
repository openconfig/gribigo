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

package compliance

import "testing"

func TestIndexAsIPv4(t *testing.T) {
	tests := []struct {
		desc     string
		inIndex  uint32
		inSlash8 int
		want     string
	}{{
		desc:     "index 0",
		inIndex:  0,
		inSlash8: 1,
		want:     "1.0.0.0",
	}, {
		desc:     "index 255",
		inIndex:  255,
		inSlash8: 1,
		want:     "1.0.0.255",
	}, {
		desc:     "index 256",
		inIndex:  256,
		inSlash8: 1,
		want:     "1.0.1.0",
	}, {
		desc:     "index 542 in 42/8",
		inIndex:  542,
		inSlash8: 42,
		want:     "42.0.2.30",
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			if got := indexAsIPv4(tt.inIndex, tt.inSlash8); got != tt.want {
				t.Fatalf("did not get expected result, got: %v, want: %v", got, tt.want)
			}
		})
	}
}

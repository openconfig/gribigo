#!/bin/bash -eu
#
# Copyright 2021 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

#!/bin/bash

# Hack to ensure that if we are running on OS X with a homebrew installed
# GNU sed then we can still run sed.
runsed() {
  if hash gsed 2>/dev/null; then
    gsed "$@"
  else
    sed "$@"
  fi
}

git clone https://github.com/openconfig/public.git
go get github.com/openconfig/ygot/generator@latest
go install github.com/openconfig/ygot/generator

generator -path=public -output_file=oc.go \
    -package_name=ocrt -generate_fakeroot -fakeroot_name=device -compress_paths=true \
    -shorten_enum_leaf_names \
    -prefer_operational_state \
    -trim_enum_openconfig_prefix \
    -typedef_enum_with_defmod \
    -enum_suffix_for_simple_union_enums \
    -exclude_modules=ietf-interfaces,openconfig-acl,openconfig-routing-policy \
    -generate_simple_unions \
    -generate_getters \
    -generate_leaf_getters \
    -generate_delete \
    -generate_append \
    public/release/models/interfaces/openconfig-interfaces.yang \
    public/release/models/interfaces/openconfig-if-ip.yang \
    public/release/models/network-instance/openconfig-network-instance.yang \
    yang/deviations.yang

git clone -b v0.4 https://github.com/mbrukman/autogen.git
autogen/autogen --no-code --no-tlc -c "The OpenConfig Contributors" -l apache -i oc.go
gofmt -w -s oc.go
rm -rf autogen public

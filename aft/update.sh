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

git clone https://github.com/openconfig/gribi.git
go get github.com/openconfig/ygot@latest
go install github.com/openconfig/ygot/generator
generator -path=gribi -output_file=oc.go \
    -package_name=aft -generate_fakeroot -fakeroot_name=RIB -compress_paths=true \
    -shorten_enum_leaf_names \
    -prefer_operational_state \
    -trim_enum_openconfig_prefix \
    -typedef_enum_with_defmod \
    -enum_suffix_for_simple_union_enums \
    -exclude_modules=openconfig-interfaces,ietf-interfaces \
    gribi/v1/yang/gribi-aft.yang

git clone -b v0.4 https://github.com/mbrukman/autogen.git
autogen/autogen --no-code --no-tlc -c "The OpenConfig Contributors" -l apache -i oc.go
gofmt -w -s oc.go
rm -rf autogen gribi

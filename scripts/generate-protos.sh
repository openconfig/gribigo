#!/bin/bash

if [ -z $SRCDIR ]; then
	THIS_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"
	SRC_DIR=${THIS_DIR}/..
fi

mkdir -p ${SRC_DIR}/build_deps/github.com/openconfig/gribi/v1/proto/{service,gribi_aft}
mkdir -p ${SRC_DIR}/build_deps/github.com/openconfig/gribi/v1/proto/gribi_aft/enums
mkdir -p ${SRC_DIR}/build_deps/github.com/openconfig/ygot/proto/{yext,ywrapper}
mkdir -p ${SRC_DIR}/build_deps/google/protobuf
curl -o ${SRC_DIR}/build_deps/github.com/openconfig/gribi/v1/proto/service/gribi.proto https://raw.githubusercontent.com/openconfig/gribi/refs/heads/master/v1/proto/service/gribi.proto
curl -o ${SRC_DIR}/build_deps/github.com/openconfig/gribi/v1/proto/gribi_aft/gribi_aft.proto https://raw.githubusercontent.com/openconfig/gribi/refs/heads/master/v1/proto/gribi_aft/gribi_aft.proto
curl -o ${SRC_DIR}/build_deps/github.com/openconfig/gribi/v1/proto/gribi_aft/enums/enums.proto https://raw.githubusercontent.com/openconfig/gribi/refs/heads/master/v1/proto/gribi_aft/enums/enums.proto
curl -o ${SRC_DIR}/build_deps/github.com/openconfig/ygot/proto/yext/yext.proto https://raw.githubusercontent.com/openconfig/ygot/master/proto/yext/yext.proto
curl -o ${SRC_DIR}/build_deps/github.com/openconfig/ygot/proto/ywrapper/ywrapper.proto https://raw.githubusercontent.com/openconfig/ygot/master/proto/ywrapper/ywrapper.proto
curl -o ${SRC_DIR}/build_deps/google/protobuf/descriptor.proto https://raw.githubusercontent.com/protocolbuffers/protobuf/refs/heads/main/src/google/protobuf/descriptor.proto

cd ${SRC_DIR}
protoc -I${SRC_DIR} -I ${SRC_DIR}/build_deps -I ${SRC_DIR}/build_deps/github.com/openconfig/gribi --go_out=. --go_opt=paths=source_relative ${SRC_DIR}/proto/result/result.proto

#rm -rf ${SRC_DIR}/build_deps

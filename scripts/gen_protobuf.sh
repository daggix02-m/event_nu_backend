#!/bin/sh
# Regenerates the gRPC transport code from proto/eventnu/v1/events.proto
# in place, with the exact tool versions that produced the checked-in files
# (recorded in the headers of internal/transport/grpc/eventnupb/*.go):
#
#   protoc             v7.35.1   (official prebuilt release: protoc-35.1)
#   protoc-gen-go      v1.36.12
#   protoc-gen-go-grpc v1.6.2
#
# Requirements:
#   - protoc on PATH (the official binary distribution, which ships the
#     include/ directory with google/protobuf/*.proto next to its bin/).
#   - Go (any module-aware version) to build the pinned plugins into a
#     temporary GOBIN, so nothing is installed globally.
#
# Run `make proto` rather than calling this directly. After a contract
# change, regenerate and commit the moved output together with the .proto.
set -eu

module=github.com/daggix02-m/event_nu_backend
proto=proto/eventnu/v1/events.proto
py_plugin_go=google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
py_plugin_grpc=google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2

protoc_bin=$(command -v protoc) || {
    echo "protoc not found on PATH" >&2
    echo "install the official binary: https://github.com/protocolbuffers/protobuf/releases (protoc-35.1)" >&2
    exit 1
}
# protoc resolves the well-known google/protobuf/* import through the
# include/ directory shipped by the official binary distribution.
include_dir=$(cd "$(dirname "$protoc_bin")/../include" 2>/dev/null && pwd)
[ -d "$include_dir" ] || {
    echo "protoc include/ directory not found next to the binary ($protoc_bin)" >&2
    echo "the official binary distribution is required, not a stripped system package" >&2
    exit 1
}

# Build the pinned plugin versions into a temporary GOBIN.
plugin_bin=$(mktemp -d)
trap 'rm -rf "$plugin_bin"' EXIT
GOBIN="$plugin_bin" go install "$py_plugin_go"
GOBIN="$plugin_bin" go install "$py_plugin_grpc"
PATH="$plugin_bin:$PATH"

protoc -I . -I "$include_dir" "$proto" \
    --go_out=. --go_opt=module=$module \
    --go-grpc_out=. --go-grpc_opt=module=$module
echo "generated internal/transport/grpc/eventnupb/{events.pb.go,events_grpc.pb.go}"

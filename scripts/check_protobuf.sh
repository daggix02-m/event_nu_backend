#!/bin/sh
# Consistency gate for the generated gRPC transport code: regenerate
# proto/eventnu/v1/events.proto with the pinned toolchain (protoc-35.1,
# protoc-gen-go v1.36.12, protoc-gen-go-grpc v1.6.2 — same pins as
# gen_protobuf.sh) into a temporary directory and compare byte-for-byte
# against the files checked into internal/transport/grpc/eventnupb/.
#
# The check never writes into the working tree, so it is safe on a dirty
# tree; drift between the working tree and a pinned regeneration fails the
# run, which is the intent (generated code must be current in git).
set -eu

module=github.com/daggix02-m/event_nu_backend
proto=proto/eventnu/v1/events.proto
# Output location after the pinned --go_opt=module= / --go-grpc_opt=module=
# strip: the module prefix is removed from the go_package path, so the
# generated files land at <out>/internal/transport/grpc/eventnupb/.
pkg_dir=internal/transport/grpc/eventnupb
py_plugin_go=google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
py_plugin_grpc=google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2

protoc_bin=$(command -v protoc) || {
    echo "protoc not found on PATH" >&2
    echo "install the official binary: https://github.com/protocolbuffers/protobuf/releases (protoc-35.1)" >&2
    exit 1
}
include_dir=$(cd "$(dirname "$protoc_bin")/../include" 2>/dev/null && pwd)
[ -d "$include_dir" ] || {
    echo "protoc include/ directory not found next to the binary ($protoc_bin)" >&2
    exit 1
}

plugin_bin=$(mktemp -d)
out_dir=$(mktemp -d)
trap 'rm -rf "$plugin_bin" "$out_dir"' EXIT
GOBIN="$plugin_bin" go install "$py_plugin_go"
GOBIN="$plugin_bin" go install "$py_plugin_grpc"
PATH="$plugin_bin:$PATH"

protoc -I . -I "$include_dir" "$proto" \
    --go_out="$out_dir" --go_opt=module=$module \
    --go-grpc_out="$out_dir" --go-grpc_opt=module=$module

status=0
for f in events.pb.go events_grpc.pb.go; do
    if cmp -s "$pkg_dir/$f" "$out_dir/$pkg_dir/$f"; then
        echo "OK  $pkg_dir/$f"
    else
        echo "DRIFT  $pkg_dir/$f differs from a pinned regeneration" >&2
        diff -u "$out_dir/$pkg_dir/$f" "$pkg_dir/$f" | head -30 >&2 || true
        status=1
    fi
done
exit $status

#!/usr/bin/env bash

# Copyright 2015 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# This script genertates `*/api.pb.go` from the protobuf file `*/api.proto`.
# Usage: 
#     hack/update-generated-protobuf-dockerized.sh "${APIROOTS}"
#     An example APIROOT is: "k8s.io/api/admissionregistration/v1"

set -o errexit
set -o nounset
set -o pipefail

KUBE_ROOT=$(dirname "${BASH_SOURCE[0]}")/..
source "${KUBE_ROOT}/hack/lib/init.sh"

kube::golang::setup_env

go install k8s.io/kubernetes/vendor/k8s.io/code-generator/cmd/go-to-protobuf
go install k8s.io/kubernetes/vendor/k8s.io/code-generator/cmd/go-to-protobuf/protoc-gen-gogo

if [[ -z "$(which protoc)" || "$(protoc --version)" != "libprotoc 23.4"* ]]; then
	  echo "Generating protobuf requires protoc 23.4 or newer. Please download and"
    echo "install the platform appropriate Protobuf package for your OS: "
    echo
    echo "  https://github.com/protocolbuffers/protobuf/releases"
    echo
    echo "WARNING: Protobuf changes are not being validated"
    exit 1
fi

gotoprotobuf=$(kube::util::find-binary "go-to-protobuf")

while IFS=$'\n' read -r line; do
  APIROOTS+=( "$line" );
done <<< "${1}"
shift

# requires the 'proto' tag to build (will remove when ready)
# searches for the protoc-gen-gogo extension in the output directory
# satisfies import of github.com/gogo/protobuf/gogoproto/gogo.proto and the
# core Google protobuf types
PATH="${KUBE_ROOT}/_output/bin:${PATH}" \
  "${gotoprotobuf}" \
  --proto-import="${KUBE_ROOT}/vendor" \
  --proto-import="${KUBE_ROOT}/third_party/protobuf" \
  --packages="$(IFS=, ; echo "${APIROOTS[*]}")" \
  --go-header-file "${KUBE_ROOT}/hack/boilerplate/boilerplate.generatego.txt" \
  "$@"

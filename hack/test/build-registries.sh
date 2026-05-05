#!/bin/bash
# Generate hack/test/registries.yaml from Sidero CI in-cluster service
# discovery. Each upstream registry is mirrored at
# registry-<host-with-dots-as-dashes>.ci.svc:5000 inside the build cluster;
# this script resolves each Service IP and emits the YAML the integration
# test consumes via REGISTRIES_FILE.
#
# Run this before integration.sh in a CI step; not used outside the cluster.

set -eou pipefail

REGISTRIES=(docker.io k8s.gcr.io quay.io gcr.io ghcr.io registry.k8s.io factory.talos.dev)
OUT="${REGISTRIES_FILE:-hack/test/registries.yaml}"

{
    echo "mirrors:"
    for r in "${REGISTRIES[@]}"; do
        addr=$(getent hosts "registry-${r//./-}.ci.svc" | awk '{print $1}')
        echo "  - ${r}=http://${addr}:5000"
    done
} > "${OUT}"

echo "wrote ${OUT}:"
cat "${OUT}"

#!/bin/bash
# End-to-end integration test for omni-infra-provider-libvirt.
#
# Stages:
#   1. infra (libvirtd + vault) via docker-compose.infra.yaml
#   2. load Vault signing key
#   3. render Omni config inline with yq, bring Omni up via
#      docker-compose.omni.yaml
#   4. wait for Omni service-account key, register infra provider
#   5. start the libvirt provider binary on the host
#   6. run the upstream omni-integration-test container
#
# Cleanup: a single `docker compose down -v --remove-orphans` covers every
# service across every stage because the trap registers all compose files.
#
# Requires: docker (with /dev/kvm passthrough, --privileged, --network host),
# jq, yq, curl.
#
# Everything bind-mounted into containers lives under the repo's _out: in CI
# the docker daemon runs in a sidecar that only shares the workspace with the
# runner, so host paths elsewhere (e.g. /tmp) differ between the two.

set -eoux pipefail

# Preflight: required tools must be on PATH.
for tool in docker jq yq curl ss; do
    command -v "${tool}" >/dev/null || { echo "${tool} is required (see hack/test/integration.sh header)"; exit 1; }
done
# The CI runners ship docker without the compose plugin.
if [[ "${CI:-false}" == "true" ]] && ! docker compose version >/dev/null 2>&1; then
    COMPOSE_VERSION=v5.6.1
    install -d /usr/local/lib/docker/cli-plugins
    curl -fsSL "https://github.com/docker/compose/releases/download/${COMPOSE_VERSION}/docker-compose-linux-$(uname -m)" \
        -o /usr/local/lib/docker/cli-plugins/docker-compose
    chmod +x /usr/local/lib/docker/cli-plugins/docker-compose
fi
docker compose version >/dev/null 2>&1 || { echo "docker compose plugin is required"; exit 1; }

# Preflight: host ports we bind via --network host must be free.
declare -A REQUIRED_PORTS=(
    [tcp/8099]="Omni HTTPS API"
    [tcp/8200]="Vault dev server"
    [tcp/8090]="Omni machine API (gRPC)"
    [tcp/8091]="Omni event sink"
    [udp/50180]="SideroLink WireGuard"
)
port_fail=0
for spec in "${!REQUIRED_PORTS[@]}"; do
    proto="${spec%/*}"
    port="${spec#*/}"
    ss_flag="-H -${proto:0:1}n"
    if [ -n "$(ss ${ss_flag} "sport = :${port}" 2>/dev/null)" ]; then
        echo "port ${proto}/${port} (${REQUIRED_PORTS[$spec]}) is already in use" >&2
        port_fail=1
    fi
done
[ "${port_fail}" -eq 0 ] || { echo "free the ports above before re-running" >&2; exit 1; }

TMP="/tmp/libvirt-e2e"
mkdir -p "${TMP}"

# Settings.

# Use latest stable releases unless specified otherwise. The :latest docker
# tag on ghcr.io/siderolabs/omni does NOT reliably track stable — it's been
# observed pointing at older dev builds — so we resolve the tag from the
# GitHub release API instead.
if [ -z "${OMNI_VERSION:-}" ] ; then
  OMNI_VERSION=$(curl -fsS https://api.github.com/repos/siderolabs/omni/releases/latest | jq -er .tag_name)
fi
if [ -z "${TALOS_VERSION:-}" ] ; then
  TALOS_VERSION=$(curl -fsS https://api.github.com/repos/siderolabs/talos/releases/latest | jq -er .tag_name | sed 's/^v//')
fi

ARTIFACTS=_out
PROJECT=libvirt-e2e

# Repo root. Override when invoking from elsewhere (CI, tests). Relative
# references to hack/, _out/, etc. below all resolve against this.
WORKDIR="${WORKDIR:-$(pwd)}"

# Absolute paths so the compose files (which live in hack/test/) resolve binds correctly.
LIBVIRT_SOCKET_DIR="${WORKDIR}/${ARTIFACTS}/libvirt"
CERTS_DIR="${WORKDIR}/hack/certs"
OMNI_OUTPUT_DIR="${WORKDIR}/${ARTIFACTS}/omni"
OMNI_CONFIG_FILE="${WORKDIR}/${ARTIFACTS}/omni-config.yaml"
LIBVIRT_PROVIDER_CONFIG_PATH="${LIBVIRT_PROVIDER_CONFIG_PATH:-${WORKDIR}/${ARTIFACTS}/libvirt-config-integration-testing.yaml}"
PROVIDER_BINARY="${WORKDIR}/${ARTIFACTS}/omni-infra-provider-libvirt-linux-amd64"

# Dedicated libvirt network and pool, so the test does not collide with a
# libvirt "default" network (192.168.122.0/24) already present on a dev host.
LIBVIRT_NETWORK=omni-e2e
LIBVIRT_BRIDGE=virbr-omni-e2e
LIBVIRT_POOL=omni-e2e
LIBVIRT_GATEWAY=192.168.150.1
LIBVIRT_DHCP_START=192.168.150.10
LIBVIRT_DHCP_END=192.168.150.254

# Omni is advertised to the VMs on the libvirt network gateway.
VIRBR_IP="${LIBVIRT_GATEWAY}"

export OMNI_VERSION LIBVIRT_SOCKET_DIR CERTS_DIR OMNI_OUTPUT_DIR OMNI_CONFIG_FILE
export LIBVIRT_NETWORK LIBVIRT_BRIDGE LIBVIRT_POOL LIBVIRT_GATEWAY LIBVIRT_DHCP_START LIBVIRT_DHCP_END

# Start from empty Omni state. A key and etcd data left by an earlier failed
# run would be reused: Omni does not rewrite the initial service account key
# once that account exists, and the old key has expired by then.
rm -rf "${LIBVIRT_SOCKET_DIR}" "${OMNI_OUTPUT_DIR}"
mkdir -p "${LIBVIRT_SOCKET_DIR}" "${OMNI_OUTPUT_DIR}"

# Compose helpers.

INFRA="-f hack/test/docker-compose.infra.yaml"
OMNI_STACK="${INFRA} -f hack/test/docker-compose.omni.yaml"
COMPOSE="docker compose -p ${PROJECT}"

# Tooling.

# omnictl refuses to talk to a backend with a different API version, so take
# it out of the Omni image under test rather than downloading it separately.
OMNICTL="${TMP}/omnictl"
OMNI_IMAGE="ghcr.io/siderolabs/omni:${OMNI_VERSION}"
docker pull "${OMNI_IMAGE}"
OMNICTL_CONTAINER=$(docker create "${OMNI_IMAGE}")
docker cp "${OMNICTL_CONTAINER}:/omnictl/omnictl-linux-amd64" "${OMNICTL}"
docker rm -v "${OMNICTL_CONTAINER}" >/dev/null
chmod +x ${OMNICTL}

# Optional registry-mirror file consumed as Omni's registries.mirrors[].
# CI generates this via hack/test/build-registries.sh before invoking us;
# locally, drop a file in (see hack/test/registries.yaml.example) or skip
# entirely. No file = no mirrors.
REGISTRIES_FILE="${REGISTRIES_FILE:-${WORKDIR}/hack/test/registries.yaml}"
if [[ -f "${REGISTRIES_FILE}" ]]; then
    MIRRORS_YAML=$(yq -o=json -I=0 '.mirrors // []' "${REGISTRIES_FILE}")
else
    MIRRORS_YAML="[]"
fi
export MIRRORS_YAML

# Cleanup.

SCRIPT_COMPLETED=false
PROVIDER_PID=

function cleanup() {
    if [[ -n "${PROVIDER_PID}" ]]; then
        kill "${PROVIDER_PID}" 2>/dev/null || true
        wait "${PROVIDER_PID}" 2>/dev/null || true
    fi

    # Dump full container logs before teardown so a failed run is debuggable.
    for svc in libvirtd vault-dev omni; do
        if docker inspect "${svc}" >/dev/null 2>&1; then
            docker logs "${svc}" &> "${TMP}/${svc}.log" || true
        fi
    done

    if docker inspect libvirtd >/dev/null 2>&1; then
        docker exec libvirtd virsh list --all &> "${TMP}/virsh-list.log" || true

        # Destroy the network through libvirt, so the bridge and NAT rules it
        # created in the host network namespace are removed as well.
        docker exec libvirtd virsh net-destroy "${LIBVIRT_NETWORK}" || true
    fi

    ${COMPOSE} ${OMNI_STACK} down -v --remove-orphans || true

    # On a successful run, also remove the bind-mount detritus. On failure
    # leave it for inspection (logs in ${TMP}/*.log, omni state in
    # ${OMNI_OUTPUT_DIR}). CI relies on the artifact-upload step instead.
    if [[ "${SCRIPT_COMPLETED}" == "true" && "${CI:-false}" == "false" ]]; then
        # Containers ran as root and dropped root-owned files in the bind
        # mounts. Re-own to the invoking user via a throwaway container so
        # the host-side rm doesn't need sudo. With sudo this is a no-op.
        docker run --rm \
            -v "${TMP}:/cleanup-tmp" \
            -v "${OMNI_OUTPUT_DIR}:/cleanup-omni" \
            -v "${LIBVIRT_SOCKET_DIR}:/cleanup-libvirt" \
            alpine chown -R "$(id -u):$(id -g)" /cleanup-tmp /cleanup-omni /cleanup-libvirt \
            || true
        rm -rf "${TMP}" "${OMNI_OUTPUT_DIR}" "${LIBVIRT_SOCKET_DIR}"
    fi

    # Under `sudo -E` _out ends up root-owned. Hand it back to the invoking
    # user so later non-root CI steps (executable list, artifact upload) can
    # write into it.
    if [[ -n "${SUDO_UID:-}" ]]; then
        chown -R "${SUDO_UID}:${SUDO_GID:-${SUDO_UID}}" "${ARTIFACTS}" || true
    fi
}

trap cleanup EXIT SIGINT

# --- Stage 1: bring up libvirtd + vault ---

${COMPOSE} ${INFRA} up -d --build --wait

# --- Stage 2: load signing key into Vault ---

${COMPOSE} ${INFRA} \
  cp hack/certs/key.private vault:/tmp/key.private
${COMPOSE} ${INFRA} \
  exec -T \
  -e VAULT_ADDR=http://0.0.0.0:8200 \
  -e VAULT_TOKEN=dev-o-token vault \
    vault kv put -mount=secret omni-private-key private-key=@/tmp/key.private

# --- Stage 3: render the Omni config inline and bring Omni up ---

export VIRBR_IP

yq -oyaml --null-input --from-file hack/test/render-omni-config.yq > "${OMNI_CONFIG_FILE}"

# Note: no --wait here. The omni image is FROM scratch (no shell), so we
# can't define a healthcheck inside compose; --wait would either race or
# spuriously consider omni unstable and stop it. We poll the API directly
# below instead.
${COMPOSE} ${OMNI_STACK} up -d

${COMPOSE} ${OMNI_STACK} logs -f omni &> "${TMP}/omni.log" &

# --- Stage 4: wait for Omni's API + service-account key, register infra provider ---

# Poll the HTTPS API until it responds — proves omni is up and serving.
for _ in {1..60}; do
    if curl -ksSf -o /dev/null https://localhost:8099/; then
        break
    fi
    if ! docker inspect -f '{{.State.Running}}' omni 2>/dev/null | grep -q true; then
        echo "omni container exited; see ${TMP}/omni.log" >&2
        exit 1
    fi
    sleep 1
done

# Then wait for the service-account key file.
for _ in {1..60}; do
    if [ -s "${OMNI_OUTPUT_DIR}/key" ]; then
        break
    fi
    sleep 1
done

# Omni writes /_out/key as root inside the container with 0600 perms.
# The omni image is FROM scratch so it has no shell tools; read via a
# throwaway alpine container that has root inside.
ADMIN_SERVICE_ACCOUNT_KEY=$(docker run --rm -v "${OMNI_OUTPUT_DIR}:/x:ro" alpine cat /x/key)

# omnictl prints the provider's own OMNI_ENDPOINT and OMNI_SERVICE_ACCOUNT_KEY.
# Keep them in a separate env file for the provider, so they don't replace the
# admin key the integration test needs.
OMNI_ENDPOINT=https://localhost:8099 OMNI_SERVICE_ACCOUNT_KEY="${ADMIN_SERVICE_ACCOUNT_KEY}" \
    ${OMNICTL} --insecure-skip-tls-verify infraprovider create libvirt \
    | grep -E '^OMNI_(ENDPOINT|SERVICE_ACCOUNT_KEY)=' > "${TMP}/provider.env"

# --- Stage 5: start the libvirt infra provider ---

# The provider runs on the host (as in the proxmox and kubevirt provider
# tests) and talks to libvirtd through the bind-mounted socket. Building the
# image instead would need --load of a multi-platform build from the remote
# CI buildkit, which the docker exporter does not support.
cat > "${LIBVIRT_PROVIDER_CONFIG_PATH}" <<EOF
libvirt:
  uri: qemu:///system?socket=${LIBVIRT_SOCKET_DIR}/libvirt-sock
EOF

(
    set -a
    # shellcheck disable=SC1091
    source "${TMP}/provider.env"
    set +a

    exec "${PROVIDER_BINARY}" \
        --config-file="${LIBVIRT_PROVIDER_CONFIG_PATH}" \
        --image-cache-path="${TMP}/image-cache" \
        --insecure-skip-verify
) &> "${TMP}/provider.log" &
PROVIDER_PID=$!

# Fail early if the provider can't start (bad config, libvirt unreachable).
sleep 10
if ! kill -0 "${PROVIDER_PID}" 2>/dev/null; then
    echo "provider exited; see ${TMP}/provider.log" >&2
    exit 1
fi

# --- Stage 6: run the upstream omni-integration-test suite ---

docker run \
    -v "${CERTS_DIR}":/etc/ssl/certs \
    -e SSL_CERT_DIR=/etc/ssl/certs \
    -e OMNI_SERVICE_ACCOUNT_KEY="${ADMIN_SERVICE_ACCOUNT_KEY}" \
    --network host \
    ghcr.io/siderolabs/omni-integration-test:${OMNI_VERSION} \
    --omni.endpoint https://localhost:8099 \
    --omni.talos-version=${TALOS_VERSION} \
    --test.run "TestIntegration/Suites/(ScaleUpAndDownAutoProvisionMachineSets)" \
    --omni.infra-provider=libvirt \
    --omni.scale-timeout 20m \
    --omni.provider-data="{cores: 2, memory: 2048, disk_size: 10, storage_pool: ${LIBVIRT_POOL}, graphics: none, network_interfaces: [{driver: virtio, network_name: ${LIBVIRT_NETWORK}}]}" \
    --test.failfast \
    --test.v

# Mark success so the trap removes bind-mount state. Anything else (failure
# in stages above) leaves logs and state in place for inspection.
SCRIPT_COMPLETED=true

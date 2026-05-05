#!/bin/bash
# End-to-end integration test for omni-infra-provider-libvirt.
#
# Stages:
#   1. infra (libvirtd + vault) via docker-compose.infra.yaml
#   2. load Vault signing key
#   3. render Omni config inline with yq, bring Omni up via
#      docker-compose.omni.yaml
#   4. wait for Omni service-account key, register infra provider
#   5. build the libvirt provider image, bring it up via
#      docker-compose.provider.yaml
#   6. run the upstream omni-integration-test container
#
# Cleanup: a single `docker compose down -v --remove-orphans` covers every
# service across every stage because the trap registers all compose files.
#
# Requires: docker (with /dev/kvm passthrough, --privileged, --network host),
# crane, jq, yq, curl.

set -eoux pipefail

# Preflight: required tools must be on PATH.
for tool in docker crane jq yq curl git make ss; do
    command -v "${tool}" >/dev/null || { echo "${tool} is required (see hack/test/integration.sh header)"; exit 1; }
done
docker compose version >/dev/null 2>&1 || { echo "docker compose plugin is required"; exit 1; }

# Preflight: required env vars (Auth0 creds for the embedded Omni instance).
: "${AUTH0_TEST_USERNAME:?AUTH0_TEST_USERNAME must be set}"
: "${AUTH0_CLIENT_ID:?AUTH0_CLIENT_ID must be set}"
: "${AUTH0_DOMAIN:?AUTH0_DOMAIN must be set}"

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
  OMNI_VERSION=$(curl -s https://api.github.com/repos/siderolabs/omni/releases/latest | jq -r .tag_name)
fi
if [ -z "${TALOS_VERSION:-}" ] ; then
  TALOS_VERSION=$(curl -s https://api.github.com/repos/siderolabs/talos/releases/latest | jq -r .tag_name | sed 's/^v//')
fi


ARTIFACTS=_out
PROJECT=libvirt-e2e
PLATFORM=$(uname -s | tr "[:upper:]" "[:lower:]")

# Repo root. Override when invoking from elsewhere (CI, tests). Relative
# references to hack/, _out/, etc. below all resolve against this.
WORKDIR="${WORKDIR:-$(pwd)}"

# Absolute paths so the compose files (which live in hack/test/) resolve binds correctly.
LIBVIRT_SOCKET_DIR="${TMP}/libvirt"
CERTS_DIR="${WORKDIR}/hack/certs"
OMNI_OUTPUT_DIR="${WORKDIR}/${ARTIFACTS}/omni"
OMNI_CONFIG_FILE="${WORKDIR}/${ARTIFACTS}/omni-config.yaml"
LIBVIRT_PROVIDER_CONFIG_PATH="${LIBVIRT_PROVIDER_CONFIG_PATH:-${WORKDIR}/${ARTIFACTS}/libvirt-config-integration-testing.yaml}"

# Provider image tag matches what `make image-omni-infra-provider-libvirt`
# produces (REGISTRY/USERNAME/IMAGE:git-describe). Computed here so we can
# reference it in the compose file.
PROVIDER_IMAGE="${PROVIDER_IMAGE:-ghcr.io/siderolabs/omni-infra-provider-libvirt:$(git describe --tag --always --dirty --match 'v[0-9]*' 2>/dev/null || echo dev)}"

VIRBR_IP=192.168.122.1

export OMNI_VERSION LIBVIRT_SOCKET_DIR CERTS_DIR OMNI_OUTPUT_DIR OMNI_CONFIG_FILE LIBVIRT_PROVIDER_CONFIG_PATH PROVIDER_IMAGE

mkdir -p "${LIBVIRT_SOCKET_DIR}" "${OMNI_OUTPUT_DIR}"

# Compose helpers.

INFRA="-f hack/test/docker-compose.infra.yaml"
OMNI_STACK="${INFRA} -f hack/test/docker-compose.omni.yaml"
ALL_STACK="${OMNI_STACK} -f hack/test/docker-compose.provider.yaml"
COMPOSE="docker compose -p ${PROJECT}"

# Tooling.

mkdir -p "${ARTIFACTS}"

[ -f ${ARTIFACTS}/talosctl ] || (crane export ghcr.io/siderolabs/talosctl:latest | tar x -C ${ARTIFACTS})

OMNICTL="${TMP}/omnictl"
curl -Lo ${OMNICTL} "https://github.com/siderolabs/omni/releases/download/${OMNI_VERSION}/omnictl-linux-amd64"
chmod +x ${OMNICTL}

# Optional registry-mirror file consumed as Omni's registries.mirrors[].
# CI generates this via hack/test/build-registries.sh before invoking us;
# locally, drop a file in (see hack/test/registries.yaml.example) or skip
# entirely. No file = no mirrors.
REGISTRIES_FILE="${REGISTRIES_FILE:-${WORKDIR}/hack/test/registries.yaml}"
if [[ -f "${REGISTRIES_FILE}" ]]; then
    MIRRORS_YAML=$(yq -o=json -I=0 .mirrors "${REGISTRIES_FILE}")
else
    MIRRORS_YAML="[]"
fi
export MIRRORS_YAML

# Cleanup.

SCRIPT_COMPLETED=false

function cleanup() {
    # Compose still validates env vars even on `down`. Provide defaults for
    # vars that aren't set yet if the trap fires early (before stage 4).
    : "${OMNI_SERVICE_ACCOUNT_KEY:=cleanup}"
    export OMNI_SERVICE_ACCOUNT_KEY

    # Dump full container logs before teardown so a failed run is debuggable.
    for svc in libvirtd vault-dev omni omni-infra-provider-libvirt; do
        if docker inspect "${svc}" >/dev/null 2>&1; then
            docker logs "${svc}" &> "${TMP}/${svc}.log" || true
        fi
    done

    ${COMPOSE} ${ALL_STACK} down -v --remove-orphans || true

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
            alpine chown -R "$(id -u):$(id -g)" /cleanup-tmp /cleanup-omni \
            || true
        rm -rf "${TMP}" "${OMNI_OUTPUT_DIR}"
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

# Auth0 credentials are validated in the preflight; export them for yq
# substitution in render-omni-config.yq.
export VIRBR_IP AUTH0_TEST_USERNAME AUTH0_CLIENT_ID AUTH0_DOMAIN

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

export OMNI_ENDPOINT=https://localhost:8099
export OMNI_SERVICE_ACCOUNT_KEY
# Omni writes /_out/key as root inside the container with 0600 perms.
# The omni image is FROM scratch so it has no shell tools; read via a
# throwaway alpine container that has root inside.
OMNI_SERVICE_ACCOUNT_KEY=$(docker run --rm -v "${OMNI_OUTPUT_DIR}:/x:ro" alpine cat /x/key)

${OMNICTL} --insecure-skip-tls-verify infraprovider create libvirt | tail -n5 | head -n2 | awk '{print "export " $0}' > "${TMP}/env"
# shellcheck disable=SC1091
source "${TMP}/env"

# --- Stage 5: build the libvirt infra provider image and bring it up ---

# Inside the provider container, the socket is mounted at the canonical
# /var/run/libvirt/libvirt-sock — so the URI is the production default.
cat > "${LIBVIRT_PROVIDER_CONFIG_PATH}" <<EOF
libvirt:
  uri: qemu:///system
EOF

# Canonical kres target. CI_ARGS=--load brings the image back from the
# remote buildkit (CI uses a remote buildx driver) into the local docker
# daemon so compose can run it. Locally, --load is the default and harmless.
make image-omni-infra-provider-libvirt CI_ARGS=--load

${COMPOSE} ${ALL_STACK} up -d --wait

${COMPOSE} ${ALL_STACK} logs -f provider &> "${TMP}/provider.log" &

# --- Stage 6: run the upstream omni-integration-test suite ---

docker run \
    -v "${CERTS_DIR}":/etc/ssl/certs \
    -e SSL_CERT_DIR=/etc/ssl/certs \
    -e OMNI_SERVICE_ACCOUNT_KEY="${OMNI_SERVICE_ACCOUNT_KEY}" \
    --network host \
    ghcr.io/siderolabs/omni-integration-test:${OMNI_VERSION} \
    --omni.endpoint https://localhost:8099 \
    --omni.talos-version=${TALOS_VERSION} \
    --test.run "TestIntegration/Suites/(ScaleUpAndDownAutoProvisionMachineSets)" \
    --omni.infra-provider=libvirt \
    --omni.scale-timeout 10m \
    --omni.provider-data='{cores: 2, memory: 2048, disk_size: 20, storage_pool: default}' \
    --test.failfast \
    --test.v

# Mark success so the trap removes bind-mount state. Anything else (failure
# in stages above) leaves logs and state in place for inspection.
SCRIPT_COMPLETED=true

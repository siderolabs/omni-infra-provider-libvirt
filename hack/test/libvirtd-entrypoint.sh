#!/bin/bash
# Starts libvirtd in a container in the foreground (so docker logs surfaces
# any errors), then activates the default network and pool. Autostart isn't
# honoured here because there's no systemd, so we activate both explicitly
# after the daemon socket is responsive.

set -euo pipefail

# Run libvirtd in the foreground in the background of this script. Stderr
# is captured by docker logs.
/usr/sbin/libvirtd >&2 &
LIBVIRT_PID=$!

ready=0
for _ in {1..30}; do
    if virsh list --all >/dev/null 2>&1; then
        ready=1
        break
    fi
    if ! kill -0 "${LIBVIRT_PID}" 2>/dev/null; then
        echo "libvirtd exited before becoming ready" >&2
        wait "${LIBVIRT_PID}" || true
        exit 1
    fi
    sleep 1
done

if [ "${ready}" -ne 1 ]; then
    echo "libvirtd never became responsive on /var/run/libvirt/libvirt-sock" >&2
    kill "${LIBVIRT_PID}" 2>/dev/null || true
    exit 1
fi

virsh net-start default || true
virsh net-autostart default || true
virsh pool-start default || true
virsh pool-autostart default || true

# Block on libvirtd; if it dies, the container exits.
wait "${LIBVIRT_PID}"

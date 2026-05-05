#!/bin/bash
# Starts libvirtd in a container in the foreground (so docker logs surfaces
# any errors), then defines and starts the test network and storage pool.
# There is no systemd here, so virtlogd is started by hand and nothing is
# autostarted.

set -euo pipefail

# With cgroup v2 the container root cgroup may not hold processes while
# controllers are delegated to children, which breaks the cgroups libvirt
# creates for each VM. Move ourselves into a leaf and delegate all
# controllers, the same trick docker:dind uses.
if [ -f /sys/fs/cgroup/cgroup.controllers ]; then
    mkdir -p /sys/fs/cgroup/init
    xargs -rn1 </sys/fs/cgroup/cgroup.procs >/sys/fs/cgroup/init/cgroup.procs 2>/dev/null || true
    sed -e 's/ / +/g' -e 's/^/+/' </sys/fs/cgroup/cgroup.controllers >/sys/fs/cgroup/cgroup.subtree_control || true
fi

# QEMU logs go through virtlogd by default.
/usr/sbin/virtlogd -d

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

# The container shares the host network namespace, so a bridge left over from
# a previous run would make net-start fail.
ip link del "${LIBVIRT_BRIDGE}" 2>/dev/null || true

# NAT network for the VMs. The gateway is where Omni is reached. The MTU stays
# below the 1450 of the CI runner pod network.
cat >/tmp/network.xml <<EOF
<network>
  <name>${LIBVIRT_NETWORK}</name>
  <forward mode='nat'/>
  <bridge name='${LIBVIRT_BRIDGE}' stp='on' delay='0'/>
  <mtu size='1400'/>
  <ip address='${LIBVIRT_GATEWAY}' netmask='255.255.255.0'>
    <dhcp>
      <range start='${LIBVIRT_DHCP_START}' end='${LIBVIRT_DHCP_END}'/>
    </dhcp>
  </ip>
</network>
EOF

virsh net-define /tmp/network.xml
virsh net-start "${LIBVIRT_NETWORK}"

mkdir -p /var/lib/libvirt/images
virsh pool-define-as "${LIBVIRT_POOL}" dir --target /var/lib/libvirt/images
virsh pool-start "${LIBVIRT_POOL}"

# Signals readiness to the compose healthcheck, after network and pool exist.
touch /run/libvirt-ready

# Block on libvirtd; if it dies, the container exits.
wait "${LIBVIRT_PID}"

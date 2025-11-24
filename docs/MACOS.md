# MacOS

## Configuring storage

```sh
mkdir -p libvirt-storage
virsh pool-define-as --name default --type dir --target "$(pwd)/libvirt-storage"
virsh pool-start default
virsh pool-autostart default
```

## Configure libvirt

```sh
export LIBVIRT_CPU_MAP_PATH=/opt/homebrew/share/libvirt/cpu_map
brew services restart libvirt
```

Without this you may see this error:

```text
failed to start VM: Failed to open file '/opt/homebrew/Cellar/libvirt/11.9.0/share/libvirt/cpu_map/index.xml': No such file or directory
```

## Misc commands for interacting with libvirt

```
LIBVIRT_DEFAULT_URI="qemu:///session"
virsh list --all
```

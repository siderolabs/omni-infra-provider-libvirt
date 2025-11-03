# Omni Infrastructure Provider for libvirt

Can be used to automatically provision Talos nodes in `libvirtd`.

## Running Infrastructure Provider

Create the configuration file for the provider.
For more options see the [libvirt URI docs](https://libvirt.org/uri.html)

### connect to libvirt via ssh

you must ensure to mount the proper SSH keys.
this also requires the SSH user to have access to libvirt on the server side.

```yaml
libvirt:
  uri: 'qemu+libssh://user@hostname/system?known_hosts_verify=ignore'
```

### connect to libvirt via socket

this requires to mount the libvirt socket into the container.

```yaml
libvirt:
  uri: 'qemu:///system'
```

### Using Docker

copy the provider credentials created in omni to an `.env` file

```env
# your omni instance URL
OMNI_ENDPOINT=https://<OMNI_INSTANCE_NAME>.<REGION>.omni.siderolabs.io
# base64 encoded key as shown by omni
OMNI_SERVICE_ACCOUNT_KEY=<PROVIDER_KEY>
```

example for using the above `ssh` based connection method:

```shell
docker run \
  --name omni-infra-provider-libvirt \
  --rm \
  -it \
  -e USER=$USER \
  --env-file /tmp/omni-provider-libvirt.env \
  -v /tmp/omni-provider-libvirt.yaml:/config.yaml \
  -v /home/user/.ssh:/.ssh:ro \
  ghcr.io/siderolabs/omni-infra-provider-libvirt \
    --config-file /config.yaml
```

example for using the above `socket` based connection method:

> **_NOTE:_**
> don't blindly copy-paste this, the location might vary depending on your linux distribution.
> ensure the socket actually exists on your host at the given path.

```shell
docker run \
  --name omni-infra-provider-libvirt \
  --rm \
  -it \
  -e USER=$USER \
  --env-file /tmp/omni-provider-libvirt.env \
  -v /tmp/omni-provider-libvirt.yaml:/config.yaml \
  -v /var/run/libvirt/libvirt-sock:/var/run/libvirt/libvirt-sock:rw \
  ghcr.io/siderolabs/omni-infra-provider-libvirt \
    --config-file /config.yaml
```

## how to use in omni cluster templates

see [test/](./test/) for some examples

## development

see `make help` for general build info.

build an image:

```shell
make generate image-omni-infra-provider-libvirt-linux-amd64
```

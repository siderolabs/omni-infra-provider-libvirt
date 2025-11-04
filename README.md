# Omni Infrastructure Provider for libvirt

Can be used to automatically provision Talos nodes in `libvirtd`.

## Configuration

In your Omni instance under Settings -> Infra Providers, create a new `libvirt` provider.
Make a note of the `OMNI_ENDPOINT` and `OMNI_SERVICE_ACCOUNT_KEY`.

We now show various ways to connect to libvirt.
For more options see the [libvirt URI docs](https://libvirt.org/uri.html)

### Connecting to libvirt via ssh

You must ensure to mount the proper SSH keys.
This also requires the SSH user to have access to libvirt on the server side.

Create the configuration file for the provider:

```yaml
libvirt:
  uri: 'qemu+libssh://user@hostname/system?known_hosts_verify=ignore'
```

### Connecting to libvirt via socket

If using Docker, this requires to mount the libvirt socket into the container.

```yaml
libvirt:
  uri: 'qemu:///system'
  # If using libvirt via Homebrew on MacOS:
  # url: 'qemu:///session?socket=/Users/<username>/.cache/libvirt/libvirt-sock'
```

## Running the provider

### Using Docker

Copy the provider credentials created in omni to an `.env` file

```env
# your omni instance URL
OMNI_ENDPOINT=https://<OMNI_INSTANCE_NAME>.<REGION>.omni.siderolabs.io
# base64 encoded key as shown by omni
OMNI_SERVICE_ACCOUNT_KEY=<PROVIDER_KEY>
```

Example for using the above `ssh` based connection method:

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

Example for using the above `socket` based connection method:

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

## How to use in an Omni cluster templates

See [test/](./test/) for some examples

## Development

See `make help` for general build info.

Build an image:

```shell
make generate image-omni-infra-provider-libvirt-linux-amd64
```

Build the binary:

```shell
# e.g. for darwin
make omni-infra-provider-libvirt-darwin-arm64
```

# Omni Infrastructure Provider for libvirt

Can be used to automatically provision Talos nodes in `libvirtd`.

## Running Infrastructure Provider

Create the configuration file for the provider:

```yaml
# connect to libvirt via ssh
# for more options see: https://libvirt.org/uri.html
libvirt:
  uri: 'qemu+libssh://user@hostname/system?known_hosts_verify=ignore'
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
```bash
docker run --name omni-infra-provider-libvirt --rm -it -e USER=user --env-file /tmp/omni-provider-libvirt.env -v /tmp/omni-provider-libvirt.yaml:/config.yaml -v /home/user/.ssh:/.ssh:ro ghcr.io/siderolabs/omni-infra-provider-libvirt --config-file /config.yaml
```

## how to use in omni cluster templates

see [test/](./test/) for some examples

## development

see `make help` for general build info.

build an image:
```shell
make generate image-omni-infra-provider-libvirt-linux-amd64
```

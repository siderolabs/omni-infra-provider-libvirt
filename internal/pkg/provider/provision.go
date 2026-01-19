// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package provider implements libvirt infra provider core.
package provider

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/digitalocean/go-libvirt"
	"github.com/google/uuid"
	"github.com/siderolabs/omni/client/pkg/infra/provision"
	"go.uber.org/zap"
	"libvirt.org/go/libvirtxml"

	"github.com/siderolabs/omni-infra-provider-libvirt/api/specs"
	"github.com/siderolabs/omni-infra-provider-libvirt/internal/pkg/provider/cidata"
	"github.com/siderolabs/omni-infra-provider-libvirt/internal/pkg/provider/resources"
)

const (
	MiB             = uint64(1024 * 1024)
	GiB             = MiB * 1024
	diskFormatQcow2 = "qcow2"
	diskFormatRaw   = "raw"
)

// Provisioner implements Talos emulator infra provider.
type Provisioner struct {
	libvirtClient *libvirt.Libvirt
	imageCache    *ImageCache
}

// NewProvisioner creates a new provisioner.
func NewProvisioner(libvirtClient *libvirt.Libvirt, imageCache *ImageCache) *Provisioner {
	return &Provisioner{
		libvirtClient: libvirtClient,
		imageCache:    imageCache,
	}
}

var errUploadImage = errors.New("error uploading image")

// ProvisionSteps implements infra.Provisioner.
//
//nolint:gocognit,gocyclo,cyclop,maintidx
func (p *Provisioner) ProvisionSteps() []provision.Step[*resources.Machine] {
	return []provision.Step[*resources.Machine]{
		provision.NewStep(
			"generateUUID",
			func(ctx context.Context, logger *zap.Logger, pctx provision.Context[*resources.Machine]) error {
				newUUID := uuid.New()

				dom, err := p.libvirtClient.DomainLookupByUUID(libvirt.UUID(newUUID))
				if err != nil {
					if dom.UUID != libvirt.UUID(newUUID) {
						// found unused UUID
						pctx.State.TypedSpec().Value.Uuid = newUUID.String()
						pctx.SetMachineUUID(pctx.State.TypedSpec().Value.Uuid)

						return nil
					}

					return provision.NewRetryError(err, time.Second*10)
				}

				return provision.NewRetryInterval(time.Second * 1)
			},
		),

		provision.NewStep(
			"createSchematic",
			func(ctx context.Context, logger *zap.Logger, pctx provision.Context[*resources.Machine]) error {
				schematicId, err := pctx.GenerateSchematicID(ctx, logger)
				if err != nil {
					return provision.NewRetryErrorf(time.Second*10, "error generating schematic ID: %w", err)
				}

				pctx.State.TypedSpec().Value.SchematicId = schematicId
				logger.Info("created schematic " + schematicId)

				return nil
			},
		),

		provision.NewStep(
			"provisionPrimaryDisk",
			func(ctx context.Context, logger *zap.Logger, pctx provision.Context[*resources.Machine]) error {
				var data Data

				err := pctx.UnmarshalProviderData(&data)
				if err != nil {
					return err
				}

				schematicID := pctx.State.TypedSpec().Value.SchematicId
				talosVersion := pctx.GetTalosVersion()

				// Acquire image from cache (downloads if needed, deduplicates concurrent requests)
				filePath, err := p.imageCache.Acquire(ctx, schematicID, talosVersion)
				if err != nil {
					return provision.NewRetryErrorf(time.Second*10, "error fetching image: %w", err)
				}
				defer p.imageCache.Release(schematicID, talosVersion)

				vmName := pctx.GetRequestID()
				volName := fmt.Sprintf("%s.qcow2", vmName)
				pctx.State.TypedSpec().Value.PoolName = data.StoragePool

				vol, err := createVolume(p.libvirtClient, data.StoragePool, volName, diskFormatQcow2, data.DiskSize)
				if err != nil {
					return fmt.Errorf("error creating disk: %w", err)
				}

				fh, err := os.Open(filePath)
				if err != nil {
					return fmt.Errorf("error opening local disk image: %w", err)
				}
				defer fh.Close() //nolint:errcheck

				r, err := gzip.NewReader(fh)
				if err != nil {
					return fmt.Errorf("error opening gzip image reader: %w", err)
				}
				defer r.Close() //nolint:errcheck

				err = p.libvirtClient.StorageVolUpload(vol, r, 0, 0, 0)
				if err != nil {
					return fmt.Errorf("%w: %w", errUploadImage, err)
				}

				volSize := data.DiskSize * GiB

				err = p.libvirtClient.StorageVolResize(vol, volSize, 0)
				if err != nil {
					return fmt.Errorf("expanding volume %s to size %d failed", volName, volSize)
				}

				pctx.State.TypedSpec().Value.VmVolName = volName

				return nil
			},
		),

		provision.NewStep(
			"provisionAdditionalDisks",
			func(ctx context.Context, logger *zap.Logger, pctx provision.Context[*resources.Machine]) error {
				var data Data

				err := pctx.UnmarshalProviderData(&data)
				if err != nil {
					return err
				}

				if len(data.AdditionalDisks) > 0 {
					var (
						additionalDisks []*specs.AdditionalDisk
						vmName          = pctx.GetRequestID()
					)

					for idx, additionalDiskSpec := range data.AdditionalDisks {
						volName := fmt.Sprintf("%s-%d-%s.qcow2", vmName, idx, additionalDiskSpec.Type)
						volSize := additionalDiskSpec.Size * GiB

						_, err = createVolume(p.libvirtClient, data.StoragePool, volName, diskFormatQcow2, volSize)
						if err != nil {
							return fmt.Errorf("error creating disk: %w", err)
						}

						additionalDisks = append(
							additionalDisks,
							&specs.AdditionalDisk{
								Type:    additionalDiskSpec.Type,
								VolName: volName,
							},
						)
					}

					pctx.State.TypedSpec().Value.AdditionalDisks = additionalDisks
				}

				logger.Info("provisioned additional disks", zap.Int("count", len(data.AdditionalDisks)))

				return nil
			},
		),

		provision.NewStep(
			"provisionCidata",
			func(ctx context.Context, logger *zap.Logger, pctx provision.Context[*resources.Machine]) error {
				// create CIDATA for nocloud, contains the hostname
				// docs: https://docs.siderolabs.com/talos/latest/platform-specific-installations/cloud-platforms/nocloud#cdrom%2Fusb
				var data Data

				err := pctx.UnmarshalProviderData(&data)
				if err != nil {
					return err
				}

				var (
					vmName  = pctx.GetRequestID()
					volName = fmt.Sprintf("%s-cidata.iso", vmName)

					metadata    = bytes.NewReader(cidata.MetaData(vmName))
					userdata    = bytes.NewReader([]byte("#cloud-config\n")) // empty
					networkdata = bytes.NewReader(cidata.NetworkData())      // TODO: allow to be passed by user?
				)

				isoData, err := cidata.GenerateCidataISO(metadata, userdata, networkdata)
				if err != nil {
					return fmt.Errorf("error generating cidata ISO: %w", err)
				}

				pool, err := p.libvirtClient.StoragePoolLookupByName(data.StoragePool)
				if err != nil {
					return fmt.Errorf("error looking up storage pool: %w", err)
				}

				// if volume exists, delete old version
				if vol, errGetVol := getVol(p.libvirtClient, data.StoragePool, volName); errGetVol == nil {
					if errVolDel := p.libvirtClient.StorageVolDelete(vol, 0); errVolDel != nil {
						return fmt.Errorf("delete old cidata volume: %w, name: %s", errVolDel, volName)
					}
				}

				volSize := uint64(len(isoData))

				vol, err := createVolume(p.libvirtClient, pool.Name, volName, diskFormatRaw, volSize)
				if err != nil {
					return fmt.Errorf("error creating cidata volume: %w", err)
				}

				err = p.libvirtClient.StorageVolUpload(vol, bytes.NewReader(isoData), 0, 0, 0)
				if err != nil {
					return fmt.Errorf("error uploading cidata ISO: %w", err)
				}

				pctx.State.TypedSpec().Value.CidataVolName = volName

				logger.Info("provisioned cidata ISO", zap.String("volume", volName))

				return nil
			},
		),

		provision.NewStep(
			"createVM",
			func(ctx context.Context, logger *zap.Logger, pctx provision.Context[*resources.Machine]) error {
				volName := pctx.State.TypedSpec().Value.VmVolName
				if volName == "" {
					return provision.NewRetryErrorf(time.Second*10, "waiting for image")
				}

				var data Data

				err := pctx.UnmarshalProviderData(&data)
				if err != nil {
					return err
				}

				vmName := pctx.GetRequestID()

				// assemble primary disk volume

				vol, err := getVol(p.libvirtClient, data.StoragePool, volName)
				if err != nil {
					return provision.NewRetryErrorf(time.Second*10, "error fetching volume: %w", err)
				}

				disks := []libvirtxml.DomainDisk{
					{
						Device: "disk",
						Driver: &libvirtxml.DomainDiskDriver{
							Name:  "qemu",
							Type:  "qcow2",
							Cache: "none",
							IO:    "native",
						},
						Source: &libvirtxml.DomainDiskSource{
							Volume: &libvirtxml.DomainDiskSourceVolume{
								Pool:   vol.Pool,
								Volume: vol.Name,
							},
						},
						Target: &libvirtxml.DomainDiskTarget{
							Dev: "vda",
							Bus: "virtio",
						},
					},
				}

				// assemble additional disk volumes

				var (
					sataDiskCount = 1 // account for root disk
					nvmeDiskCount = 0
				)

				for _, additionalDisk := range pctx.State.TypedSpec().Value.AdditionalDisks {
					var dev, bus string

					switch additionalDisk.Type {
					case "nvme":
						{
							dev = fmt.Sprintf("nvme%dn1", nvmeDiskCount)
							bus = "nvme"
							nvmeDiskCount++
						}
					case "sata":
						{
							idx := sataDiskCount

							s := ""
							for idx >= 0 {
								s = fmt.Sprint(rune('a'+(idx%26))) + s
								idx = idx/26 - 1
							}

							dev = fmt.Sprintf("sd%s", s)
							bus = "virtio"
							sataDiskCount++
						}
					default:
						{
							return fmt.Errorf("unknown disk type: %q", additionalDisk.Type)
						}
					}

					additionalDisk := libvirtxml.DomainDisk{
						Device: "disk",
						Driver: &libvirtxml.DomainDiskDriver{
							Name:  "qemu",
							Type:  "qcow2",
							Cache: "none",
							IO:    "native",
						},
						Source: &libvirtxml.DomainDiskSource{
							Volume: &libvirtxml.DomainDiskSourceVolume{
								Pool:   data.StoragePool,
								Volume: additionalDisk.VolName,
							},
						},
						Target: &libvirtxml.DomainDiskTarget{
							Dev: dev,
							Bus: bus,
						},
						Serial: uuid.NewString(),
					}

					disks = append(disks, additionalDisk)
				}

				// add cidata ISO as cdrom, if present
				cidataVolName := pctx.State.TypedSpec().Value.CidataVolName
				if cidataVolName != "" {
					cidataDisk := libvirtxml.DomainDisk{
						Device: "cdrom",
						Driver: &libvirtxml.DomainDiskDriver{
							Name: "qemu",
							Type: "raw",
						},
						Source: &libvirtxml.DomainDiskSource{
							Volume: &libvirtxml.DomainDiskSourceVolume{
								Pool:   data.StoragePool,
								Volume: cidataVolName,
							},
						},
						Target: &libvirtxml.DomainDiskTarget{
							Dev: "sda",
							Bus: "sata",
						},
						ReadOnly: &libvirtxml.DomainDiskReadOnly{},
					}

					disks = append(disks, cidataDisk)
				}

				// assemble network interfaces

				var networkInterfaces []libvirtxml.DomainInterface

				for _, ifaceData := range data.NetworkInterfaces {
					iface := libvirtxml.DomainInterface{
						Model: &libvirtxml.DomainInterfaceModel{
							Type: ifaceData.Driver,
						},
						Source: &libvirtxml.DomainInterfaceSource{
							Network: &libvirtxml.DomainInterfaceSourceNetwork{
								Network: ifaceData.NetworkName,
							},
						},
					}

					networkInterfaces = append(networkInterfaces, iface)
				}

				// generate libvirt XML spec
				// https://libvirt.org/html/libvirt-libvirt-domain.html#virDomainCreateXML
				domData := libvirtxml.Domain{
					Type: "kvm",
					Name: vmName,
					// this one is really important, it has to match the UUID in omni
					UUID: pctx.State.TypedSpec().Value.Uuid,
					Memory: &libvirtxml.DomainMemory{
						Unit:  "MiB",
						Value: data.Memory,
					},
					VCPU: &libvirtxml.DomainVCPU{
						Placement: "static",
						Value:     data.Cores,
					},
					OS: &libvirtxml.DomainOS{
						Type: &libvirtxml.DomainOSType{
							Arch:    "x86_64",
							Machine: "q35",
							Type:    "hvm",
						},
						BootDevices: []libvirtxml.DomainBootDevice{
							{Dev: "hd"},
						},
					},
					CPU: &libvirtxml.DomainCPU{
						Mode: "host-passthrough",
					},
					Features: &libvirtxml.DomainFeatureList{
						ACPI: &libvirtxml.DomainFeature{},
						APIC: &libvirtxml.DomainFeatureAPIC{},
					},
					Devices: &libvirtxml.DomainDeviceList{
						Channels: []libvirtxml.DomainChannel{
							{
								Source: &libvirtxml.DomainChardevSource{
									UNIX: &libvirtxml.DomainChardevSourceUNIX{
										Mode: "bind",
										Path: "/var/lib/libvirt/qemu/channel/target/omni-node-001.org.qemu.guest_agent.0",
									},
								},
								Target: &libvirtxml.DomainChannelTarget{
									VirtIO: &libvirtxml.DomainChannelTargetVirtIO{
										Name: "org.qemu.guest_agent.0",
									},
								},
							},
						},
						Emulator:   "", // let libvirt pick qemu-system-x86_64
						Disks:      disks,
						Interfaces: networkInterfaces,
						MemBalloon: &libvirtxml.DomainMemBalloon{
							Model: "virtio",
						},
						Serials: []libvirtxml.DomainSerial{
							// { Target: &libvirtxml.DomainSerialTarget{Type: "pty",}},
						},
						Consoles: []libvirtxml.DomainConsole{
							{
								Target: &libvirtxml.DomainConsoleTarget{
									Type: "serial",
								},
							},
							// {Target: &libvirtxml.DomainConsoleTarget{Type: "virtio"}},
						},
						Videos: []libvirtxml.DomainVideo{
							{
								Model: libvirtxml.DomainVideoModel{
									Type: "virtio",
									Resolution: &libvirtxml.DomainVideoResolution{
										X: 1920,
										Y: 1080,
									},
								},
							},
						},
						Graphics: []libvirtxml.DomainGraphic{
							{
								Spice: &libvirtxml.DomainGraphicSpice{
									AutoPort: "yes",
								},
							},
						},
					},
				}

				domXML, err := domData.Marshal()
				if err != nil {
					return fmt.Errorf("error rendering domain XML: %w", err)
				}

				logger.Debug("domain XML", zap.String("xml_data", domXML))

				// create domain
				_, err = p.libvirtClient.DomainDefineXML(domXML)
				if err != nil {
					return fmt.Errorf("creating domain: %w", err)
				}

				// set VM id in omni
				pctx.State.TypedSpec().Value.VmName = vmName

				return nil
			},
		),

		provision.NewStep(
			"startVM",
			func(ctx context.Context, logger *zap.Logger, pctx provision.Context[*resources.Machine]) error {
				vmName := pctx.State.TypedSpec().Value.VmName

				dom, err := p.libvirtClient.DomainLookupByName(vmName)
				if err != nil {
					return provision.NewRetryErrorf(time.Second*10, "VM lookup failed: %w", err)
				}

				domState, _, err := p.libvirtClient.DomainGetState(dom, 0)
				if err != nil {
					return provision.NewRetryErrorf(time.Second*10, "error fetching domain state: %w", err)
				}

				if libvirt.DomainState(domState) == libvirt.DomainRunning {
					return nil
				}

				err = p.libvirtClient.DomainCreate(dom)
				if err != nil {
					if !strings.Contains(err.Error(), "domain is already running") {
						return provision.NewRetryErrorf(time.Second*10, "failed to start VM: %w", err)
					}
				}

				return nil
			},
		),
	}
}

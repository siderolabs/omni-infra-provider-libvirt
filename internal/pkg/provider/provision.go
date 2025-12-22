// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package provider implements libvirt infra provider core.
package provider

import (
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
	"github.com/siderolabs/omni/client/pkg/omni/resources/infra"
	"go.uber.org/zap"
	"libvirt.org/go/libvirtxml"

	"github.com/siderolabs/omni-infra-provider-libvirt/api/specs"
	"github.com/siderolabs/omni-infra-provider-libvirt/internal/pkg/provider/resources"
)

const (
	GiB             = uint64(1024 * 1024 * 1024)
	diskFormatQcow2 = "qcow2"
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

var (
	errCreateVol   = errors.New("error creating volume")
	errUploadImage = errors.New("error uploading image")
	errVolNoExist  = errors.New("volume does not exist")
)

func getVol(lc *libvirt.Libvirt, poolName, volName string) (libvirt.StorageVol, error) {
	var vol libvirt.StorageVol

	pool, err := lc.StoragePoolLookupByName(poolName)
	if err != nil {
		return vol, fmt.Errorf("getvol: %w", err)
	}

	vol, err = lc.StorageVolLookupByName(pool, volName)
	if err != nil {
		// TODO: there is probably a better way to check this
		if strings.Contains(err.Error(), "Storage volume not found") {
			return vol, errVolNoExist
		}

		return vol, err
	}

	return vol, nil
}

func createVolume(lc *libvirt.Libvirt, poolName, volumeName, format string, capacity uint64) (libvirt.StorageVol, error) {
	if vol, err := getVol(lc, poolName, volumeName); err == nil {
		return vol, nil
	}

	var vol libvirt.StorageVol

	pool, err := lc.StoragePoolLookupByName(poolName)
	if err != nil {
		return vol, fmt.Errorf("%w: %w", errCreateVol, err)
	}

	volData := libvirtxml.StorageVolume{
		Type: "file",
		Name: volumeName,
		Allocation: &libvirtxml.StorageVolumeSize{
			// thin provision: allocate zero bytes at time of creation
			Unit:  "bytes",
			Value: 0,
		},
		Capacity: &libvirtxml.StorageVolumeSize{
			Unit:  "bytes",
			Value: capacity,
		},
		Target: &libvirtxml.StorageVolumeTarget{
			Format: &libvirtxml.StorageVolumeTargetFormat{
				Type: format,
			},
		},
	}

	volXML, err := volData.Marshal()
	if err != nil {
		return vol, fmt.Errorf("%w, error rendering XML: %w", errCreateVol, err)
	}

	vol, err = lc.StorageVolCreateXML(pool, volXML, 0)
	if err != nil {
		return vol, fmt.Errorf("%w: error creating volume: %w", errCreateVol, err)
	}

	return vol, nil
}

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

		provision.NewStep("configureHostname", func(ctx context.Context, _ *zap.Logger, pctx provision.Context[*resources.Machine]) error {
			patch := fmt.Sprintf("machine:\n  network:\n    hostname: %s", pctx.GetRequestID())

			return pctx.CreateConfigPatch(ctx, "000-hostname-%s"+pctx.GetRequestID(), []byte(patch))
		}),

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

// Deprovision implements infra.Provisioner.
func (p *Provisioner) Deprovision(ctx context.Context, logger *zap.Logger, machine *resources.Machine, machineRequest *infra.MachineRequest) error {
	vmName := machineRequest.Metadata().ID()

	if vmName == "" {
		return provision.NewRetryError(errors.New("empty vmName"), time.Second*10)
	}

	dom, err := p.libvirtClient.DomainLookupByName(vmName)
	if err != nil {
		if strings.Contains(err.Error(), "Domain not found") {
			logger.Info("domain was alredy removed: " + vmName)
		} else {
			return fmt.Errorf("fetching domain: %w", err)
		}
	} else {
		logger.Info("found domain " + vmName)

		state, _, err := p.libvirtClient.DomainGetState(dom, 0) //nolint:govet
		if err != nil {
			return fmt.Errorf("fetching domain state: %w", err)
		}

		switch state {
		case int32(libvirt.DomainRunning):
			{
				// in libvirt, "destroy" translates to "shut down" or "power off"
				err = p.libvirtClient.DomainDestroy(dom)
				if err != nil {
					return fmt.Errorf("destroy domain: %w", err)
				}

				logger.Info("destroyed domain " + vmName)

				return provision.NewRetryInterval(time.Second * 3)
			}
		case int32(libvirt.DomainShutdown):
			return provision.NewRetryInterval(time.Second * 10)
		case int32(libvirt.DomainShutoff):
			{
				// in libvirt, "undefine" translates to "delete a VM"
				err = p.libvirtClient.DomainUndefine(dom)
				if err != nil {
					return fmt.Errorf("undefine VM: %w", err)
				}

				logger.Info("undefined domain " + vmName)
			}
		default:
			return provision.NewRetryErrorf(time.Second*10, "unknown VM state: %v", state)
		}
	}

	poolName := machine.TypedSpec().Value.PoolName
	volName := machine.TypedSpec().Value.VmVolName

	if poolName == "" || volName == "" {
		return fmt.Errorf("empty pool/vol names: %s/%s", poolName, volName)
	}

	vol, err := getVol(p.libvirtClient, poolName, volName)
	if err != nil {
		if !errors.Is(err, errVolNoExist) {
			return fmt.Errorf("error fetching volume: %w", err)
		}

		logger.Info("volume was removed already: " + volName)
	} else {
		err = p.libvirtClient.StorageVolDelete(vol, 0)
		if err != nil {
			return fmt.Errorf("error deleting volume: %w", err)
		}

		logger.Info("removed volume: " + volName)
	}

	for _, additionalDisk := range machine.TypedSpec().Value.AdditionalDisks {
		additionalVolume, err := getVol(p.libvirtClient, poolName, additionalDisk.VolName)
		if err != nil {
			if !errors.Is(err, errVolNoExist) {
				return fmt.Errorf("fetching volume %s: %w", additionalDisk.VolName, err)
			}

			logger.Info("volume was removed already: " + additionalDisk.VolName)

			continue
		}

		err = p.libvirtClient.StorageVolDelete(additionalVolume, 0)
		if err != nil {
			return fmt.Errorf("error deleting volume: %w", err)
		}

		logger.Info("removed volume: " + additionalDisk.VolName)
	}

	return nil
}

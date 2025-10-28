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
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/digitalocean/go-libvirt"
	"github.com/google/uuid"
	"github.com/siderolabs/omni/client/pkg/constants"
	"github.com/siderolabs/omni/client/pkg/infra/provision"
	"github.com/siderolabs/omni/client/pkg/omni/resources/infra"
	"go.uber.org/zap"
	"libvirt.org/go/libvirtxml"

	"github.com/siderolabs/omni-infra-provider-libvirt/internal/pkg/provider/resources"
)

const (
	GiB = uint64(1024 * 1024 * 1024)
)

// Provisioner implements Talos emulator infra provider.
type Provisioner struct {
	libvirtClient *libvirt.Libvirt
}

// NewProvisioner creates a new provisioner.
func NewProvisioner(libvirtClient *libvirt.Libvirt) *Provisioner {
	return &Provisioner{
		libvirtClient: libvirtClient,
	}
}

var (
	timeout        = time.Second * 120
	downloadIsoErr = errors.New("error downloading image")
	createVolErr   = errors.New("error creating volume")
	uploadIsoErr   = errors.New("error uploading image")
	volNoExist     = errors.New("volume does not exist")
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
			return vol, volNoExist
		}

		return vol, err
	}

	return vol, nil
}

func createVolume(lc *libvirt.Libvirt, poolName, volumeName string, capacity uint64) (libvirt.StorageVol, error) {
	if vol, err := getVol(lc, poolName, volumeName); err == nil {
		return vol, err
	}

	var vol libvirt.StorageVol
	pool, err := lc.StoragePoolLookupByName(poolName)
	if err != nil {
		return vol, fmt.Errorf("%w: %w", createVolErr, err)
	}

	volData := libvirtxml.StorageVolume{
		Type: "file",
		Name: volumeName,
		Allocation: &libvirtxml.StorageVolumeSize{
			Unit:  "bytes",
			Value: 0,
		},
		Capacity: &libvirtxml.StorageVolumeSize{
			Unit:  "bytes",
			Value: capacity,
		},
		Target: &libvirtxml.StorageVolumeTarget{
			Format: &libvirtxml.StorageVolumeTargetFormat{
				Type: "raw",
			},
		},
	}
	volXML, err := volData.Marshal()
	if err != nil {
		return vol, fmt.Errorf("%w, error rendering XML: %w", createVolErr, err)
	}

	vol, err = lc.StorageVolCreateXML(pool, volXML, 0)
	if err != nil {
		return vol, fmt.Errorf("%w: error creating volume: %w", createVolErr, err)
	}

	return vol, nil
}

// ProvisionSteps implements infra.Provisioner.
//
//nolint:gocognit,gocyclo,cyclop,maintidx
func (p *Provisioner) ProvisionSteps() []provision.Step[*resources.Machine] {
	return []provision.Step[*resources.Machine]{
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
			"fetchDiskImage",
			func(ctx context.Context, logger *zap.Logger, pctx provision.Context[*resources.Machine]) error {
				var data Data
				if err := pctx.UnmarshalProviderData(&data); err != nil {
					return err
				}

				outDir := "/tmp"
				fileName := fmt.Sprintf("%s.qcow2.gz", pctx.State.TypedSpec().Value.SchematicId)
				filePath := path.Join(outDir, fileName)

				_, err := os.Stat(filePath)
				if err != nil {
					if errors.Is(err, os.ErrNotExist) {
						// image does not exist, so fetch it
						imageURL, err := url.Parse(constants.ImageFactoryBaseURL)
						if err != nil {
							return err
						}

						imageURL = imageURL.JoinPath(
							"image",
							pctx.State.TypedSpec().Value.SchematicId,
							pctx.GetTalosVersion(),
							"metal-amd64.qcow2.gz",
						)

						req, err := http.NewRequest(http.MethodGet, imageURL.String(), nil)
						if err != nil {
							return fmt.Errorf("error creating request: %w", err)
						}

						client := http.Client{
							CheckRedirect: func(req *http.Request, via []*http.Request) error {
								return nil // follow all redirects
							},
							Timeout: timeout,
						}

						res, err := client.Do(req)
						if err != nil {
							return provision.NewRetryErrorf(time.Second*10, "error fetching image: %w", err)
						}

						imageData, err := io.ReadAll(res.Body)
						if err != nil {
							return provision.NewRetryErrorf(time.Second*10, "%w: error reading response body: %w", downloadIsoErr, err)
						}

						err = os.WriteFile(filePath, imageData, 0o740)
						if err != nil {
							return fmt.Errorf("error writing image to disk: %w", err)
						}
					} else {
						// already exists
						return nil
					}
				}

				return nil
			},
		),
		provision.NewStep(
			"provisionDisk",
			func(ctx context.Context, logger *zap.Logger, pctx provision.Context[*resources.Machine]) error {
				var data Data
				err := pctx.UnmarshalProviderData(&data)
				if err != nil {
					return err
				}

				vmName := pctx.GetRequestID()
				volName := fmt.Sprintf("%s.qcow2", vmName)
				vol, err := createVolume(p.libvirtClient, data.StoragePool, volName, uint64(data.DiskSize))
				if err != nil {
					return fmt.Errorf("error creating disk: %w", err)
				}

				outDir := "/tmp"
				fileName := fmt.Sprintf("%s.qcow2.gz", pctx.State.TypedSpec().Value.SchematicId)
				filePath := path.Join(outDir, fileName)
				fh, err := os.Open(filePath)
				if err != nil {
					return fmt.Errorf("error opening local disk image: %w", err)
				}
				r, err := gzip.NewReader(fh)
				if err != nil {
					return fmt.Errorf("error opening gzip image reader: %w", err)
				}

				err = p.libvirtClient.StorageVolUpload(vol, r, 0, 0, 0)
				if err != nil {
					return fmt.Errorf("error uploading disk image: %w", err)
				}

				volSize := data.DiskSize * GiB
				err = p.libvirtClient.StorageVolResize(vol, volSize, 0)
				if err != nil {
					return fmt.Errorf("expanding volume %s to size %d failed", volName, volSize)
				}

				pctx.State.TypedSpec().Value.VmVolName = volName

				// TODO: we could try to be clever here and cache files
				err = os.Remove(fileName)
				if err != nil {
					if !errors.Is(err, os.ErrNotExist) {
						return fmt.Errorf("error deleting %s: %w", fileName, err)
					}
					// if it is not there, it is okay if it couldn't be deleted.
				}

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

				if pctx.State.TypedSpec().Value.Uuid == "" {
					pctx.State.TypedSpec().Value.Uuid = uuid.NewString()
					pctx.SetMachineUUID(pctx.State.TypedSpec().Value.Uuid)
				}

				vmName := pctx.GetRequestID()
				vol, err := getVol(p.libvirtClient, data.StoragePool, volName)
				if err != nil {
					return provision.NewRetryErrorf(time.Second*10, "error fetching volume: %w", err)
				}

				// https://libvirt.org/html/libvirt-libvirt-domain.html#virDomainCreateXML
				domData := libvirtxml.Domain{
					Type: "kvm",
					Name: vmName,
					// this one is really important, it has to match the UUID in omni
					UUID: pctx.State.TypedSpec().Value.Uuid,
					Memory: &libvirtxml.DomainMemory{
						Unit:  "MiB",
						Value: uint(data.Memory), // KiB
					},
					VCPU: &libvirtxml.DomainVCPU{
						Placement: "static",
						Value:     uint(data.Cores),
					},
					OS: &libvirtxml.DomainOS{
						Type: &libvirtxml.DomainOSType{Arch: "x86_64", Machine: "q35", Type: "hvm"},
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
						Emulator: "", // let libvirt pick qemu-system-x86_64
						Disks: []libvirtxml.DomainDisk{
							{
								Device: "disk",
								Driver: &libvirtxml.DomainDiskDriver{Name: "qemu", Type: "qcow2"},
								Source: &libvirtxml.DomainDiskSource{
									Volume: &libvirtxml.DomainDiskSourceVolume{
										Pool:   vol.Pool,
										Volume: vol.Name,
									},
								},
								Target: &libvirtxml.DomainDiskTarget{Dev: "vda", Bus: "virtio"},
							},
						},
						Interfaces: []libvirtxml.DomainInterface{
							{
								Model: &libvirtxml.DomainInterfaceModel{Type: "virtio"},
								Source: &libvirtxml.DomainInterfaceSource{
									Network: &libvirtxml.DomainInterfaceSourceNetwork{Network: data.Network},
								},
							},
						},
						Serials: []libvirtxml.DomainSerial{
							//{ Target: &libvirtxml.DomainSerialTarget{Type: "pty",}},
						},
						Consoles: []libvirtxml.DomainConsole{
							{Target: &libvirtxml.DomainConsoleTarget{Type: "serial"}},
							//{Target: &libvirtxml.DomainConsoleTarget{Type: "virtio"}},
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
							{VNC: &libvirtxml.DomainGraphicVNC{AutoPort: "yes"}},
						},
					},
				}
				domXML, err := domData.Marshal()
				if err != nil {
					return fmt.Errorf("error rendering domain XML: %w", err)
				}

				//log.Println(domXML)

				if pctx.State.TypedSpec().Value.Uuid == "" {
					pctx.State.TypedSpec().Value.Uuid = uuid.NewString()
					pctx.SetMachineUUID(pctx.State.TypedSpec().Value.Uuid)
				}

				// create domain
				_, err = p.libvirtClient.DomainDefineXML(domXML)
				if err != nil {
					return fmt.Errorf("error creating domain: %w", err)
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
	var vmName string
	if machine == nil {
		vmName = machineRequest.Metadata().ID()
	} else {
		vmName = machine.TypedSpec().Value.VmName
	}

	if vmName == "" {
		return provision.NewRetryError(errors.New("empty vmName"), time.Second*10)
	}

	dom, err := p.libvirtClient.DomainLookupByName(vmName)
	if err != nil {
		if strings.Contains(err.Error(), "Domain not found") {
			logger.Info("domain was alredy removed: " + vmName)
			return nil
		} else {
			return fmt.Errorf("error fetching domain: %w", err)
		}
	}
	logger.Info("found domain " + vmName)

	if err := p.libvirtClient.DomainDestroy(dom); err != nil {
		return fmt.Errorf("error shutting down domain: %w", err)
	}
	logger.Info("destroyed domain " + vmName)

	if err := p.libvirtClient.DomainUndefine(dom); err != nil {
		return fmt.Errorf("error undefining VM: %w", err)
	}
	logger.Info("undefined domain " + vmName)

	if machine != nil {
		_, err = getVol(p.libvirtClient, machine.TypedSpec().Value.PoolName, machine.TypedSpec().Value.VmVolName)
		if err != nil {
			if !errors.Is(err, volNoExist) {
				return fmt.Errorf("error removing volume: %w", err)
			}
		}
	}

	return nil
}

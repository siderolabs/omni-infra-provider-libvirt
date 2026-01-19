// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/digitalocean/go-libvirt"
	"github.com/siderolabs/omni/client/pkg/infra/provision"
	"github.com/siderolabs/omni/client/pkg/omni/resources/infra"
	"go.uber.org/zap"

	"github.com/siderolabs/omni-infra-provider-libvirt/internal/pkg/provider/resources"
)

// Deprovision implements infra.Provisioner.
func (p *Provisioner) Deprovision(ctx context.Context, logger *zap.Logger, machine *resources.Machine, machineRequest *infra.MachineRequest) error {
	vmName := machineRequest.Metadata().ID()

	if vmName == "" {
		return provision.NewRetryError(errors.New("empty vmName"), time.Second*10)
	}

	if err := removeDomain(p.libvirtClient, vmName, logger); err != nil {
		return err
	}

	poolName := machine.TypedSpec().Value.PoolName
	volName := machine.TypedSpec().Value.VmVolName

	if poolName == "" || volName == "" {
		return fmt.Errorf("empty pool/vol names: %s/%s", poolName, volName)
	}

	if err := removeVolMain(p.libvirtClient, volName, poolName, logger); err != nil {
		return err
	}

	if err := removeVolAdditionalDisks(p.libvirtClient, machine, poolName, logger); err != nil {
		return err
	}

	if err := removeVolCidata(p.libvirtClient, machine, poolName, logger); err != nil {
		return err
	}

	return nil
}

func removeDomain(lc *libvirt.Libvirt, vmName string, logger *zap.Logger) error {
	dom, err := lc.DomainLookupByName(vmName)
	if err != nil {
		if strings.Contains(err.Error(), "Domain not found") {
			logger.Info("domain was alredy removed: " + vmName)
		} else {
			return fmt.Errorf("fetching domain: %w", err)
		}
	} else {
		logger.Info("found domain " + vmName)

		state, _, err := lc.DomainGetState(dom, 0) //nolint:govet
		if err != nil {
			return fmt.Errorf("fetching domain state: %w", err)
		}

		switch state {
		case int32(libvirt.DomainRunning):
			{
				// in libvirt, "destroy" translates to "shut down" or "power off"
				err = lc.DomainDestroy(dom)
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
				err = lc.DomainUndefine(dom)
				if err != nil {
					return fmt.Errorf("undefine VM: %w", err)
				}

				logger.Info("undefined domain " + vmName)
			}
		default:
			return provision.NewRetryErrorf(time.Second*10, "unknown VM state: %v", state)
		}
	}

	return nil
}

func removeVolMain(lc *libvirt.Libvirt, volName, poolName string, logger *zap.Logger) error {
	vol, err := getVol(lc, poolName, volName)
	if err != nil {
		if !errors.Is(err, errVolNoExist) {
			return fmt.Errorf("error fetching volume: %w", err)
		}

		logger.Info("volume was removed already: " + volName)
	} else {
		err = lc.StorageVolDelete(vol, 0)
		if err != nil {
			return fmt.Errorf("error deleting volume: %w", err)
		}

		logger.Info("removed volume: " + volName)
	}

	return nil
}

func removeVolAdditionalDisks(lc *libvirt.Libvirt, machine *resources.Machine, poolName string, logger *zap.Logger) error {
	for _, additionalDisk := range machine.TypedSpec().Value.AdditionalDisks {
		additionalVolume, err := getVol(lc, poolName, additionalDisk.VolName)
		if err != nil {
			if !errors.Is(err, errVolNoExist) {
				return fmt.Errorf("fetching volume %s: %w", additionalDisk.VolName, err)
			}

			logger.Info("volume was removed already: " + additionalDisk.VolName)

			continue
		}

		err = lc.StorageVolDelete(additionalVolume, 0)
		if err != nil {
			return fmt.Errorf("error deleting volume: %w", err)
		}

		logger.Info("removed volume: " + additionalDisk.VolName)
	}

	return nil
}

func removeVolCidata(lc *libvirt.Libvirt, machine *resources.Machine, poolName string, logger *zap.Logger) error {
	if cidataVolName := machine.TypedSpec().Value.CidataVolName; cidataVolName != "" {
		cidataVol, err := getVol(lc, poolName, cidataVolName)
		if err != nil {
			if !errors.Is(err, errVolNoExist) {
				return fmt.Errorf("fetching cidata volume %s: %w", cidataVolName, err)
			}

			logger.Info("cidata volume was removed already: " + cidataVolName)
		} else {
			err = lc.StorageVolDelete(cidataVol, 0)
			if err != nil {
				return fmt.Errorf("error deleting cidata volume: %w", err)
			}

			logger.Info("removed cidata volume: " + cidataVolName)
		}
	}

	return nil
}

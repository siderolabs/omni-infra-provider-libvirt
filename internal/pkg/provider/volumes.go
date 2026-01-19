// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package provider

import (
	"errors"
	"fmt"
	"strings"

	"github.com/digitalocean/go-libvirt"
	"libvirt.org/go/libvirtxml"
)

var (
	errCreateVol  = errors.New("error creating volume")
	errVolNoExist = errors.New("volume does not exist")
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

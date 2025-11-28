// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package provider

// Data is the provider custom machine config.
type Data struct {
	StoragePool       string             `yaml:"storage_pool"`
	NetworkInterfaces []networkInterface `yaml:"network_interfaces,omitempty"`
	AdditionalDisks   []additionalDisk   `yaml:"additional_disks,omitempty"`
	DiskSize          uint64             `yaml:"disk_size"`
	Cores             uint               `yaml:"cores"`
	Memory            uint               `yaml:"memory"`
}

type additionalDisk struct {
	Type string `yaml:"type"`
	Size uint64 `yaml:"size"` // GiB
}

type networkInterface struct {
	Driver      string `yaml:"driver"`
	NetworkName string `yaml:"network_name"`
}

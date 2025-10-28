// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package provider

// Data is the provider custom machine config.
type Data struct {
	Cores       uint   `yaml:"cores"`
	Memory      uint64 `yaml:"memory"`    // MB
	DiskSize    uint64 `yaml:"disk_size"` // GB
	StoragePool string `yaml:"storage_pool"`
	Network     string `yaml:"network"`
}

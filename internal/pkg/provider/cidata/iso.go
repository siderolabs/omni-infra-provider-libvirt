// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package cidata

import (
	"bytes"
	"fmt"

	"github.com/kdomanski/iso9660"
)

const (
	cidataVolumeLabel = "cidata"
)

// GenerateCidataISO creates an ISO9660 image containing nocloud metadata.
// The ISO contains meta-data, user-data, and network-config files required
// by the nocloud datasource.
func GenerateCidataISO(metadata, userdata, networkdata *bytes.Reader) ([]byte, error) {
	writer, err := iso9660.NewWriter()
	if err != nil {
		return nil, fmt.Errorf("failed to create ISO writer: %w", err)
	}
	defer writer.Cleanup() //nolint:errcheck

	if err := writer.AddFile(metadata, "meta-data"); err != nil {
		return nil, fmt.Errorf("failed to add meta-data: %w", err)
	}

	if err := writer.AddFile(userdata, "user-data"); err != nil {
		return nil, fmt.Errorf("failed to add user-data: %w", err)
	}

	if err := writer.AddFile(networkdata, "network-config"); err != nil {
		return nil, fmt.Errorf("failed to add network-config: %w", err)
	}

	var buf bytes.Buffer

	if err := writer.WriteTo(&buf, cidataVolumeLabel); err != nil {
		return nil, fmt.Errorf("failed to write ISO: %w", err)
	}

	return buf.Bytes(), nil
}

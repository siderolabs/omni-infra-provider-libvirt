// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package cidata

import "fmt"

func MetaData(hostname string) []byte {
	return fmt.Appendf(nil, "local-hostname: %s\n", hostname)
}

const defaultNetworkData = `version: 2
ethernets:
  all-en:
    match:
      name: "en*"
    dhcp4: true
    dhcp6: true
  all-eth:
    match:
      name: "eth*"
    dhcp4: true
    dhcp6: true
`

func NetworkData() []byte {
	return []byte(defaultNetworkData)
}

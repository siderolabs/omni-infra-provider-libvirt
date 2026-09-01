## [Omni Infra Provider libvirt 0.3.0](https://github.com/siderolabs/omni-infra-provider-libvirt/releases/tag/v0.3.0) (2026-09-01)

Welcome to the v0.3.0 release of Omni Infra Provider libvirt!



Please try out the release binaries and report any issues at
https://github.com/siderolabs/omni-infra-provider-libvirt/issues.

### Contributors

* Fritz Schaal
* Oguz Kilcan

### Changes
<details><summary>5 commits</summary>
<p>

* [`13c631b`](https://github.com/siderolabs/omni-infra-provider-libvirt/commit/13c631bae77884fcb99ec2f1d6d80077d7b1a9ab) chore: bump dependencies, rekres
* [`8c883fd`](https://github.com/siderolabs/omni-infra-provider-libvirt/commit/8c883fdbaa66d7a311748de3a2b294fad480872f) chore: make vnc default for graphics
* [`6e5282d`](https://github.com/siderolabs/omni-infra-provider-libvirt/commit/6e5282d2b4a788f4b53d0e64483d52c618556058) fix: set correct sata disk type and dev name
* [`b273bef`](https://github.com/siderolabs/omni-infra-provider-libvirt/commit/b273bef5f0a11f0b6eab6aaa00049a6c285027c4) fix: limit disk serial length to 20
* [`e16c55e`](https://github.com/siderolabs/omni-infra-provider-libvirt/commit/e16c55e3a5b35b094a0e82bb35fd07496f9b759f) feat: resolve disk images through Omni's installation media API
</p>
</details>

### Dependency Changes

* **github.com/cosi-project/runtime**     v1.16.2 -> v1.16.3
* **github.com/digitalocean/go-libvirt**  273eaa321819 -> 1a83157e1858
* **github.com/planetscale/vtprotobuf**   ba97887b0a25 -> 8ae5a48058df
* **github.com/siderolabs/omni/client**   582730ce940c -> b1341200b16d
* **go.yaml.in/yaml/v3**                  v3.0.4 -> v3.0.5
* **google.golang.org/protobuf**          f2248ac996af -> v1.36.12
* **libvirt.org/go/libvirtxml**           v1.12002.0 -> v1.12005.0

Previous release can be found at [v0.2.0](https://github.com/siderolabs/omni-infra-provider-libvirt/releases/tag/v0.2.0)

## [Omni Infra Provider libvirt 0.2.0](https://github.com/siderolabs/omni-infra-provider-libvirt/releases/tag/v0.2.0) (2026-07-23)

Welcome to the v0.2.0 release of Omni Infra Provider libvirt!



Please try out the release binaries and report any issues at
https://github.com/siderolabs/omni-infra-provider-libvirt/issues.

### Add Versioning Support

The infra provider will now report its version to Omni.


### Contributors

* Fritz Schaal
* Artem Chernyshev
* Edward Sammut Alessi

### Changes
<details><summary>4 commits</summary>
<p>

* [`746dd25`](https://github.com/siderolabs/omni-infra-provider-libvirt/commit/746dd25b4ce6ba517fe29e59500bbee425b4ee1d) feat: rekres and add versioning
* [`820f097`](https://github.com/siderolabs/omni-infra-provider-libvirt/commit/820f0974c3231353e3c1ef3e4713cc989348db24) chore: rekres
* [`3b90832`](https://github.com/siderolabs/omni-infra-provider-libvirt/commit/3b90832552c68f85a3fb1786a293cab5739046ce) chore: rekres, update dependencies
* [`f98ad32`](https://github.com/siderolabs/omni-infra-provider-libvirt/commit/f98ad329579e89b6cfd447509a222240daaec198) chore: bump deps
</p>
</details>

### Dependency Changes

* **github.com/cosi-project/runtime**    v1.14.0 -> v1.16.2
* **github.com/siderolabs/omni/client**  v1.5.8 -> 582730ce940c
* **go.uber.org/zap**                    v1.27.1 -> v1.28.0
* **go.yaml.in/yaml/v3**                 v3.0.4 **_new_**
* **golang.org/x/sync**                  v0.19.0 -> v0.22.0
* **libvirt.org/go/libvirtxml**          v1.12001.0 -> v1.12002.0

Previous release can be found at [v0.1.0](https://github.com/siderolabs/omni-infra-provider-libvirt/releases/tag/v0.1.0)

## [omni-infra-provider-libvirt 0.1.0](https://github.com/siderolabs/omni-infra-provider-libvirt/releases/tag/v0.1.0) (2026-03-13)

Welcome to the v0.1.0 release of omni-infra-provider-libvirt!



Please try out the release binaries and report any issues at
https://github.com/siderolabs/omni-infra-provider-libvirt/issues.

### Contributors

* Fritz Schaal

### Changes
<details><summary>9 commits</summary>
<p>

* [`0ee2387`](https://github.com/siderolabs/omni-infra-provider-libvirt/commit/0ee238734a0edfb834d30788bfe79d648fcb07eb) docs: add kubernetes deployment examples
* [`908ed84`](https://github.com/siderolabs/omni-infra-provider-libvirt/commit/908ed846ff9aea0210abdfc0ec4d7d6f102728e2) chore: rekres, update dependencies
* [`0671963`](https://github.com/siderolabs/omni-infra-provider-libvirt/commit/0671963fcb0a6ddc9c54bf9eef7e4ba6ed544e4a) chore: rekres, update dependencies
* [`628fcd5`](https://github.com/siderolabs/omni-infra-provider-libvirt/commit/628fcd56c73d384b1f8e9c1941918b73dbf2a481) fix: set hostname via nocloud
* [`3b5ccfe`](https://github.com/siderolabs/omni-infra-provider-libvirt/commit/3b5ccfee0714f2acc5558c3d99796325f41a3577) feat: proper image caching
* [`9da12fc`](https://github.com/siderolabs/omni-infra-provider-libvirt/commit/9da12fc5b0f474b5727a2e5948bc15dde23a808e) chore: update dependencies, rekres, minor improvements
* [`06c1bab`](https://github.com/siderolabs/omni-infra-provider-libvirt/commit/06c1bab38f8581837f6696c25effb72879e27973) feat: support multiple network interfaces
* [`e0852f6`](https://github.com/siderolabs/omni-infra-provider-libvirt/commit/e0852f64b23872099a8e63d5319445273f531d11) feat: support additional disks
* [`e33e2e3`](https://github.com/siderolabs/omni-infra-provider-libvirt/commit/e33e2e35e1244ca18f22b1670e7bbf331f9a8526) chore: disable codecov
</p>
</details>

### Dependency Changes

* **github.com/cosi-project/runtime**     v1.11.0 -> v1.14.0
* **github.com/digitalocean/go-libvirt**  3d9fc6d90050 -> 273eaa321819
* **github.com/kdomanski/iso9660**        v0.4.0 **_new_**
* **github.com/planetscale/vtprotobuf**   79df5c4772f2 -> ba97887b0a25
* **github.com/siderolabs/omni/client**   3f2021b05f62 -> v1.5.8
* **github.com/spf13/cobra**              v1.10.1 -> v1.10.2
* **go.uber.org/zap**                     v1.27.0 -> v1.27.1
* **golang.org/x/sync**                   v0.19.0 **_new_**
* **google.golang.org/protobuf**          v1.36.10 -> f2248ac996af
* **libvirt.org/go/libvirtxml**           v1.11008.0 -> v1.12001.0

Previous release can be found at [v0.1.0-alpha.1](https://github.com/siderolabs/omni-infra-provider-libvirt/releases/tag/v0.1.0-alpha.1)

## [omni-infra-provider-libvirt 0.1.0-alpha.1](https://github.com/siderolabs/omni-infra-provider-libvirt/releases/tag/v0.1.0-alpha.1) (2025-11-18)

Welcome to the v0.1.0-alpha.1 release of omni-infra-provider-libvirt!



Please try out the release binaries and report any issues at
https://github.com/siderolabs/omni-infra-provider-libvirt/issues.

### Contributors

* Andrew Longwill
* Fritz Schaal
* fsgh42

### Changes
<details><summary>3 commits</summary>
<p>

* [`7dc899d`](https://github.com/siderolabs/omni-infra-provider-libvirt/commit/7dc899dcef8fd1f6087981e2e0e8c71fff47f4c2) chore: rekres repo
* [`2fbd816`](https://github.com/siderolabs/omni-infra-provider-libvirt/commit/2fbd8166ea9e3dea8a6ac57e773741da760a3b51) chore: add support for darwin
* [`b6583a4`](https://github.com/siderolabs/omni-infra-provider-libvirt/commit/b6583a4aeda4aacfee6a013ddd325acc4d22a35a) docs: add documentation on how to use libvirt socket
</p>
</details>

### Dependency Changes

This release has no dependency changes

Previous release can be found at [v0.1.0-alpha.0](https://github.com/siderolabs/omni-infra-provider-libvirt/releases/tag/v0.1.0-alpha.0)

## [ 0.1.0-alpha.0](https://github.com///releases/tag/v0.1.0-alpha.0) (2025-10-31)

Welcome to the v0.1.0-alpha.0 release of !



Please try out the release binaries and report any issues at
https://github.com///issues.

### Contributors

* Fritz Schaal
* Spencer Smith

### Changes
<details><summary>3 commits</summary>
<p>

* [`bfd17d0`](https://github.com///commit/bfd17d0a24365bcd6824bd1926068002f61e458e) chore: kresify this repo
* [`0104de0`](https://github.com///commit/0104de0b6cb0d5dbcedc6ab032bfffa422f71a0a) chore: fixes from review
* [`015f22a`](https://github.com///commit/015f22a07967aa9e4d7789edd38a010a9468ae9b) initial
</p>
</details>

### Dependency Changes

This release has no dependency changes


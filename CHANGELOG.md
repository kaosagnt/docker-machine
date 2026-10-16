# Changelog

GitLab's fork of Docker Machine. Each `## v<version>` section is a release, newest first. A merge that adds a new one at the top, with `version/version.go` set to match, releases it: see [Releases](README.md#releases). The releases and their downloads are at https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/releases.

## v0.16.2-gitlab.62

- Update Go to 1.27.2, from 1.26.5. This fixes 21 known vulnerabilities, including GO-2026-5026 (CVE-2026-39821), GO-2026-5942 (CVE-2026-46600), GO-2026-5972 (CVE-2026-33818), GO-2026-6088 (CVE-2026-56859), GO-2026-6089 (CVE-2026-56853) and GO-2026-6090 (CVE-2026-56862). With Go 1.27, the macOS binary needs macOS 13 Ventura or later. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/206.

## v0.16.2-gitlab.61

- Fix the google driver reading the network project only from a full API URL in `--google-network`. The relative form `projects/<project>/global/networks/<name>` and network names that contain digits now match too, so a bare subnetwork name resolves in the network's project (for example, a shared VPC) instead of the VM project. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/203.

## v0.16.2-gitlab.60

- Allow `--google-flex-selection` entries to set `region`, `zone` (repeatable, `zone[:ALLOW|DENY]`) and `subnetwork`. Entries that leave them out inherit `--google-region`, `--google-location-zone` and `--google-subnetwork`, so existing configs are unchanged. The bulkInsert call for an entry goes to its region, each cross-region entry has its own stockout cooldown, and `region` is added to the bulkInsert attempt, stockout and placed log fields. Zones outside their region and a full-path `subnetwork=` in another region are rejected at config time. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/202.
- Derive the google driver's region from the instance's zone once the zone is known, and use `--google-region` only before placement. Without `--google-bulk-insert` this also changes the region used for the subnetwork path and the static address lookup. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/202.

## v0.16.2-gitlab.59

- Add `MACHINE_DRIVER_INPROCESS`. Set to a true value, it runs built-in drivers in the docker-machine process instead of in a plugin subprocess reached over RPC. It is off by default, and drivers that are not compiled in always use the plugin path. In-process, a panic in a driver method that returns an error is returned as that error, the driver's own log lines no longer get the machine name prefix or `machine` field, and virtualbox and vmwarefusion print their command output only when the client runs with `MACHINE_DEBUG` set. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/199.

## v0.16.2-gitlab.58

- Add `DOCKER_MACHINE_SSH_READINESS_TIMEOUT`, an opt-in Go duration of at least one minute that bounds the whole SSH readiness wait. When it passes, the running probe is cancelled (the external `ssh` process is killed and the native client's connection is closed), no further probe starts, and `create` fails with `SSH readiness deadline exceeded`. Unset, empty or invalid values (logged with a warning) mean no deadline and the previous probe schedule. The wait now uses github.com/cenkalti/backoff/v4 v4.3.0. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/197.
- Change the native SSH client to dial once per command instead of running its own 60-attempt dial loop. Readiness probes are retried by the SSH wait, and provisioning commands by the existing three-attempt retry. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/197.

## v0.16.2-gitlab.57

- Add `--google-cos-tls-via-metadata` (`GOOGLE_COS_TLS_VIA_METADATA`) to the google driver. The Docker server certificate and dockerd drop-in are generated before the VM exists and sent with the CA and SSH key as instance metadata, which the image has to install itself (`docs/drivers/gce.md` lists the keys). docker-machine does not SSH into the machine during `create` and waits for a TLS handshake on port 2376 instead. Machines created this way store a `ServerName` in `config.json` that `ls`, `config` and `env` verify against, `start` refuses to re-create them from their disk when the instance is gone, and the COS readiness gate and readiness URL are not checked for them. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/196.
- Add the machine name to the SANs of the Docker server certificate that docker-machine issues, for all machines. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/196.
- Attach the SSH key in the google instance insert request instead of with a separate `SetMetadata` call after create. `--google-use-existing` still uses `SetMetadata`. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/196.

## v0.16.2-gitlab.56

- Fix the google COS cloud-init wait failing every create on stock COS images with cloud-init 24.1 or later, where `cloud-init status --wait` exits 1 within a second. The wait now runs `systemctl start cloud-init.target` and then `cloud-init status` once: exit 0 and 2 pass, and exit 1 (errors recorded by cloud-init) or a 5-minute timeout fail the create. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/195.
- Rename `--google-cos-docker-network-readiness-gate` to `--google-cos-wait-for-cloud-init` (`GOOGLE_COS_WAIT_FOR_CLOUD_INIT`). The old flag still works and logs a deprecation warning. The instance metadata key becomes `gitlab-wait-for-cloud-init`. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/195.
- Add `--google-no-service-account` (`GOOGLE_NO_SERVICE_ACCOUNT`) to the google driver. Instances are created with no service account on both the `Instances.Insert` and bulkInsert paths, so the metadata server has no access token for them. The driver warns that it ignores `--google-service-account` and `--google-scopes` when they are set to non-default values. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/194.

## v0.16.2-gitlab.55

- Add a JSON log format, selected with `--log-format=json` or `MACHINE_LOG_FORMAT=json`. Each line is one object with time, level, message and any fields. Text stays the default and appends fields as `key=value`. Driver plugin processes get the format through `MACHINE_PLUGIN_LOG_FORMAT`, and in JSON mode their lines are relayed with their fields plus a `machine` field. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/193.
- Add a `phase` field to the first log line of each `create` phase (certificate bootstrap, pre-create checks, driver create, waiting for the machine to run, waiting for SSH, OS detection, provisioning and the Docker connection check), and log the variable parts of create-path messages (provisioner, region, machine type, zone, bulkInsert operation, attempt counters) as fields instead of in the message text. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/193.

## v0.16.2-gitlab.54

- Add `docker-machine update-labels --label key=value <machine>`, backed by a new optional `LabelUpdater` driver interface. The google driver merges the given labels into the instance's current labels. Drivers that don't implement the interface report label updates as unsupported. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/190.
- Split the google COS readiness gate in two. `--google-cos-docker-network-readiness-gate` now only waits for cloud-init, and the container egress probe runs only when the new `--google-cos-docker-network-readiness-url` (`GOOGLE_COS_DOCKER_NETWORK_READINESS_URL`) is set, fetching that URL instead of the metadata server on port 80. Any HTTP response passes, and the URL must be http or https with a host and no shell metacharacters. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/191.
- Fix `stop`, `start` and instance lookups failing with `Invalid value for field 'zone': ''` on google bulkInsert machines whose create never recorded a zone. They now find the zone with an AggregatedList call and save it, or return not-found when the instance was never placed. Bulk-mode creates skip the pre-create existence check when no zone is known yet. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/187.
- Return google API lookup failures other than not-found from the machine state check and from zone recovery (including on delete), instead of treating the machine as gone. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/187.

## v0.16.2-gitlab.53

- Add `--google-flex-stockout-cooldown` (`GOOGLE_FLEX_STOCKOUT_COOLDOWN`, default `0s`, which disables it) and `--google-flex-stockout-probe-lease` (`GOOGLE_FLEX_STOCKOUT_PROBE_LEASE`, default `5m`) to the google driver's bulkInsert mode. With a cooldown set, a `--google-flex-selection` entry that fails with a recognised stockout error is tried after the other entries by later creates until the cooldown expires. After that, one process at a time may probe it at its configured position, a successful placement clears the cooldown, and another stockout starts it again. Creates on the same host share this state through `google-flex-stockout-health.json` in the Docker Machine store directory, and missing, corrupt or locked state falls back to the configured order. Both flags require `--google-bulk-insert`, and with a cooldown set the probe lease must be at least `--google-operation-backoff-max-elapsed-time`. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/188.
- Update Go to 1.26.5, from 1.26.3. This fixes CVE-2026-27145, CVE-2026-39822 and CVE-2026-42504. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/186.

## v0.16.2-gitlab.52

- Add `--google-cos-docker-network-readiness-gate` (`GOOGLE_COS_DOCKER_NETWORK_READINESS_GATE`) to the google driver, which sets the instance metadata key `gitlab-docker-network-readiness-gate=true`. When that key is `true`, the Container-Optimized OS provisioner waits up to five minutes for cloud-init to finish before it provisions Docker. After Docker is set up, it runs an `alpine:latest` container on the bridge network to fetch from the GCE metadata server, without pulling the image, so the image must already be on the host. If the check fails, it logs diagnostics, restarts Docker once and checks again. If it still fails, it stops Docker and the create fails. The provisioner reads the metadata key on every COS create and fails the create if the metadata server returns anything other than 200 or 404 after three attempts. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/183.
- Update golang.org/x/crypto to v0.52.0 and golang.org/x/net to v0.55.0, with golang.org/x/sys v0.45.0, golang.org/x/term v0.43.0 and golang.org/x/text v0.37.0. This fixes CVE-2026-39827, CVE-2026-39828, CVE-2026-39829, CVE-2026-39830, CVE-2026-39832, CVE-2026-39835, CVE-2026-42508, CVE-2026-46595 and CVE-2026-46597 in golang.org/x/crypto, and CVE-2026-25680, CVE-2026-25681, CVE-2026-27136, CVE-2026-33814, CVE-2026-39821, CVE-2026-42502 and CVE-2026-42506 in golang.org/x/net. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/182.

## v0.16.2-gitlab.51

- Fix bulkInsert creates leaving VMs running when the follow-up `Instances.AggregatedList` zone lookup fails, for example with an HTTP 403 quota error. The google driver now reads the placed zone from the bulkInsert operation's per-location status and uses `AggregatedList` only when the operation doesn't report it. When a create fails after the VM was placed, the zone is still saved so removing the machine can delete the VM. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/181.
- Fix bulkInsert mode failing the create when the `BulkInsert` call itself is rejected for capacity, instead of trying the next `--google-flex-selection`. An API error with HTTP status 503 or 429 and the reason `insufficientCapacity`, `ZONE_RESOURCE_POOL_EXHAUSTED`, `ZONE_RESOURCE_POOL_EXHAUSTED_WITH_DETAILS`, `RESOURCE_POOL_EXHAUSTED` or `RESOURCE_AVAILABILITY` now moves on to the next selection. Other errors, including quota errors, still fail the create. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/180.

## v0.16.2-gitlab.50

- Retry provisioning SSH commands when the SSH transport fails: the external `ssh` exits with status 255, or the native client loses the session without an exit status or fails to connect. Each command is tried up to three times in total, three seconds apart, and `DOCKER_MACHINE_SSH_COMMAND_MAX_ATTEMPTS` overrides the count. Commands that exit with a non-zero status are not retried, and neither are `sudo reboot`, the RancherOS upgrade or the SSH availability check. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/179.
- Change the external SSH client options to `ServerAliveInterval=15` (was 60), `ServerAliveCountMax=3` and `TCPKeepAlive=yes`, so a stalled session is detected after about 45 seconds. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/179.
- Fix the native SSH client's `Output` and `OutputWithPty` returning no error when the SSH session could not be opened. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/179.

## v0.16.2-gitlab.49

- Fix bulkInsert mode only ever trying the first `--google-flex-selection` entry. The google driver now sends one `BulkInsert` request per entry, in the order given, and moves to the next entry on `VM_MIN_COUNT_NOT_REACHED`, `ZONE_RESOURCE_POOL_EXHAUSTED` or `ZONE_RESOURCE_POOL_EXHAUSTED_WITH_DETAILS`. Any other error fails the create, and if every entry fails, the error lists each failure. Requests no longer use `InstanceFlexibilityPolicy` and carry the entry's machine type and disk directly. A create can now make up to one `BulkInsert` call per entry. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/178.
- Allow `--google-bulk-insert` without `--google-flex-selection`. The google driver then builds a single selection from `--google-machine-type`, `--google-disk-type` and the `--google-provisioned-*` flags. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/178.
- Always set `TargetShape=ANY` on the bulkInsert location policy, so GCP can place the VM in any allowed zone instead of using its `ANY_SINGLE_ZONE` default. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/178.
- Treat `--google-location-zone` preferences other than `ALLOW` and `DENY`, including `PREFERRED`, as `ALLOW` and log a warning. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/178.
- Log the bulkInsert request as JSON at debug level. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/178.

## v0.16.2-gitlab.48

- Fix removing a bulkInsert machine whose create failed before its zone was recorded, which failed every time with "zone unresolved". The google driver now looks up the instance's zone with `AggregatedList` and deletes it, or returns a not-found (HTTP 404) error when no such instance exists. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/175.

## v0.16.2-gitlab.47

- Add an experimental bulkInsert mode to the google driver, enabled with `--google-bulk-insert` (`GOOGLE_BULK_INSERT`) and `--google-region` (`GOOGLE_REGION`). The VM is created with `RegionInstances.BulkInsert` in the region, the repeatable `--google-location-zone=zone[:PREFERENCE]` flag constrains the zones, and the zone and machine type GCP picked are saved as `ResolvedZone` and `ResolvedMachineType` in the driver state and used by later operations. The mode can't be combined with `--google-zone`, `--google-address` or `--google-use-existing`, and `start` can't re-create a missing instance from its disk. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/169.
- Add the repeatable `--google-flex-selection` flag (`GOOGLE_FLEX_SELECTION`) for bulkInsert mode, which requires at least one entry. Each entry is `machine-type=<type>` with optional `disk-type`, `disk-iops` and `disk-throughput` keys, listed in preference order and sent as `InstanceFlexibilityPolicy` instance selections, where the disk keys override the boot disk for that selection. It replaced the `--google-flex-machine-type` flag that was added after v0.16.2-gitlab.46, so that flag is in no release. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/173.
- Update google.golang.org/api to v0.279.0, from v0.169.0, with golang.org/x/crypto v0.50.0, golang.org/x/net v0.53.0, golang.org/x/oauth2 v0.36.0 and golang.org/x/sys v0.43.0. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/169.
- Update Go to 1.26.3, from 1.25.8, which fixes several Go standard library vulnerabilities (the MR doesn't list them). Log calls with mismatched format arguments in the azure and openstack drivers, the RPC driver client and the crash reporter are fixed too. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/170.

## v0.16.2-gitlab.46

- Add `--google-provisioned-iops` (`GOOGLE_PROVISIONED_IOPS`) and `--google-provisioned-throughput` (`GOOGLE_PROVISIONED_THROUGHPUT`, in MiB/s) to the google driver. They set the provisioned IOPS and throughput of the boot disk it creates, for Hyperdisk disk types. The default `0` leaves the GCE defaults, and negative values are rejected. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/166.

## v0.16.2-gitlab.45

- Add `--tls-bootstrap-lock` (`MACHINE_TLS_BOOTSTRAP_LOCK`) to `docker-machine create`. When it's set, CA and client certificate generation holds an exclusive lock on `.bootstrap.lock` in the cert directory, waiting up to 15 seconds for it. Concurrent creates on a host with an empty cert directory then no longer each write their own mismatched certificates. It is off by default. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/165.

## v0.16.2-gitlab.44

- Update Go from 1.24.11 to 1.25.8, and raise the Go version in go.mod to 1.25.8. This picks up the Go fix for CVE-2025-68121 (crypto/tls), which was reported in the docker-machine binary built with Go 1.24.11. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/160.

## v0.16.2-gitlab.43

- Update github.com/docker/docker from v25.0.6 to v28.5.2 to fix CVE-2025-54410, along with updated indirect dependencies (including google.golang.org/grpc v1.67.0), and move the Docker client calls in libmachine to the relocated image.PullOptions and container.StartOptions types. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/159.
- Update Go to 1.24.11 and golang.org/x/crypto to v0.46.0, along with golang.org/x/net v0.47.0, golang.org/x/sys v0.39.0, golang.org/x/term v0.38.0, golang.org/x/text v0.32.0, golang.org/x/sync v0.19.0, golang.org/x/mod v0.30.0 and golang.org/x/tools v0.39.0, to address CVE-2025-58183, CVE-2025-47914 and CVE-2025-58181. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/158.

## v0.16.2-gitlab.42

- Fix the Google driver failing with "requesting firewall rule" when the docker-machines firewall rule does not exist yet. A 404 from the rule lookup is no longer treated as an error, so the rule is created. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/155.
- Update Go to 1.24.6 and golang.org/x/oauth2 from v0.18.0 to v0.31.0 to fix security issues in the older golang.org/x/oauth2. This also drops the indirect cloud.google.com/go/compute and google.golang.org/appengine dependencies and updates cloud.google.com/go/compute/metadata to v0.3.0. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/154.

## v0.16.2-gitlab.41

- Change the Google driver's default image from Ubuntu 20.04 (ubuntu-2004-focal-v20240229) to Ubuntu 22.04 (ubuntu-2204-jammy-v20250815). See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/153.

## v0.16.2-gitlab.40

- Change the Amazon EC2 driver's default AMIs from Ubuntu 20.04 LTS to Ubuntu 22.04 LTS (20250712 images) in every region except us-gov-east-1 and us-gov-west-1, whose defaults are unchanged. The AWS driver documentation now points to drivers/amazonec2/region.go instead of listing the AMIs. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/152.

## v0.16.2-gitlab.39

- Update the Amazon EC2 driver's default AMIs to Ubuntu 20.04 LTS 20250624 images in every region except us-gov-east-1 and us-gov-west-1, and add default AMIs for the ap-east-2, ap-southeast-5, ap-southeast-7 and mx-central-1 regions. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/151.

## v0.16.2-gitlab.38

- Update Go to 1.24.4 to fix CVE-2025-4673 and other vulnerabilities. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/150.

## v0.16.2-gitlab.37

- Update github.com/aws/aws-sdk-go from v1.36.21 to v1.55.7. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/149.

## v0.16.2-gitlab.36

- Update golang.org/x/net from v0.37.0 to v0.40.0 to fix CVE-2025-22872, along with golang.org/x/crypto v0.38.0, golang.org/x/sys v0.33.0, golang.org/x/term v0.32.0, golang.org/x/text v0.25.0 and golang.org/x/sync v0.14.0. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/147.
- Update Go from 1.24.1 to 1.24.3, and raise the Go version in go.mod from 1.23.0 to 1.24.3. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/148.

## v0.16.2-gitlab.35

- Update github.com/golang-jwt/jwt/v4, which replaces github.com/dgrijalva/jwt-go, from v4.5.1 to v4.5.2 to fix CVE-2025-30204 (GHSA-mh63-6h87-95cp). See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/146.

## v0.16.2-gitlab.34

- Fix the Amazon EC2 driver's default AWS credentials lookup, which the MR reports failed with "amazonec2 driver requires AWS credentials configured ..." when using IAM roles through EKS Pod Identity. The driver now creates its AWS session with session.NewSession instead of the deprecated session.New, and exits with an error if the session can't be created. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/142.
- Update golang.org/x/net from v0.33.0 to v0.37.0 to fix CVE-2025-22870, along with golang.org/x/crypto v0.36.0, golang.org/x/sys v0.31.0, golang.org/x/term v0.30.0, golang.org/x/text v0.23.0 and golang.org/x/sync v0.12.0, and raise the Go version in go.mod from 1.22 to 1.23.0. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/144.
- Update Go from 1.23.6 to 1.24.1. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/145.

## v0.16.2-gitlab.33

- Update Go from 1.23.5 to 1.23.6 to fix CVE-2025-22866 (GO-2025-3447). See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/143.

## v0.16.2-gitlab.32

- Update Go from 1.23.2 to 1.23.5 to fix CVE-2024-45336 and CVE-2024-45341. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/141.

## v0.16.2-gitlab.31

- Update golang.org/x/crypto from v0.24.0 to v0.31.0 to fix CVE-2024-45337 and golang.org/x/net from v0.26.0 to v0.33.0 to fix CVE-2024-45338, along with golang.org/x/sys v0.28.0, golang.org/x/term v0.27.0, golang.org/x/text v0.21.0 and golang.org/x/sync v0.10.0. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/140.

## v0.16.2-gitlab.30

- Update Go from 1.22.7 to 1.23.2, and update github.com/golang-jwt/jwt/v4, which replaces github.com/dgrijalva/jwt-go, from v4.0.0 to v4.5.1 to address CVE-2024-51744. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/139.

## v0.16.2-gitlab.29

- Update Go from 1.22.5 to 1.22.7 to fix CVE-2024-34155, CVE-2024-34156 and CVE-2024-34158. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/136.

## v0.16.2-gitlab.28

- Replace github.com/dgrijalva/jwt-go with github.com/golang-jwt/jwt/v4 v4.0.0 through a go.mod replace directive to address CVE-2020-26160, and update github.com/docker/docker from v24.0.9 to v25.0.6 and google.golang.org/grpc from v1.60.1 to v1.64.1 to address GHSA-xr7q-jx4m-x55m and GHSA-v23v-6jw2-98fq. This also updates google.golang.org/api to v0.169.0 and golang.org/x/oauth2 to v0.18.0. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/134.

## v0.16.2-gitlab.27

- Fix VirtualBox version detection when `VBoxManage --version` prints a warning before the version number, as reported on Debian 12 with VirtualBox 7. The version is now read from the start of any output line, and output with no version gives a "Failed to parse Virtualbox version" error. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/129.
- Update Go from 1.21.8 to 1.22.5 and raise the Go version in go.mod from 1.20 to 1.22, addressing CVE-2024-24790. This also updates github.com/docker/docker to v24.0.9, golang.org/x/net to v0.26.0, golang.org/x/crypto to v0.24.0, golang.org/x/sys to v0.21.0, golang.org/x/term to v0.21.0, golang.org/x/text to v0.16.0 and google.golang.org/protobuf to v1.34.2. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/131.

## v0.16.2-gitlab.26

- Change the Google driver's default image from Ubuntu 16.04 (ubuntu-1604-xenial-v20210928) to Ubuntu 20.04 (ubuntu-2004-focal-v20240229). The MR reports that Docker could no longer be installed on new VMs with the Ubuntu 16.04 image. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/125.
- Add the `--amazonec2-volume-iops` (`AWS_VOLUME_IOPS`) and `--amazonec2-volume-throughput` (`AWS_VOLUME_THROUGHPUT`) options to the Amazon EC2 driver. IOPS is passed for io1 and io2 root volumes; for gp3 root volumes IOPS and throughput are passed with minimums of 3000 and 125. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/128.
- Add support for GCP Shared VPC to the Google driver. When `--google-network` is a full network URL (`https://www.googleapis.com/compute/v1/projects/<project>/global/networks/<name>`), the project in that URL is used to look up, create and update the firewall rule and to build the subnetwork path from a `--google-subnetwork` name. Without a full URL the driver's own project is used as before. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/103.

## v0.16.2-gitlab.25

- Update the Amazon EC2 driver's default AMIs to Ubuntu 20.04 LTS 20240205 images, and add default AMIs for the ap-southeast-4, ca-west-1 and il-central-1 regions. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/120.
- Keep the default AMIs for us-gov-east-1 and us-gov-west-1, which the AMI update removed, at their previous AMI IDs. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/124.
- Update Go from 1.21.3 to 1.21.8. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/123.
- Update golang.org/x/crypto from v0.16.0 to v0.21.0, golang.org/x/net from v0.19.0 to v0.21.0, golang.org/x/sys to v0.18.0 and golang.org/x/term to v0.18.0 to fix CVE-2023-39325, CVE-2023-48795, CVE-2023-3978 and CVE-2023-44487. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/122.

## v0.16.2-gitlab.24

- Add `--amazonec2-credit-specification` to set the CPU credit option (`standard` or `unlimited`) of burstable Amazon EC2 instance types. When it is not set, the driver sends no credit specification and the instance type's AWS default applies. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/118.
- Update google.golang.org/grpc from v1.36.0 to v1.60.1, google.golang.org/api from v0.42.0 to v0.149.0, golang.org/x/crypto from v0.14.0 to v0.16.0, golang.org/x/net from v0.17.0 to v0.19.0, golang.org/x/oauth2 to v0.13.0 and golang.org/x/sys from v0.13.0 to v0.15.0, along with the indirect dependencies they pull in. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/116.
- Update github.com/dgrijalva/jwt-go from a 2016 v3.0.1 pseudo-version to v3.2.0. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/117.
- The binaries report version 0.16.2-gitlab.23, because `version/version.go` wasn't updated for this tag. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/commit/38385069d7f23410d434dc35a3c209b7a8beb0c3.

## v0.16.2-gitlab.23

- Update Go from 1.12.9 to 1.21.3 in the image that builds the release binaries. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/112.
- Manage dependencies with Go modules (go.mod and go.sum) instead of godep, and remove the vendor directory, Gopkg.toml and Gopkg.lock. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/112.
- Update golang.org/x/net to v0.17.0 and golang.org/x/crypto to v0.14.0 to fix GO-2023-1571, GO-2022-0968 and GO-2023-2102. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/112.
- Replace github.com/samalba/dockerclient with the official Docker client, github.com/docker/docker v24.0.5 with API version negotiation, for creating the Swarm containers and querying the Docker version on a machine, and replace github.com/docker/docker/pkg/term with golang.org/x/term in the native SSH client's interactive shell. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/112.

## v0.16.2-gitlab.22

- Tag Amazon EC2 spot instances with the `Name` tag and the `--amazonec2-tags` tags by calling CreateTags on the instance after it launches, at most 10 tags per call, instead of tagging only the spot instance request. On-demand instances are still tagged in the launch request. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/110.

## v0.16.2-gitlab.21

- Add `--amazonec2-volume-encrypted` to encrypt the instance's EBS volume, and `--amazonec2-volume-kms-key` (`AWS_VOLUME_KMS_KEY`) to set the KMS key ID, ARN or alias used to encrypt it. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/98.
- Launch Amazon EC2 spot instances with RunInstances and spot market options instead of a separate spot instance request, so `--amazonec2-metadata-token` and `--amazonec2-metadata-token-response-hop-limit` (for example, requiring IMDSv2) now apply to spot instances too. The driver no longer waits for a spot request to be fulfilled or cancels it on removal. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/100.

## v0.16.2-gitlab.20

- Fix Amazon EC2 spot instance creation failing with "'instance' is not a valid taggable resource type for this operation". Spot instance requests now tag only the spot instance request, while on-demand instances still tag the instance, its volumes and its network interfaces. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/106 and https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/107.

## v0.16.2-gitlab.19

- Fix provisioning with Docker 23 and later, which no longer creates `/etc/docker` when installed and so made writing the certificates fail with "tee: /etc/docker/ca.pem: No such file or directory". The Arch, Debian, Google COS, RancherOS, SUSE and Ubuntu (systemd) provisioners now create the directory first, as the others already did. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/102.
- Restart the Docker service after enabling it in the Ubuntu (systemd) provisioner. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/81.
- Add the AWS regions ap-south-2, eu-central-2, eu-south-2 and me-central-1, with their default AMIs, to the Amazon EC2 driver. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/99.
- Change the Amazon EC2 driver's `defaultAmiId`, the AMI that `NewDriver` sets before flags are applied, from ami-c60b90d1 to ami-08d4ac5b634553e16, which the MR identifies as the Ubuntu 20.04 image ubuntu-focal-20.04-amd64-server-20220610. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/90.
- Fix the release script step that copies the newest stable tag's binaries to the S3 `latest` directory. The old check tested the empty output of `git describe` instead of its exit status. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/92.

## v0.16.2-gitlab.18

- No changes to docker-machine itself. Upload beta, RC and stable release binaries to S3 under the ref name again (for example `v0.16.2-gitlab.18`) instead of the ref slug used for v0.16.2-gitlab.17. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/commit/bde78082ca9babf935921421e391658db345a4c6.

## v0.16.2-gitlab.17

- No changes to docker-machine itself. Upload beta, RC and stable release binaries to S3 under the ref slug (`CI_COMMIT_REF_SLUG`) instead of `mr-${CI_MERGE_REQUEST_IID}`, and keep the `mr-<IID>` path for merge request development builds only. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/89.

## v0.16.2-gitlab.16

- Tag Amazon EC2 instances, their volumes and their network interfaces with the `Name` tag and the `--amazonec2-tags` tags in the launch request (EC2 TagSpecifications), for both on-demand and spot instance requests, instead of tagging the instance and its EBS volume with a separate CreateTags call after launch. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/87.
- Apply `--google-label` labels to the disk that the Google driver creates for the VM, as well as to the VM. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/86.
- Set the Google driver's SSH key under both the `ssh-keys` and the legacy `sshKeys` instance metadata keys, change the default `--google-username` from `docker-user` to `ubuntu`, and change the default `--google-machine-image` to ubuntu-1604-xenial-v20210928. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/72.
- Read the `CPE_NAME` field of `/etc/os-release`, which Amazon Linux 2 sets, instead of logging "Couldn't set key CPE_NAME, no corresponding struct field found". See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/82.
- Upload merge request development builds to S3 under `mr-<MR IID>` instead of the branch name. This release also set the same `mr-${CI_MERGE_REQUEST_IID}` path for the beta, RC and stable S3 release jobs, which v0.16.2-gitlab.17 and v0.16.2-gitlab.18 change back. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/84.

## v0.16.2-gitlab.15

- Create the GitLab Release entry for stable and RC tags with release-cli. The entry now links the macOS amd64, Linux amd64, Linux arm, Linux arm64, OpenBSD amd64 and Windows amd64 binaries, the `release.sha256` checksums file and its GPG signature, and the S3 index page. It previously linked only the Linux amd64, macOS amd64 and Windows amd64 binaries and the index page. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/83.
- Upload the OpenBSD amd64 binary to S3 with the other release binaries. The S3 release jobs previously did not take the OpenBSD build job's artifacts. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/83.

## v0.16.2-gitlab.14

- Update the Amazon EC2 driver's default AMIs from Ubuntu 16.04 LTS (20180228.1) to Ubuntu 20.04 LTS (20220420) images in every listed region, and add default AMIs for the ap-northeast-3, ap-southeast-3, eu-south-1, me-south-1 and us-gov-east-1 regions. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/80.
- Add the af-south-1 region, with a default AMI, to the Amazon EC2 driver. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/70.
- Restart the Docker service, instead of starting it, after writing the Docker daemon configuration and TLS certificates during provisioning. The MR reports that provisioning failed on Ubuntu 18.04 and later because systemd did not pick up the added drop-in files for the docker unit. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/62.
- Add an Amazon Linux 2 provisioner, based on the Red Hat provisioner, that is used for hosts whose os-release ID is `amzn`. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/65.
- Fix the DigitalOcean driver waiting nanoseconds instead of seconds between its checks for a new droplet's public IP address. The MR reports that the driver could not create machines because of this. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/61.
- Add documentation for the Amazon EC2 driver at docs/drivers/aws.md. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/76.
- Add documentation for the Google Compute Engine driver at docs/drivers/gce.md, and replace docs/README.md, which pointed to Docker's documentation repository, with a description of this fork. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/75.
- Add installation instructions at docs/install-machine.md that download the binary from this fork's releases, and link the README's documentation section to them instead of Docker's documentation. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/63.

## v0.16.2-gitlab.13

- Add the `--google-skip-firewall-create` (`GOOGLE_SKIP_FIREWALL_CREATE`) option to the Google driver, which skips looking up, creating and updating the firewall rule that opens the Docker and swarm ports. The MR's use case is projects where firewall rules are created in advance because the account running docker-machine lacks permission to manage them. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/57.
- Publish beta builds from the `main` branch instead of `master`, so they are listed at https://gitlab-docker-machine-downloads.s3.amazonaws.com/main/index.html. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/56.

## v0.16.2-gitlab.12

- Use the Compute Engine API's Wait call instead of Get when the Google driver checks the status of zone and global operations, still looping with the existing backoff until the operation is done. The MR expects this to reduce the number of API calls made against the GCP API quota. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/55.
- Update google.golang.org/api to v0.40.0 and github.com/golang/protobuf to v1.5.2, and update golang.org/x/net, golang.org/x/oauth2 and golang.org/x/sys to newer revisions. This replaces google.golang.org/cloud with cloud.google.com/go v0.80.0 and adds google.golang.org/grpc v1.36.0, google.golang.org/protobuf v1.25.0, go.opencensus.io v0.23.0, github.com/googleapis/gax-go v2.0.5 and golang.org/x/text v0.3.5 as dependencies. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/52.
- Stop stripping the symbol table from release binaries by removing the `-s` linker flag, so profilers can resolve docker-machine's stack frames. DWARF debugging information is still left out (`-w`). See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/54.

## v0.16.2-gitlab.11

- Add the `--amazonec2-metadata-token` (default `optional`) and `--amazonec2-metadata-token-response-hop-limit` (default 1) options to the Amazon EC2 driver. They set the instance metadata options (HttpTokens and HttpPutResponseHopLimit) on instances launched with RunInstances, so setting the token to `required` makes the instance require IMDSv2. Spot instance requests do not use these options. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/49.
- Add the ap-east-1 (Hong Kong) region, with a default AMI, to the Amazon EC2 driver. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/33.

## v0.16.2-gitlab.10

- Add the `--google-accelerator` (`GOOGLE_ACCELERATOR`, format `count=N,type=TYPE`) option to the Google driver to attach GPUs to the instance, and the `--google-maintenance-policy` (`GOOGLE_MAINTENANCE_POLICY`, for example `MIGRATE` or `TERMINATE`) option to set the instance's host maintenance behavior, which GPU instances need because they can't use `MIGRATE`. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/48.
- Update github.com/aws/aws-sdk-go from v1.4.10 to v1.36.21, which also updates github.com/jmespath/go-jmespath and removes github.com/go-ini/ini. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/47.
- Generate the index.html page published with the binaries on S3 with the shared release-index-gen tool instead of a Go program in this repository, and pass it GPG key settings from the `GPG_KEY` and `GPG_PASSPHRASE` variables. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/20.

## v0.16.2-gitlab.9

- Add the `--google-metadata-from-file` option to the Google driver, in `key=path/to/file` form and repeatable, which sets an instance metadata value from the contents of a file, for example cloud-config user data. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/41.
- Add the `--google-metadata` option to the Google driver, in `key=value` form and repeatable, which sets custom metadata on the instance when it is created. The SSH key is now added to the instance's existing metadata instead of replacing it. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/42.

## v0.16.2-gitlab.8

- Build the linux/amd64 release binary with `make build-x`, like the other platforms, so it is statically linked and published as `docker-machine-Linux-x86_64` instead of a dynamically linked `docker-machine`. The release's "linux amd64" download link now points to the new file name. The MR reports that the dynamically linked binary did not work on Alpine Linux. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/44.

## v0.16.2-gitlab.7

- Add a provisioner for Google Container-Optimized OS (os-release ID `cos`). It configures the Docker daemon through a systemd drop-in, skips package installation, and opens TCP port 2376 with iptables. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/40.

## v0.16.2-gitlab.6

- Add the `--google-label` option to the Google driver, in `key:value` form and repeatable, which sets labels on the instance. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/35.

## v0.16.2-gitlab.5

- Apply the instance's tags (the `Name` tag and any `--amazonec2-tags`) to its first attached EBS volume as well as to the EC2 instance in the Amazon EC2 driver. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/29.
- Detect Flatcar Container Linux as CoreOS: the CoreOS provisioner now also matches hosts whose os-release `ID_LIKE` is `coreos`. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/24.
- Return an "unexpected AWS API response" error in the Amazon EC2 driver when the RunInstances, RequestSpotInstances, DescribeSpotInstanceRequests or DescribeInstances calls return no instances or requests, instead of reading the first element of an empty list. The MR reports that RunInstances can return an empty list while still creating an instance that docker-machine then doesn't track. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/19.

## v0.16.2-gitlab.4

- Fix the Google driver ignoring an error from looking up the existing firewall rule: `create` now fails with "requesting firewall rule: ..." instead of going on to create a rule that may already exist. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/25.

## v0.16.2-gitlab.3

- Fix the project title on the S3 release index page, which now reads "Docker Machine (GitLab's fork)" instead of "GitLab Runner - Custom Executor's autoscaling driver". See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/commit/d9a33511f26f8a898e819f3c422369614f1fe62c.

## v0.16.2-gitlab.2

- Make the amazonec2 driver return the IP address it has already stored instead of calling the EC2 DescribeInstances API every time it needs the machine's IP (for example for each SSH command). See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/14.
- Make the Google driver return the IP address it has already stored, and store the address after the first API lookup, instead of calling the Compute API every time it needs the machine's IP. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/15.
- The binaries report version 0.16.2-gitlab.1, because `version/version.go` wasn't updated for this tag. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/commit/bd58089623e13829cac6d49a6d6e1419ec635ae8.

## v0.16.2-gitlab.1

- Base the fork on upstream Docker Machine 0.16.2, using upstream master as of 2 September 2019, which builds with Go 1.12.9. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/10.
- Carry the GitLab patch that makes the DigitalOcean driver rate-limit aware: API calls that get HTTP 429 are retried after the rate limit's reset time plus a random delay, a droplet is removed again if creation fails, and the shared `WaitFor` retry helper adds up to 9 seconds of random delay between attempts. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/2.
- Carry the GitLab patch that saves the machine's configuration when `create` fails, so details such as the DigitalOcean droplet ID are kept. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/commit/5050ac2b4f8256adc07ea888f4d355287e79c45e.
- Carry the GitLab patch that adds exponential backoff to the Google driver's operation status checks, configured with the `--google-operation-backoff-*` flags. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/7.
- Carry the GitLab patch that adds the Google driver's `--google-min-cpu-platform` option (`GOOGLE_MIN_CPU_PLATFORM`), which sets the minimum CPU platform of the created VM by its friendly name, for example `Intel Skylake`. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/9.
- Publish the linux amd64, arm and arm64, darwin amd64 and windows amd64 binaries to an S3 bucket from CI, under a path named after the branch or tag and with a generated `index.html`: merge requests as development releases, master and `-rc` tags as beta releases, and stable tags as stable releases. For each tag the job also creates a GitLab release with download links. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/13.

## v0.14.0-gitlab.1

- Rebase the GitLab patches onto upstream Docker Machine 0.14.0, keeping the DigitalOcean rate limiting, saving the machine when `create` fails, and the Google operation backoff. The Google driver's 404 handling and the GitLab CI configuration were merged upstream (https://github.com/docker/machine/pull/4330 and https://github.com/docker/machine/pull/4329), so they are no longer separate patches. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/commit/89b833253d9412716a0291cbdccc94454c33d1b5.
- Build with Go 1.9 instead of upstream 0.14.0's Go 1.8.3. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/commit/1f5063691314ff146ab6141f4b33898e0e20cf43.
- Add the `--google-min-cpu-platform` option (`GOOGLE_MIN_CPU_PLATFORM`) to the Google driver, which sets the minimum CPU platform of the created VM by its friendly name, for example `Intel Skylake`. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/9.

## v0.13.0-gitlab.2

- Replace the Google driver's fixed 1-second polling of operation status with exponential backoff, configured with `--google-operation-backoff-initial-interval` (default 1 second), `--google-operation-backoff-randomization-factor` (0.5), `--google-operation-backoff-multipler` (2), `--google-operation-backoff-max-interval` (30 seconds) and `--google-operation-backoff-max-elapsed-time` (300 seconds). An operation still pending after the maximum elapsed time now fails with "maximum backoff elapsed time exceeded". See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/7.
- Build with Go 1.9 instead of Go 1.8.3. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/7.

## v0.13.0-gitlab.1

- Rebase the GitLab patches (DigitalOcean rate limiting, saving the machine when `create` fails, GitLab CI builds) onto upstream Docker Machine 0.13.0. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/commit/9ba6da9ebd1140b65eb73e1ce08086ca72764c60.
- Treat HTTP 404 as success when the Google driver deletes a machine's disk, logging a warning; upstream 0.13.0 already does this for the instance. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/5.

## v0.11.0-gitlab.2

- Fix the DigitalOcean driver's `remove` retrying forever when the droplet no longer exists: it now stops after an HTTP 404 from the delete call. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/commit/9b5af13e87b48f395c8f4b08f229e20c2b510ce3.

## v0.11.0-gitlab.1

- Rebase the GitLab patches (DigitalOcean rate limiting, saving the machine when `create` fails) onto upstream Docker Machine master as of 23 May 2017. See https://github.com/docker/machine/pull/4098.
- Treat HTTP 404 as success when the Google driver deletes a machine's instance or disk, and check the error's status code instead of matching "notFound" in its text when starting a machine. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/5.
- Build linux amd64, arm and arm64, darwin amd64 and windows amd64 binaries in GitLab CI and keep them as job artifacts for one week. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/commit/4d0048fca7e5aa4eb5d4bc15405683649f3fd076.

## v0.10.0-gitlab.1

- Rebase the GitLab patches from v0.9.0-gitlab.1, unchanged, onto upstream Docker Machine 0.10.0. See https://github.com/docker/machine/pull/4010.

## v0.9.0-gitlab.1

- Base the fork on upstream Docker Machine master as of 25 January 2017. See https://github.com/docker/machine/pull/3928.
- Make the DigitalOcean driver rate-limit aware: API calls that get HTTP 429 are retried after the rate limit's reset time plus a random delay, a droplet is removed again if creation fails, and `create` waits 5 seconds before polling for the droplet's IP address, gives up after 60 polls, and waits 30 seconds once the address is assigned. The shared `WaitFor` retry helper also adds up to 9 seconds of random delay between attempts. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/2.
- Fix a crash in the DigitalOcean driver's `remove` when the API returns an error without a response. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/commit/2d7981403a055d58401361c739a8f74591416a81.
- Update the vendored `github.com/digitalocean/godo` to v0.9.0-75-gdfde623. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/commit/beb318e6cc37503c42eae4c4a74766bf05ab9287.
- Save the machine's configuration when `create` fails, so details such as the DigitalOcean droplet ID are kept. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/commit/c94e19330ee83b4f44ef166aa291440c6039572f.
- Build the docker-machine binary in GitLab CI and keep it as a job artifact. See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/commit/bcba73618edecd2ad10456d38cf30aea13e3c029.

# Docker Machine releases before the fork

# 0.16.0 (2018-11-08)

### General

- The default storage driver has been updated to `overlay2` for several systems
- Improved error reporting for the `ssh` subcommand when using the `--native-ssh` flag

### Drivers

**amazonec2**

- Improved handling of VPC errors

**openstack**

- Machine removal no longer fails upon attempting to delete a non-existing keypair

# 0.15.0 (2018-06-12)

## General

- `docker-machine` can now be installed using `go install`.
- Now built with go 1.10
- Added keep-alive to SSH connections (#4450)

## Drivers

### amazonec2

- Updated default AMIs to mitigate Meltdown and Spectre
- Added `--amazonec2-security-group-readonly` flag to prevent mutating security groups

### exoscale

- Updated driver to v0.9.23

### hyperv

- Fixed Hyper-V pre-create issues (#4426)
- Added the ability to disable Hyper-V dynamic memory management during VM creation (`--hyperv-disable-dynamic-memory`)

### vmwarefusion

- Improved shell checks (#4491)


# 0.14.0 (2018-03-06)

## General

* Added `--client-certs` flag to the `docker-machine regenerate-certs` command.
* Improved OpenBSD support
* Fixed a bug with `scp` commands issued from a Windows host.
* Enabled progress output by default for `scp` commands using `rsync`
* Added `--quiet` flag to `scp` to suppress progress output
* Machine now uses the `ss` command to detect connectivity when `netstat` is unavailable
* Added bash completion for `docker-machine mount`
* Improved provisioning resilience on Debian-based hosts

## Drivers

`amazonec2`
* Added support for `eu-west-3` region
* Upon failure, the `create` command now ensures dangling resources are cleaned up before exiting
* Machine creation no longer fails when waiting on spot instance readiness

`digitalocean`
* Added `--digitalocean-monitoring` flag
* Increased the default droplet size

`exoscale`
* Updated driver library
* Several improvements and fixes to the default machine template
* Added support for user-provided SSH key (`--exoscale-ssh-key`)
* Added support for arbitrary disk size

`google`
* Enabled disk auto-deletion on newly created machines
* Fixed a bug preventing the removal of a machine if it had already been removed remotely.
* Added support for fully qualified network and subnetwork names

`hyperv`
* Fixed potential cmdlet collision with VMWare powercli
* Fixed a bug with virtual switch selection
* Machine now correctly detects if the user is a Hyper-V administrator when using a localized version of Windows

`openstack`
* Added `--openstack-config-drive` flag
* Fixed an issue causing some user-uploaded keypairs to be removed when removing the associated machine.
* Fixed a bug preventing the removal of a machine if it had already been removed remotely.

`virtualbox`
* Added OpenBSD support

`vmwarefusion`
* Improved error detection and reporting when creating a new instance

`vmwarevsphere`
* Added  `--vmwarevsphere-folder` flag


# 0.13.0 (2017-10-12)

General

- Added new `docker-machine mount` command for mounting machine directories over SSHFS
- Improved some logging messages
- Fixed a bug with the `scp` command when using an identity file.
- Fixed a parsing error that caused the boot2docker ISO cache to malfunction, forcing a new download everytime.

Drivers

- `azure`
  - `docker-machine rm` now also cleans up the associated storage account if it has no remaining storage containers.
  - The creation process will no longer recreate the associated subnet if it already it exists.
- `exoscale`
  - Updated driver
  - Removed default `docker-machine` affinity group if no other affinity group was specified
- `virtualbox`
  - Fixed a bug where the machine would sometimes be assigned an invalid IP address at creation time.
- `vmwaresphere`
  - Added support for multiple networks

# 0.12.2 (2017-7-12)

General

* The `scp` sub-command now allows to provide an optional `user@` to the address.
* Fixed bash completion on OS X

Drivers

* `amazonec2`
  * Updated default AMIs to the latest version of Ubuntu 16.04 LTS
  * Fixed a bug preventing proper machine removal
* `vmwarevsphere`
  * Creating VMs on a DRS-enabled cluster should now work properly
  * Fixed a bug that prevented provisioning
* `vmwarefusion`
  * Fixed a bug that prevented provisioning
* `exoscale`
  * Updated library

# 0.12.1 (2017-6-30)

General

- Fixed an issue with the version comparison function that prevented machines created with Engine 17.06.0 from starting properly.

# 0.12.0 (2017-6-5)

General

- Various bash completion improvements
- Bump Go to version 1.8.3

Drivers

- `openstack`
    - Enable `HTTP_PROXY`
- `digitalocean`
    - Add support for tagging
- `virtualbox`
    - Scope DHCP address range based on CIDR
- `generic`
    - Increase default timeout
- `google`
    - Add subnetwork support

Provisioners

- Remove restriction on `--engine-install-url` in default-to-boot2docker drivers (`virtualbox`, `vmwarefusion`, etc.)
- Reduce provisioning time of SUSE/openSUSE systems

# 0.11.0 (2017-4-25)

General

- Various bugfixes and updated library dependencies
- new `docker-machine scp --delta` to invoke `rsync` behind the scenes for more efficient transfer

Drivers

- `digitalocean`
  - Add support for tagging DigitalOcean instances.
- `google`
  - Add support for subnetworks

# 0.10.0 (2017-2-27)

General

- Various improvements to shell tab completion
- Add support for compiling on ARM64 architecture

Drivers

- Make `virtualbox` default driver
- `amazonec2`
    - Update AMIs to latest version of Ubuntu 16.04 LTS
- `virtualbox`
    - Fix parsing of `--virtualbox-share-folder` on Windows
- `google`
    - Add `--google-open-port` flag to specify additional ports to open

Provisioners

- Machine now uses systemd drop-in files instead of over-writing the system units
- Add support for upgrade to new Docker versioning scheme
- Use `dockerd` only in Docker versions where it is available
- Support multiple architectures in SUSE provisioner

# 0.9.0 (2017-1-17)

General
- On Windows, the `COMPOSE_CONVERT_WINDOWS_PATHS` environment variable is now set by `docker-machine env` to improve Compose usability.
- Docker Machine can now be built on FreeBSD
- `docker-machine scp` non-22 port support
- `scp` supports SSH agent
- Bump Go version to 1.7.4

Drivers
- `amazonec2`
  - Credentials can now be loaded from IAM instance profiles
  - Add `--amazonec2-userdata` flag
  - Add `--amazonec2-block-duration-minutes` flag
  - Add support for `us-east-2` (Ohio)
  - Update base images to Ubuntu 16.04
- `azure`
  - Add `--azure-dns` flag for specifying DNS names
  - Add `--azure-storage-type` flag
  - Allow using vnets from another resource group
  - Add AzureGermanCloud support
  - Add support for custom data
  - Support Service Principal authentication
  - Update base images to Ubuntu 16.04
- `digitalocean`
  - Add ability to speicify the private SSH key path
- `gce`
  - Update base images to Ubuntu 16.04
- `virtualbox`
  - Shared folder location can be specified instead of "hardcoded" to `C:\Users` or `/Users`
- `openstack`
  - Add support for `OS_CACERT`

Provisioners
- OpenSUSE provisioner refactored to use properly supported 3rd party code


# 0.8.2 (2016-8-26)

- Update Go version to 1.7.1

# 0.8.1 (2016-8-20)

Provisioners
- Fix issue with generated systemd service file on RedHat family distros
Drivers
- `azure`
    - Bump Ubuntu image to 16.04
    - Update docs with updated default parameters
    - Change logging slightly

# 0.8.0 (2016-6-14)

General
- Fix issue with plugin heartbeat log repeating on disconnect
- Add `tcsh` support to `env --shell`
- Add `zsh` completion scripts
- Bump Go version to 1.6.2

Drivers
- `amazonec2`
    - Workaround to prevent orphaned SSH keys
- `virtualbox`
    - Add option for VM UI type (`--virtualbox-ui-type`)
- `vmwarefusion`
    - Fix CPU option inconsistency
- `openstack`
    - Expose user data parameter (`--openstack-user-data-file`)
- `generic`
    - Copy public key to created Machine directory

Provisioners
- Add Oracle Enterprise Linux support
- Fix port binding of Swarm master
- Add ability to create a manager instance which does not get scheduled on
- Introduce `--swarm-join-opt` to pass options to agent nodes
- Various SSH-related fixes
- Fix state for upgrade path

# 0.7.0 (2016-4-13)

General
- `DRIVER` environment variable now supported to supply value for `create --driver` flag
- Update to Go 1.6.1
- SSH client has been refactored
- RC versions of Machine will now create and upgrade to boot2docker RCs instead
  of stable versions if available

Drivers
- `azure`
    - Driver has been completely re-written to use resource templates and a significantly easier-to-use authentication model
- `digitalocean`
    - New `--digitalocean-ssh-key-fingerprint` for using existing SSH keys instead of creating new ones
- `virtualbox`
    - Fix issue with `bootlocal.sh`
    - New `--virtualbox-nictype` flag to set driver for NAT network
    - More robust host-only interface collision detection
    - Add support for running VirtualBox on a Windows 32 bit host
    - Change default DNS passthrough handling
- `amazonec2`
    - Specifying multiple security groups to use is now supported
- `exoscale`
    - Add support for user-data
- `hyperv`
    - Machines can now be created by a non-administrator
- `rackspace`
    - New `--rackspace-active-timeout` parameter
- `vmwarefusion`
    - Bind mount shared folder directory by default
- `google`
    - New `--google-use-internal-ip-only` parameter

Provisioners
- General
    - Support for specifying Docker engine port in some cases
- CentOS
    - Now defaults to using upstream `get.docker.com` script instead of custom RPMs.
- boot2docker
    - More robust eth* interface detection
- Swarm
    - Add `--swarm-experimental` parameter to enable experimental Swarm features


# 0.6.0 (2016-02-04)

+ Fix SSH wait before provisioning issue

# 0.6.0-rc4 (2016-02-03)

General

+ `env`
    + Fix shell auto detection

Drivers

+ `exoscale`
    + Fix configuration of exoscale endpoint

# 0.6.0-rc3 (2016-02-01)

- Exit with code 3 if error is during pre-create check

# 0.6.0-rc2 (2016-01-28)

- Fix issue creating Swarms
- Fix `ls` header issue
- Add code to wait for Docker daemon before returning from `start` / `restart`
- Start porting integration tests to Go from BATS
- Add Appveyor for Windows tests
- Update CoreOS provisioner to use `docker daemon`
- Various documentation and error message fixes
- Add ability to create GCE machine using existing VM

# 0.6.0-rc1 (2016-01-18)

General

- Update to Go 1.5.3
- Short form of command invocations is now supported
    - `docker-machine start`, `docker-machine stop` and others will now use
      `default` as the machine name argument if one is not specified
- Fix issue with panics in drivers
- Machine now returns exit code 3 if the pre-create check fails.
    - This is potentially useful for scripting `docker-machine`.
- `docker-machine provision` command added to allow re-running of provisioning
  on instances.
    - This allows users to re-run provisioning if it fails during `create`
      instead of needing to completely start over.

Provisioning

- Most provisioners now use `docker daemon` instead of `docker -d`
- Swarm masters now run with replication enabled
- If `/var/lib` is a BTRFS partition, `btrfs` will now be used as the storage
  driver for the instance

Drivers

- Amazon EC2
    - Default VPC will be used automatically if none is specified
    - Credentials are now be read from the conventional `~/.aws/credentials`
      file automatically
    - Fix a few issues such as nil pointer dereferences
- VMware Fusion
    - Try to get IP from multiple DHCP lease files
- OpenStack
    - Only derive tenant ID if tenant name is supplied

# 0.5.6 (2016-01-11)

General

- `create`
  - Set swarm master to advertise on port 3376
  - Fix swarm restart policy
  - Stop asking for ssh key passwords interactively
- `env`
  - Improve documentation
  - Fix bash on windows
  - Automatic shell detection on Windows
- `help`
  - Don't show the full path to `docker-machine.exe` on windows
- `ls`
  - Allow custom format
  - Improve documentation
- `restart`
  - Improve documentation
- `rm`
  - Improve documentation
  - Better user experience when removing multiple hosts
- `version`
  - Don't show the full path to `docker-machine.exe` on windows
- `start`, `stop`, `restart`, `kill`
  - Better logs and homogeneous behaviour across all drivers

Build

- Introduce CI tests for external binary compatibility
- Add amazon EC2 integration test

Misc

- Improve BugSnags reports: better shell detection, better windows version detection
- Update DockerClient dependency
- Improve bash-completion script
- Improve documentation for bash-completion

Drivers

- Amazon EC2
  - Improve documentation
  - Support optional tags
  - Option to create EbsOptimized instances
- Google
  - Fix remove when instance is stopped
- Openstack
  - Flags to import and reuse existing nova keypairs
- VirtualBox
  - Fix multiple bugs related to host-only adapters
  - Retry commands when `VBoxManage` is not ready
  - Reject VirtualBox versions older that 4.3
  - Fail with a clear message when Hyper-v installation prevents VirtualBox from working
  - Print a warning for Boot2Docker v1.9.1, which is known to have an issue with AUFS
- Vmware Fusion
  - Support soft links in VM paths

Libmachine

- Fix code sample that uses libmachine
- libmachine can be used in external applications


# 0.5.5 (2015-12-28)

General

- `env`
  - Better error message if swarm is down
  - Add quotes to command if there are spaces in the path
  - Fix Powershell env hints
  - Default to cmd shell on windows
  - Detect fish shell
- `scp`
  - Ignore empty ssh key
- `stop`, `start`, `kill`
  - Add feedback to the user
- `rm`
  - Now works when `config.json` is not found
- `ssh`
  - Disable ControlPath
  - Log which SSH client is used
- `ls`
  - Listing is now faster by reducing calls to the driver
  - Shows if the active machine is a swarm cluster

Build

- Automate 90% of the release process
- Upgrade to Go 1.5.2
- Don't build 32bits binaries for Linux and OSX
- Prevent makefile from defaulting to using containers

Misc

- Update docker-machine version
- Updated the bash completion with new options added
- Bugsnag: Retrieve windows version on non-English OS

Drivers

- Amazon EC2
  - Convert API calls to official SDK
  - Make DeviceName configurable
- Digital Ocean
  - Custom SSH port support
- Generic
  - Don't support `kill` since `stop` is not supported
- Google
  - Coreos provisionning
- Hyper-V
  - Lot's of code simplifications
  - Pre-Check that the user is an Administrator
  - Pre-Check that the virtual switch exists
  - Add Environment variables for each flag
  - Fix how Powershell is detected
  - VSwitch name should be saved to config.json
  - Add a flag to set the CPU count
  - Close handle after copying boot2docker.iso into vm folder - will otherwise keep hyper-v from starting vm
  - Update Boot2Docker cache in PreCreateCheck phase
- OpenStack
 - Filter floating IPs by tenant ID
- Virtualbox
  - Reject duplicate hostonlyifs Name/IP with clear message
  - Detect when hostonlyif can't be created. Point to known working version of VirtualBox
  - Don't create the VM if no hardware virtualization is available and add a flag to force create
  - Add `VBox.log` to bugsnag crashreport
  - Update Boot2Docker cache in PreCreateCheck phase
  - Detect Incompatibility with Hyper-v
- VSphere
 - Rewrite driver to work with govmomi instead of wrapping govc
- All
  - Change host restart to use the driver implementation
  - Fix truncated logs
  - Increase heartbeat interval and timeout

Provisioners

- Download latest Boot2Docker if it is out-of-date
- Add swarm config to coreos
- All provisioners now honor `engine-install-url`

# 0.5.4 (2015-12-28)

This is a patch release to fix a regression with STDOUT/STDERR behavior (#2587).

# 0.5.3 (2015-12-14)

**Please note**: With this release Machine will be reverting back to distribution in a single binary, which is more efficient on bandwidth and hard disk space. All the core driver plugins are now included in the main binary. You will want to delete the old driver binaries that you might have in your path.

e.g.:

```console
$ rm /usr/local/bin/docker-machine-driver-{amazonec2,azure,digitalocean,exoscale,generic,google,hyperv,none,openstack,rackspace,softlayer,virtualbox,vmwarefusion,vmwarevcloudair,vmwarevsphere}
```

Non-core driver plugins should still work as intended (in externally distributed binaries of the form `docker-machine-driver-name`.  Please report any issues you encounter them with externally loaded plugins.

General

- Optionally report crashes to Bugsnag to help us improve docker-machine
- Fix multiple nil dereferences in `docker-machine ls` command
- Improve the build and CI
- `docker-machine env` now supports emacs
- Run Swarm containers in provisioning step using Docker API instead of SSH/shell
- Show docker daemon version in `docker-machine ls`
- `docker-machine ls` can filter by engine label
- `docker-machine ls` filters are case insensitive
- `--timeout` flag for `docker-machine ls`
- Logs use `logrus` library
- Swarm container network is now `host`
- Added advertise flag to Swarm manager template
- Fix `help` flag for `docker-machine ssh`
- Add confirmation `-y` flag to `docker-machine rm`
- Fix `docker-machine config` for fish
- Embed all core drivers in `docker-machine` binary to reduce the bundle from 120M to 15M

Drivers

- Generic
	- Support password protected ssh keys though ssh-agent
	- Support DNS names
- Virtualbox
	- Show a warning if virtualbox is too old
	- Recognize yet another Hardware Virtualization issue pattern
	- Fix Hardware Virtualization on Linux/AMD
	- Add the `--virtualbox-host-dns-resolver` flag
	- Allow virtualbox DNSProxy override
- Google
	- Open firewall port for Swarm when needed
- VMware Fusion
	- Explicitly set umask before invoking vmrun in vmwarefusion
	- Activate the plugin only on OSX
	- Add id/gid option to mount when using vmhgfs
	- Fix for vSphere driver boot2docker ISO issues
- Digital Ocean
	- Support for creating Droplets with Cloud-init User Data
- Openstack
	- Sanitize keynames by replacing dots with underscores
- All
	- Most base images are now set to `Ubuntu 15.10`
	- Fix compatibility with drivers developed with docker-machine 0.5.0
	- Better error report for broken/incompatible drivers
	- Don't break `config.json` configuration when the disk is full

Provisioners

- Increase timeout for installing boot2docker
- Support `Ubuntu 15.10`

Misc

- Improve the documentation
- Update known drivers list

# 0.5.2 (2015-11-30)

General

-   Bash autocompletion and helpers fixed
-   Remove `RawDriver` from `config.json` - Driver parameters can now be edited
    directly again in this file.
-   Change fish `env` variable setting to be global
-   Add `docker-machine version` command
-   Move back to normal `codegangsta/cli` upstream
-   `--tls-san` flag for extra SANs

Drivers

-   Fix `GetURL` IPv6 compatibility
-   Add documentation page for available 3rd party drivers
-   VirtualBox
    -   Support for shared folders and virtualization detection on Linux hosts
    -   Improved detection of invalid host-only interface settings
-   Google
    -   Update default images
-   VMware Fusion
    -   Add option to disable shared folder
-   Generic
    -   New environment variables for flags

Provisioners

-   Support for Ubuntu >=15.04.  This means Ubuntu machines can be created which
    work with `overlay` driver of lib network.
-   Fix issue with current netstat / daemon availability checking

# 0.5.1 (2015-11-16)

-   Fixed boot2docker VM import regression
-   Fix regression breaking `docker-machine env -u` to unset environment variables
-   Enhanced virtualization capability detection and `VBoxManage` path detection
-   Properly lock VirtualBox access when running several commands concurrently
-   Allow plugins to write to STDOUT without `--debug` enabled
-   Fix Rackspace driver regression
-   Support colons in `docker-machine scp` filepaths
-   Pass environment variables for provisioned Engines to Swarm as well
-   Various enhancements around boot2docker ISO upgrade (progress bar, increased timeout)

# 0.5.0 (2015-11-1)

-   General
    -   Add pluggable driver model
    -   Clean up code to be more modular and reusable in `libmachine`
    -   Add `--github-api-token` for situations where users are getting rate limited
        by GitHub attempting to get the current `boot2docker.iso` version
    -   Various enhancements around the Makefile and build toolchain (still an active WIP)
    -   Disable SSH multiplex explicitly in commands run with the "External" client
    -   Show "-" for "inactive" machines instead of nothing
    -   Make daemon status detection more robust
-   Provisioners
    -   New CoreOS, SUSE, and Arch Linux provisioners
    -   Fixes around package installation / upgrade code on Debian and Ubuntu
-   CLI
    -   Support for regular expression pattern matching and matching by names in `ls --filter`
    -   `--no-proxy` flag for `env` (sets `NO_PROXY` in addition to other environment variables)
-   Drivers
    -   `openstack`
        -   `--openstack-ip-version` parameter
        -   `--openstack-active-timeout` parameter
    -   `google`
        -   fix destructive behavior of `start` / `stop`
    -   `hyperv`
        -   fix issues with PowerShell
    -   `vmwarefusion`
        -   some issues with shared folders fixed
        -   `--vmwarefusion-configdrive-url` option for configuration via `cloud-init`
    -   `amazonec2`
        -   `--amazonec2-use-private-address` option to use private networking
    -   `virtualbox`
        -   Enhancements around robustness of the created host-only network
        -   Fix IPv6 network mask prefix parsing
        -   `--virtualbox-no-share` option to disable the automatic home directory mount
        -   `--virtualbox-hostonly-nictype` and `--virtualbox-hostonly-nicpromisc` for controlling settings around the created hostonly NIC

# 0.4.1 (2015-08)

-   Fixes `upgrade` functionality on Debian based systems
-   Fixes `upgrade` functionality on Ubuntu based systems

# 0.4.0 (2015-08-11)

## Updates

-   HTTP Proxy support for Docker Engine
-   RedHat distros now use Docker Yum repositories
-   Ability to set environment variables in the Docker Engine
-   Internal libmachine updates for stability

## Drivers

-   Google:
    -   Preemptible instances
    -   Static IP support

## Fixes

-   Swarm Discovery Flag is verified
-   Timeout added to `ls` command to prevent hangups
-   SSH command failure now reports information about error
-   Configuration migration updates

# 0.3.0 (2015-06-18)

## Features

-   Engine option configuration (ability to configure all engine options)
-   Swarm option configuration (ability to configure all swarm options)
-   New Provisioning system to allow for greater flexibility and stability for installing and configuring Docker
-   New Provisioners
    -   Rancher OS
    -   RedHat Enterprise Linux 7.0+ (experimental)
    -   Fedora 21+ (experimental)
    -   Debian 8+ (experimental)
-   PowerShell support (configure Windows Docker CLI)
-   Command Prompt (cmd.exe) support (configure Windows Docker CLI)
-   Filter command help by driver
-   Ability to import Boot2Docker instances
-   Boot2Docker CLI migration guide (experimental)
-   Format option for `inspect` command
-   New logging output format to improve readability and display across platforms
-   Updated "active" machine concept - now is implicit according to `DOCKER_HOST` environment variable.  Note: this removes the implicit "active" machine and can no longer be specified with the `active` command.  You change the "active" host by using the `env` command instead.
-   Specify Swarm version (`--swarm-image` flag)

## Drivers

-   New: Exoscale Driver
-   New: Generic Driver (provision any host with supported base OS and SSH)
-   Amazon EC2
    -   SSH user is configurable
    -   Support for Spot instances
    -   Add option to use private address only
    -   Base AMI updated to 20150417
-   Google
    -   Support custom disk types
    -   Updated base image to v20150316
-   Openstack
    -   Support for Keystone v3 domains
-   Rackspace
    -   Misc fixes including environment variable for Flavor Id and stability
-   Softlayer
    -   Enable local disk as provisioning option
    -   Fixes for SSH access errors
    -   Fixed bug where public IP would always be returned when requesting private
    -   Add support for specifying public and private VLAN IDs
-   VirtualBox
    -   Use Intel network interface driver (adds great stability)
    -   Stability fixes for NAT access
    -   Use DNS pass through
    -   Default CPU to single core for improved performance
    -   Enable shared folder support for Windows hosts
-   VMware Fusion
    -   Boot2Docker ISO updated
    -   Shared folder support

## Fixes

-   Provisioning improvements to ensure Docker is available
-   SSH improvements for provisioning stability
-   Fixed SSH key generation bug on Windows
-   Help formatting for improved readability

## Breaking Changes

-   "Short-Form" name reference no longer supported Instead of "docker-machine " implying the active host you must now use docker-machine
-   VMware shared folders require Boot2Docker 1.7

## Special Thanks

We would like to thank all contributors.  Machine would not be where it is
without you.  We would also like to give special thanks to the following
contributors for outstanding contributions to the project:

-   @frapposelli for VMware updates and fixes
-   @hairyhenderson for several improvements to Softlayer driver, inspect formatting and lots of fixes
-   @ibuildthecloud for rancher os provisioning
-   @sthulb for portable SSH library                                              
-   @vincentbernat for exoscale                                                   
-   @zchee for Amazon updates and great doc updates

# 0.2.0 (2015-04-16)

Core Stability and Driver Updates

## Core

-   Support for system proxy environment
-   New command to regenerate TLS certificates
    -   Note: this will restart the Docker engine to apply
-   Updates to driver operations (create, start, stop, etc) for better reliability
-   New internal `libmachine` package for internal api (not ready for public usage)
-   Updated Driver Interface
    -   [Driver Spec](https://github.com/docker/machine/blob/master/docs/DRIVER_SPEC.md)
    -   Removed host provisioning from Drivers to enable a more consistent install
    -   Removed SSH commands from each Driver for more consistent operations
-   Swarm: machine now uses Swarm default binpacking strategy

## Driver Updates

-   All drivers updated to new Driver interface
-   Amazon EC2
    -   Better checking for subnets on creation
    -   Support for using Private IPs in VPC
    -   Fixed bug with duplicate security group authorization with Swarm
    -   Support for IAM instance profile
    -   Fixed bug where IP was not properly detected upon stop
-   DigitalOcean
    -   IPv6 support
    -   Backup option
    -   Private Networking
-   Openstack / Rackspace
    -   Gophercloud updated to latest version
    -   New insecure flag to disable TLS (use with caution)
-   Google
    -   Google source image updated
    -   Ability to specify auth token via file
-   VMware Fusion
    -   Paravirtualized driver for disk (pvscsi)
    -   Enhanced paravirtualized NIC (vmxnet3)
    -   Power option updates
    -   SSH keys persistent across reboots
    -   Stop now gracefully stops VM
    -   vCPUs now match host CPUs
-   SoftLayer
    -   Fixed provision bug where `curl` was not present
-   VirtualBox
    -   Correct power operations with Saved VM state
    -   Fixed bug where image option was ignored

## CLI

-   Auto-regeneration of TLS certificates when TLS error is detected
    -   Note: this will restart the Docker engine to apply
-   Minor UI updates including improved sorting and updated command docs
-   Bug with `config` and `env` with spaces fixed
    -   Note: you now must use `eval $(docker-machine env machine)` to load environment settings
-   Updates to better support `fish` shell
-   Use `--tlsverify` for both `config` and `env` commands
-   Commands now use eval for better interoperability with shell

## Testing

-   New integration test framework (bats)

# 0.1.0 (2015-02-26)

Initial beta release.

-   Provision Docker Engines using multiple drivers
-   Provide light management for the machines
    -   Create, Start, Stop, Restart, Kill, Remove, SSH
-   Configure the Docker Engine for secure communication (TLS)
-   Easily switch target machine for fast configuration of Docker Engine client
-   Provision Swarm clusters (experimental)

## Included drivers

-   Amazon EC2
-   Digital Ocean
-   Google
-   Microsoft Azure
-   Microsoft Hyper-V
-   Openstack
-   Rackspace
-   VirtualBox
-   VMware Fusion
-   VMware vCloud Air
-   VMware vSphere

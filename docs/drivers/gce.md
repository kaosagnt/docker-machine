<!--[metadata]>
+++
title = "Google Compute Engine"
description = "Google Compute Engine driver for machine"
keywords = ["machine, Google Compute Engine, driver"]
[menu.main]
parent="smn_machine_drivers"
+++
<![end-metadata]-->

# Google Compute Engine

Create machines on [Google Compute Engine](https://cloud.google.com/compute/).
You will need a Google account and a project id.
See <https://cloud.google.com/compute/docs/projects> for details on projects.

### Credentials

The Google driver uses [Application Default Credentials](https://developers.google.com/identity/protocols/application-default-credentials)
to get authorization credentials for use in calling Google APIs.

So if `docker-machine` is used from a GCE host, authentication will happen automatically
via the built-in service account.
Otherwise, [install gcloud](https://cloud.google.com/sdk/) and get
through the oauth2 process with `gcloud auth login`.

### Example

To create a machine instance, specify `--driver google`, the project id and the machine name.

    $ gcloud auth login
    $ docker-machine create --driver google --google-project PROJECT_ID vm01
    $ docker-machine create --driver google \
      --google-project PROJECT_ID \
      --google-zone us-central1-a \
      --google-machine-type f1-micro \
      vm02

### Options

    -   `--google-project`: **required** The id of your project to use when launching the instance.
    -   `--google-zone`: The zone to launch the instance.
    -   `--google-machine-type`: The type of instance.
    -   `--google-machine-image`: The absolute URL to a base VM image to instantiate.
    -   `--google-username`: The username to use for the instance.
    -   `--google-scopes`: The scopes for OAuth 2.0 to Access Google APIs. See [Google Compute Engine Doc](https://cloud.google.com/storage/docs/authentication).
    -   `--google-no-service-account`: Attach no service account to the VM. The metadata server then has no access token, and `--google-service-account` and `--google-scopes` are ignored.
    -   `--google-disk-size`: The disk size of instance.
    -   `--google-disk-type`: The disk type of instance.
    -   `--google-address`: Instance's static external IP (name or IP).
    -   `--google-preemptible`: Instance preemptibility.
    -   `--google-tags`: Instance tags (comma-separated).
    -   `--google-use-internal-ip`: When this option is used during create it will make docker-machine use internal rather than public NATed IPs. The flag is persistent in the sense that a machine created with it retains the IP. It's useful for managing docker machines from another machine on the same network e.g. while deploying swarm.
    -   `--google-use-internal-ip-only`: When this option is used during create, the new VM will not be assigned a public IP address. This is useful only when the host running `docker-machine` is located inside the Google Cloud infrastructure; otherwise, `docker-machine` can't reach the VM to provision the Docker daemon. The presence of this flag implies `--google-use-internal-ip`.
    -   `--google-use-existing`: Don't create a new VM, use an existing one. This is useful when you'd like to provision Docker on a VM you created yourself, maybe because it uses create options not supported by this driver.
    -   `--google-cos-tls-via-metadata`: Deliver the Docker TLS material as instance metadata instead of provisioning over SSH. See [TLS via metadata](#tls-via-metadata).

The GCE driver will use the `ubuntu-2204-jammy-v20250815` instance image unless otherwise specified. To obtain a
list of image URLs run:

    gcloud compute images list --uri

Environment variables and default values:

| CLI option                 | Environment variable     | Default                              |
| -------------------------- | ------------------------ | ------------------------------------ |
| **`--google-project`**     | `GOOGLE_PROJECT`         | -                                    |
| `--google-zone`            | `GOOGLE_ZONE`            | `us-central1-a`                      |
| `--google-machine-type`    | `GOOGLE_MACHINE_TYPE`    | `f1-standard-1`                      |
| `--google-machine-image`   | `GOOGLE_MACHINE_IMAGE`   | `ubuntu-2204-jammy-v20250815`        |
| `--google-username`        | `GOOGLE_USERNAME`        | `docker-user`                        |
| `--google-scopes`          | `GOOGLE_SCOPES`          | `devstorage.read_only,logging.write` |
| `--google-no-service-account` | `GOOGLE_NO_SERVICE_ACCOUNT` | -                                 |
| `--google-disk-size`       | `GOOGLE_DISK_SIZE`       | `10`                                 |
| `--google-disk-type`       | `GOOGLE_DISK_TYPE`       | `pd-standard`                        |
| `--google-address`         | `GOOGLE_ADDRESS`         | -                                    |
| `--google-preemptible`     | `GOOGLE_PREEMPTIBLE`     | -                                    |
| `--google-tags`            | `GOOGLE_TAGS`            | -                                    |
| `--google-use-internal-ip` | `GOOGLE_USE_INTERNAL_IP` | -                                    |
| `--google-use-existing`    | `GOOGLE_USE_EXISTING`    | -                                    |
| `--google-cos-tls-via-metadata` | `GOOGLE_COS_TLS_VIA_METADATA` | -                          |

### TLS via metadata

By default, `docker-machine create` waits for SSH on the new VM, copies the
CA and server certificate to `/etc/docker`, writes a systemd drop-in that
adds the TLS listener on port 2376, and restarts dockerd. The server
certificate contains the VM's IP address, which is only known after the VM
exists, so all of this has to happen after boot.

With `--google-cos-tls-via-metadata`, the server certificate is issued for
the machine name instead and generated before the VM exists, together with
the drop-in. Both are attached to the instance as metadata and the VM
installs them itself; `docker-machine` never connects over SSH during
create. It waits until the Docker API on port 2376 answers a ping over TLS
with the machine's certificates and `ServerName` set to the machine name,
then checks the Docker connection as usual. Clients have to verify the certificate against
the machine name rather than the address: `docker-machine` does so through
`HostOptions.AuthOptions.ServerName` in the machine's `config.json`, other
clients (the GitLab Runner docker+machine executor) read it from there. The
`docker` CLI has no such option, so `docker-machine env` alone is not enough
for it: point `DOCKER_HOST` at the machine name and resolve the name to the
address (for example through `/etc/hosts`), or use `docker-machine ssh`.

The VM gets these metadata attributes, in addition to the SSH key:

| Key                           | Content                                        | Install as                                          |
| ----------------------------- | ---------------------------------------------- | --------------------------------------------------- |
| `gitlab-docker-tls-ca`        | CA certificate, PEM                            | `/etc/docker/ca.pem`                                |
| `gitlab-docker-tls-cert`      | Server certificate, PEM                        | `/etc/docker/server.pem`                            |
| `gitlab-docker-tls-key`       | Server key, PEM                                | `/etc/docker/server-key.pem`, mode 0600             |
| `gitlab-docker-daemon-dropin` | systemd drop-in (the COS provisioner's output) | `/etc/systemd/system/docker.service.d/10-machine.conf` |

The image has to run something at boot that fetches the four attributes
from the metadata server, writes the files, opens port 2376 in the local
firewall (`iptables -A INPUT -p tcp --dport 2376 -j ACCEPT` on COS, whose
INPUT policy is DROP), runs `systemctl daemon-reload`, and starts or restarts
`docker.service`. On stock Container-Optimized OS, dockerd starts before
cloud-init processes the user-data, so a unit delivered through cloud-config
has to restart it. A missing attribute should fail the unit; `docker-machine`
then times out after five minutes and the create fails.

The flag applies to new instances only and is rejected together with
`--google-use-existing`. `--google-cos-wait-for-cloud-init` and
`--google-cos-docker-network-readiness-url` are not checked, since the SSH
provisioner does not run; order the unit that installs the TLS material
after whatever else the VM has to finish before it is ready. `docker-machine
ssh`, `regenerate-certs` and `provision` still work over SSH afterwards.

The server key is readable from inside the VM through the metadata server,
as `/etc/docker/server-key.pem` is by any privileged container.

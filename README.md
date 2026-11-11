# incus-redfish-proxy

A very simple proxy which allows to expose a Redfish API for a Incus instance.

```sh
incus-redfish-proxy [flags] <instance-name>
```

## Profiles

A profile describes how the proxy behaves towards a Redfish client. Real BMCs
(baseboard management controllers) differ in many details: how they apply BIOS
settings, whether secure boot keys
can be written, how virtual media is inserted, and how long the system refuses
writes after a power on. A profile reproduces such a behavior on top of an
Incus instance. Operations Center uses profiles in its end to end tests.

Select a profile with `--profile`:

```sh
incus-redfish-proxy --profile deferred-bios my-instance
incus-redfish-proxy --profile ./my-profile/profile.yaml my-instance
```

A value without a path separator and without the `.yaml` suffix names an
embedded profile. Anything else is the path of a profile file. Without
`--profile`, the proxy uses `generic`, which is the behavior of the proxy
before profiles existed.

`--list-profiles` prints the embedded profiles and exits.

`--timing-scale` multiplies every duration of the profile. `0.1` makes a
settle window of 20 seconds last 2 seconds. Tests use it to stay fast. If it is
not set, `timing.scale` of the profile applies.

The model of the system is `Incus (<profile name>)`, so a client can tell which
profile is active.

Embedded profiles:

| Profile                | Behavior                                                                                                |
|------------------------|---------------------------------------------------------------------------------------------------------|
| `generic`              | Every key holds its default. The behavior of the proxy before profiles existed.                         |
| `deferred-bios`        | BIOS settings stay pending until a reset. One attribute is only accepted once another one is in effect. |
| `no-secure-boot-write` | Secure boot databases are read only over Redfish. Keys can only be reset.                               |
| `oem-media`            | Virtual media is only inserted through an OEM action, answered with a task.                             |
| `stream-media`         | Virtual media is streamed with range requests while the server reads it.                                |

[profiles/README.md](profiles/README.md) describes every key of a profile and
how to draft a new profile from a BMC recording of Operations Center.

## Lazy virtual media

By default, the proxy downloads an inserted image into an Incus storage volume
before the insert completes. With `virtual_media.delivery` set to `stream` or
`prefetch`, the proxy instead mounts the image as a read only file through
FUSE (Filesystem in Userspace) and attaches that file to the instance.
`stream` turns every read into a range request. `prefetch` also downloads the
image in order into a local file while the instance reads it.

Requirements:

- The proxy runs on the Incus host. incusd opens the file directly, so the
  mount must be visible on the host.
- `/dev/fuse` and the `fuse3` package (`fusermount3`) are available.
- If the proxy does not run as root, `/etc/fuse.conf` contains
  `user_allow_other`. QEMU runs as an unprivileged user and needs this
  option to read the mount. Root may use it without the setting.
- The image server answers range requests.

`--media-dir` sets the directory for the mounts and the prefetch files. The
default is `/var/lib/incus-redfish-proxy/media`. Every instance gets its own
directory below it, named `<project>_<instance>`. On start, the proxy removes
stale mounts and files of an earlier run and mounts a streamed image again, if
the instance still has one attached. This only helps a stopped instance. A
running instance keeps the file of the old mount open. Its reads fail until a
client ejects the media and inserts it again.

If the image server does not support range requests,
`virtual_media.stream.fallback` decides: `full-fetch` downloads the image into
a storage volume as before, `error` refuses the insert. On other platforms than
Linux, FUSE is not supported and the fallback always applies. Other errors
always fail the insert.

## Known limitations

- BIOS attributes that need a stopped instance stay pending after a restart
  from within the guest. Only a reset through the proxy applies them.
- Enabling secure boot on a running instance applies only on a reset through
  the proxy.
- Incus copies new UEFI (Unified Extensible Firmware Interface) variables from
  the firmware template whenever `security.secureboot` changes.
  `SecureBootEnable` therefore drops the certificates and signatures added over
  Redfish. Enable secure boot first, then add the keys.
- `BootSourceOverrideEnabled: Once` sets the boot priority of the virtual media
  like `Continuous` and marks it as one time. The next power on drops both. A
  power on is a reset through the proxy, or an `instance-started` or
  `instance-restarted` event of Incus. The running instance keeps its boot
  order. Incus marks the instance with `volatile.vm.needs_reset`, so the next
  reboot from within the guest becomes a full restart. That restart no longer
  boots the virtual media.
- Streamed media requires the proxy to run on the Incus host, see above.

## Library

The package `redfishproxy` embeds the proxy into another program, for example
the end to end tests of Operations Center. `redfishproxy.NewHandler` takes a
`redfishproxy.Config`. Besides the instance, remote and project, it holds:

- `Profile`: the name of an embedded profile or the path of a profile file.
  Empty means `generic`.
- `TimingScale`: replaces `timing.scale` of the profile, if not zero.
- `MediaDir`: the directory for streamed media. Empty means
  `redfishproxy.DefaultMediaDir`.

`redfishproxy.Profiles()` returns the names of the embedded profiles.

## Tools

- `cmd/redfish-profile-draft` drafts a profile from a BMC recording of
  Operations Center, see [profiles/README.md](profiles/README.md).
- `cmd/redfish-scraper` downloads every resource of a Redfish service.

## End to end tests

The end to end tests in `e2e_tests` run the proxy in process against a real
Incus server. They create virtual machines, so they need KVM. The build tag
`e2e` keeps them out of `go test ./...`. Run them with:

```sh
make e2e-test
```

The tests check the preconditions first and list every missing one:

| Requirement                                                                        | Reason                                                                                  |
|------------------------------------------------------------------------------------|-----------------------------------------------------------------------------------------|
| Linux with `/dev/kvm`                                                              | Incus needs KVM for virtual machines.                                                   |
| A current stable Incus release, for example from the Zabbly packages               | The proxy needs the API extension `instance_nvram`. Incus 6.0 of Ubuntu 24.04 lacks it. |
| The user is in the group `incus-admin`, or `IRP_E2E_REMOTE` names a trusted remote | The tests create a project, virtual machines and volumes.                               |
| A storage pool named `default`                                                     | The full fetch of virtual media imports into it.                                        |
| `/dev/fuse` and the package `fuse3`                                                | Streamed media is a FUSE mount.                                                         |
| `user_allow_other` in `/etc/fuse.conf`, unless the tests run as root               | QEMU runs as an unprivileged user and reads the mount.                                  |
| Internet access                                                                    | The stream tests download a pinned Alpine Linux image once.                             |

The tests run in the project `irp-e2e-<random>` and delete it at the end.
Environment variables change the defaults:

| Variable                 | Default                            | Meaning                                                                                                                                       |
|--------------------------|------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------|
| `IRP_E2E_REMOTE`         | default remote of the Incus client | Incus remote to test against.                                                                                                                 |
| `IRP_E2E_POOL`           | `default`                          | Storage pool of the root disks.                                                                                                               |
| `IRP_E2E_MEDIA_DIR`      | `~/.cache/irp-e2e/media`           | Directory for streamed media. QEMU must be able to reach it, so a home directory with mode 0700 does not work. `/var/lib/irp-e2e/media` does. |
| `IRP_E2E_CACHE`          | `~/.cache/irp-e2e/images`          | Cache for the downloaded boot image.                                                                                                          |
| `IRP_E2E_LOG_DIR`        | `e2e_tests/logs`                   | Proxy log, and the config and logs of every instance of a failed test.                                                                        |
| `IRP_E2E_KEEP`           | empty                              | Any value keeps the project and its instances for debugging.                                                                                  |
| `IRP_E2E_TIMEOUT_FACTOR` | `1`                                | Multiplies every timeout, for slow machines.                                                                                                  |

The GitHub workflow `.github/workflows/e2e.yml` runs the tests on a hosted
runner. Its setup steps show a complete installation.

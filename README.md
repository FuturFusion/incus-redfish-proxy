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

- BIOS attributes, which need a stopped instance, stay pending after a restart
  from within the guest. Only a reset through the proxy applies them.
- Enabling secure boot on a running instance applies only on a reset through
  the proxy.
- `BootSourceOverrideEnabled: Once` sets the boot priority of the virtual media
  like `Continuous` and marks it as one time. The next power on, that Incus
  reports as `instance-started`, drops both. Incus applies a changed boot order
  only when the instance starts (`volatile.vm.needs_reset`), so the dropped
  priority takes effect on the start after that, not on a reboot from within
  the guest.
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

# Profiles

A profile describes how the proxy behaves towards a Redfish client. Every
directory below `profiles/` holds one profile:

- `profile.yaml`: the profile. Only this file is embedded into the proxy.
- `README.md`: where the profile comes from and what is still unverified.
- `recording.jsonl.gz` and `manifest.json`: the anonymized BMC (baseboard
  management controller) recording of Operations Center that the profile is
  drawn from. Profiles without hardware
  behind them have no recording.

## Format

A profile file only states the keys that differ from the defaults. The
defaults are the values of [generic/profile.yaml](generic/profile.yaml), which
lists every key. The proxy refuses unknown keys, so a typing error does not go
unnoticed.

```yaml
version: 1
name: my-profile
description: One sentence about the behavior.

bios:
  pending: true
  settings_object: true
```

`version` is `1`. `name` contains lower case letters, digits and dashes. It
must match the directory name of an embedded profile. `description` is one
sentence that `--list-profiles` prints.

An answer key names the HTTP status and the shape of the body:

- `204`: no body.
- `202-task`: 202 with a task monitor.
- `200`: 200 with a top level `@Message.ExtendedInfo`.
- `200-error`: 200 with the messages inside an `error` object. Some BMCs
  answer a success like this. This is not standard Redfish, but a client must
  accept it.
- `200-resource`: 200 with the changed resource.

The message is `Base.1.18.Success`, if the change takes effect at once. It is
`Base.1.18.ResetRequired`, if the change waits for the next power on.

Write durations like `20s` or `1m30s`. Sizes are plain bytes or a number
with `KiB`, `MiB` or `GiB`. An error shape is
`{status: <HTTP status>, message_id: <Registry>.<major>.<minor>.<Id>}`.

The column "OC" (Operations Center) names the code path in the Redfish adapter
of Operations Center (`internal/provisioning/adapter/bmc/redfish`) that a key
exercises.

### `bios`

| Key                             | Default                                                | Values                                                                                         | OC                                              |
|---------------------------------|--------------------------------------------------------|------------------------------------------------------------------------------------------------|-------------------------------------------------|
| `bios.patch_response`           | `202-taskmonitor`                                      | `202-taskmonitor`, `202-task` (needs `task_resource`), `200`, `204`                            | `ApplyBIOSAttributes`, `WaitForTask`            |
| `bios.task_resource`            | `false`                                                | `true` serves `/redfish/v1/TaskService` and its collection, single tasks are always served     | `taskState`                                     |
| `bios.settings_object`          | `false`                                                | `true` makes `Bios/Settings` a separate resource with the pending attributes                   | `updateBIOSAttributes`                          |
| `bios.pending`                  | `false`                                                | `true` stages the attributes of a PATCH until the next reset (needs `settings_object`)         | `ApplyBIOSAttributes`, `BIOSAttributes`         |
| `bios.apply_time.supported`     | `[]`                                                   | `Immediate`, `OnReset` (needs `pending`), published as `@Redfish.Settings.SupportedApplyTimes` | `getBIOSSettingsApplyTimes`                     |
| `bios.apply_time.default`       | `""`                                                   | Apply time of a PATCH without one. Empty means `OnReset` with `pending`, `Immediate` otherwise | `updateBIOSAttributes`                          |
| `bios.patch_requires_power_off` | `true`                                                 | `false` stages attributes on a running instance                                                | `ApplyBIOSAttributes`                           |
| `bios.registry.id`              | `""`                                                   | `<Name>.v<major>_<minor>_<patch>`, required for synthetic attributes                           | `getBIOSAttributeRegistry`                      |
| `bios.registry.attributes`      | `[]`                                                   | Synthetic attributes, see below                                                                | `BIOSAttributes`, `describeBIOSAttributesError` |
| `bios.errors.unknown_attribute` | `{status: 400, message_id: Base.1.18.PropertyUnknown}` | Error shape for a write to an unknown attribute                                                | `describeBIOSAttributesError`                   |
| `bios.errors.deferred_rejected` | `{status: 400, message_id: Base.1.18.PropertyUnknown}` | Error shape for a write to an attribute, whose requirement is not in effect                    | deferred BIOS pass                              |

A synthetic attribute has a `name` and a `type`: `Enumeration`, `String`,
`Integer` or `Boolean`. Optional fields:

- `values`: the values of an enumeration.
- `default`: the value before the first write.
- `read_only`: refuses writes.
- `min`, `max`: the bounds of an integer.
- `maps_to`, `value_map`: stores the attribute in an Incus config key instead
  of the proxy state. `user.redfish.*` keys are reserved for the proxy. Every
  value must map to a different Incus value.
- `requires: {attribute, value}`: hides and refuses the attribute until the
  other attribute has this value in effect. Real firmware behaves like this
  for options that only exist in a certain mode.

### `secure_boot`

| Key                                           | Default                                                   | Values                                                                                                                            | OC                                                |
|-----------------------------------------------|-----------------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------|---------------------------------------------------|
| `secure_boot.database_write`                  | `true`                                                    | `false` makes the certificates and signatures read only                                                                           | `ApplySecureBootCertificates`                     |
| `secure_boot.database_write_error`            | `{status: 405, message_id: Base.1.18.ActionNotSupported}` | Error shape for a refused write                                                                                                   | `ApplySecureBootCertificates`                     |
| `secure_boot.database_add_response`           | `"201"`                                                   | `201` with `Location` and the entry, `204` without `Location` and body                                                            | `ApplySecureBootCertificates`                     |
| `secure_boot.database_delete_response`        | `"204"`                                                   | `204`, `200`, `200-error`                                                                                                         | `wipeSecureBootDatabase`, `deleteSecureBootEntry` |
| `secure_boot.reset_keys.supported`            | `false`                                                   | `true` publishes `#SecureBoot.ResetKeys`                                                                                          | `secureBootSupportsResetKeys`                     |
| `secure_boot.reset_keys.types`                | `[DeleteAllKeys, DeletePK]`                               | `DeleteAllKeys`, `DeletePK`                                                                                                       | `ResetSecureBootKeys`                             |
| `secure_boot.reset_keys.response`             | `"204"`                                                   | `204`, `202-task`, `200`, `200-error`                                                                                             | `ResetSecureBootKeys`, `WaitForTask`              |
| `secure_boot.reset_keys.applies`              | `immediately`                                             | `immediately` (needs a stopped instance), `on_reset` (the next reset that powers on applies it)                                   | `isDeploymentInSecureBootSetupMode`               |
| `secure_boot.reset_keys.disables_secure_boot` | `false`                                                   | `true` switches `SecureBootEnable` off, when the keys are deleted, like HPE iLO. Operations Center then enables secure boot again | `enableDeploymentSecureBoot`                      |
| `secure_boot.reset_keys.unreachable.after`    | `0s`                                                      | Time from the power on that follows a key reset to the start of a window, in which the proxy answers no request                   | `isTransientTransportError`                       |
| `secure_boot.reset_keys.unreachable.for`      | `0s`                                                      | Length of the window. A request that arrives in it gets no answer, also after the window, until the client gives up               | `isTransientTransportError`                       |
| `secure_boot.certificate_id_style`            | `hash`                                                    | `hash`, `sequential` (one counter for all databases, IDs are never used again until a key reset)                                  | `wipeSecureBootDatabase`, `deleteSecureBootEntry` |
| `secure_boot.certificate_id_prefix`           | `""`                                                      | `sequential` only, `Certificate` gives `Certificate1`                                                                             | `readSecureBootDatabase`                          |
| `secure_boot.enable_requires_power_off`       | `true`                                                    | `false` accepts `SecureBootEnable` on a running instance and applies it on the next reset                                         | `EnableSecureBoot`                                |
| `secure_boot.power_cycle.after`               | `[]`                                                      | `reset_keys`, `enable`. During the boot that applies such a change, the system powers itself off and on again                     | `checkDeploymentPoweredOff`                       |
| `secure_boot.power_cycle.off_after`           | `0s`                                                      | Time from the power on to the power off of the cycle                                                                              | `checkDeploymentPoweredOff`                       |
| `secure_boot.power_cycle.off_for`             | `0s`                                                      | Time the system stays off. A cycle stops, if a client changes the power state in the meantime                                     | `checkDeploymentPoweredOff`                       |
| `secure_boot.empty_after_power_on.times`      | `0`                                                       | After this many power ons that follow a write to a key database, the certificate and signature collections read empty             | `verifyDeploymentSecureBootCertificates`          |
| `secure_boot.empty_after_power_on.for`        | `0s`                                                      | How long the collections read empty after such a power on                                                                         | `verifyDeploymentSecureBootCertificates`          |

### `virtual_media`

| Key                                      | Default                                                       | Values                                                                                                                                                                                                                                           | OC                                                          |
|------------------------------------------|---------------------------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-------------------------------------------------------------|
| `virtual_media.insert`                   | `standard`                                                    | `standard` (`#VirtualMedia.InsertMedia`), `oem` (OEM action only), `patch` (PATCH of the resource only)                                                                                                                                          | `insertMedia`                                               |
| `virtual_media.oem_vendor`               | `Incus`                                                       | Key below `Actions.Oem`. Never a real vendor                                                                                                                                                                                                     | `oemActionTarget`                                           |
| `virtual_media.oem_action`               | `InsertVirtualMedia`                                          | Name of the OEM action                                                                                                                                                                                                                           | `oemActionTarget`                                           |
| `virtual_media.insert_response`          | `"204"`                                                       | `204`, `202-task`, `200`, `200-error`. Applies to the eject actions as well, they answer 204 for `202-task`                                                                                                                                      | `AttachMedia`, `WaitForTask`                                |
| `virtual_media.boot_override_once`       | `false`                                                       | `true` offers `BootSourceOverrideEnabled` `Once`, the next power on drops it                                                                                                                                                                     | `bootSourceOverrideEnabledCandidates`, `overrideBootDevice` |
| `virtual_media.refused_parameters.names` | `[]`                                                          | InsertMedia parameters the BMC does not know: `Inserted`, `MediaType`, `Password`, `TransferMethod`, `TransferProtocolType`, `UserName`, `WriteProtected`. An InsertMedia request that holds one is refused, and the ActionInfo does not list it | `isHintRejected`                                            |
| `virtual_media.refused_parameters.error` | `{status: 400, message_id: Base.1.18.ActionParameterUnknown}` | Error shape of the refusal. The answer names the parameter, so OC sends the insert again without it                                                                                                                                              | `isHintRejected`                                            |
| `virtual_media.locations`                | `[managers]`                                                  | `managers`, `systems`. The collections that show the one virtual media device. The first one names the device in tasks                                                                                                                           | `getVirtualMediaByID`                                       |
| `virtual_media.id`                       | `CD`                                                          | Member ID of the device at every location. OC addresses it as `manager:<id>` or `system:<id>`                                                                                                                                                    | `getVirtualMediaByID`                                       |
| `virtual_media.delivery`                 | `full-fetch`                                                  | `full-fetch`, `stream`, `prefetch`, see the main README                                                                                                                                                                                          | none, invisible to OC                                       |
| `virtual_media.stream.read_ahead`        | `4MiB`                                                        | Size of every range request, at least `4KiB`, at most `cache`                                                                                                                                                                                    | none                                                        |
| `virtual_media.stream.cache`             | `64MiB`                                                       | Bytes kept in memory                                                                                                                                                                                                                             | none                                                        |
| `virtual_media.stream.fallback`          | `full-fetch`                                                  | `full-fetch`, `error`, if the server has no range requests or FUSE is missing                                                                                                                                                                    | none                                                        |

### `power`

| Key                                | Default                                                              | Values                                                                                                                                                                                                                                                                                                         | OC                            |
|------------------------------------|----------------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-------------------------------|
| `power.reset_types`                | `[]`                                                                 | `On`, `ForceOn`, `GracefulShutdown`, `ForceOff`, `GracefulRestart`, `ForceRestart`, published as `ResetType@Redfish.AllowableValues`. Empty publishes nothing and accepts every type                                                                                                                           | `performReset`                |
| `power.response`                   | `"204"`                                                              | `204`, `202-task`, `200`, `200-error`                                                                                                                                                                                                                                                                          | `performReset`, `WaitForTask` |
| `power.not_settled_window`         | `0s`                                                                 | Writes below `Systems` and `Managers` are refused for this long after a power on                                                                                                                                                                                                                               | `isNotSettledRedfishError`    |
| `power.not_settled_error`          | `{status: 400, message_id: Base.1.18.ResourceInStandby}`             | Error shape of the refusal. OC waits only for IDs ending in `ResourceInStandby` or `UnableToModifyDuringSystemPOST`                                                                                                                                                                                            | `isNotSettledRedfishError`    |
| `power.delay`                      | `0s`                                                                 | A reset waits this long before it changes the power state. One duration for both, or `{on: 20s, off: 5s}`. `on` applies to On and ForceOn, `off` to the other types. With a delay, the answer comes at once, like on a real BMC. Choose a delay above about 10s to make the first poll of OC see the old state | `hasPowerState`               |
| `power.redundant_reset_errors.off` | `null`                                                               | Error shape for ForceOff and GracefulShutdown of a system that is off. `null` accepts the reset and does nothing                                                                                                                                                                                               | `performReset`                |
| `power.redundant_reset_errors.on`  | `null`                                                               | Error shape for On and ForceOn of a system that is on. `null` accepts the reset and does nothing                                                                                                                                                                                                               | `performReset`                |
| `power.busy_reads.resources`       | `[]`                                                                 | `system`, `bios`, `secure_boot`. Reads of these resources are refused after a power on                                                                                                                                                                                                                         | `retryTransport`              |
| `power.busy_reads.failures`        | `0`                                                                  | After every power on, the first reads of each resource fail this many times. OC asks again up to 3 times                                                                                                                                                                                                       | `retryTransport`              |
| `power.busy_reads.error`           | `{status: 503, message_id: Base.1.18.ServiceTemporarilyUnavailable}` | Error shape of the refusal. OC asks again only for 429, 502, 503 and 504                                                                                                                                                                                                                                       | `retryTransport`              |

### `responses`

| Key               | Default | Values                                                                                         | OC                                       |
|-------------------|---------|------------------------------------------------------------------------------------------------|------------------------------------------|
| `responses.patch` | `"204"` | `204`, `200-resource`, `200`, `200-error`. PATCH of `Systems`, `SecureBoot` and `VirtualMedia` | `overrideBootDevice`, `EnableSecureBoot` |

The BIOS PATCH has its own key, `bios.patch_response`.

### `timing`

| Key            | Default | Values                                                                 | OC   |
|----------------|---------|------------------------------------------------------------------------|------|
| `timing.scale` | `1.0`   | Multiplies every duration of the profile. `--timing-scale` replaces it | none |

### `overlay`

| Key       | Default | Values                                                   | OC                                 |
|-----------|---------|----------------------------------------------------------|------------------------------------|
| `overlay` | `{}`    | Path to JSON merge patch (RFC 7386) for the GET response | any reader of the patched resource |

The paths may contain the placeholders `{system}`, `{manager}` and `{media}`.
The proxy ignores a trailing slash, so `/X` and `/X/` must not both appear.
A `null` in the patch removes a property.

```yaml
overlay:
  "/redfish/v1/Systems/{system}":
    Oem: {Incus: {ServerPowerState: "On"}}
```

## From a BMC recording to a profile

Operations Center records the Redfish traffic of a deployment. The tool
`redfish-profile-draft` turns such a recording into a draft profile.

1. Deploy the server with recording turned on. The flag is hidden, as it is
   meant for development:

   ```sh
   operations-center provisioning server deploy --record-bmc <server> <token-uuid> <seed>
   ```

2. Download the anonymized recording:

   ```sh
   operations-center admin bmc-recording download --anonymize <server> <id>
   ```

3. Draft the profile:

   ```sh
   go run ./cmd/redfish-profile-draft --name <name> --out profiles/<name>/profile.yaml <server>-<id>-anonymized.zip
   ```

   Every value names the sequence numbers of the exchanges it is drawn from,
   for example `# seq 12, 14`. The comment of a value that the tool infers
   instead of observing contains `TODO verify`, for example
   `# seq 12; TODO verify`. The tool lists the keys without evidence on standard
   error. If the tool knows why there is no evidence, it adds the reason, for
   example `Operations Center sent no BIOS PATCH`. Settings of the proxy, like
   `timing.scale`, are marked `proxy setting`. No recording can show them, so
   the tool does not list them.

4. Review the draft. Resolve every `TODO verify`. Keep vendor attribute names
   out of the profile. The draft lists them in comments only.

5. Store the recording next to the profile: unzip it, check that
   `manifest.json` has `final_state: completed`, and gzip `exchanges.jsonl` as
   `profiles/<name>/recording.jsonl.gz`. Copy `manifest.json` as well. Check the
   anonymization by hand: no credentials, no host names, no MAC addresses. Ask
   the maintainers before you commit a recording larger than 10 MB.

6. Write `profiles/<name>/README.md` from the template below.

7. Add the profile to the end to end tests of Operations Center. Run them and
   compare the deployment state history with `history` in `manifest.json`.

`redfish-profile-draft` also reads a profile directory. So
`go run ./cmd/redfish-profile-draft --name <name> profiles/<name>` drafts the profile again
from the archived recording.

## README of a profile

```markdown
# <name>

- Hardware: <model>, BMC firmware <version>, BIOS <version>
- Recorded: <date> with Operations Center <version>, by <role, not a name>
- Recording: recording.jsonl.gz (anonymized), manifest.json
- Unverified values: <keys still marked TODO verify>
```

A profile without hardware behind it says "synthetic, not derived from
hardware" instead.

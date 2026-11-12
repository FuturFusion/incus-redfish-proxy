# Recording comparison

## Summary

- Steps: 3 of 7 states on both sides
- Transitions: 2 of 8 state transitions on both sides
- Operations: 2 of 5 method and kind pairs on both sides
- Errors: 0 of 0 error outcomes of the reference also in the candidate
- Keys: 1 of 6 profile keys equal in both drafts

## Header

|                     | Reference        | Candidate        |
| ------------------- | ---------------- | ---------------- |
| Server model        | Example Server 1 | Example Server 1 |
| BMC firmware        | 1.30.20          | 1.30.20          |
| BIOS                | 2.4.1            | 2.4.1            |
| Operations Center   | 0.11.0           | 0.11.0           |
| Final state         | completed        | completed        |
| Exchanges           | 9                | 6                |
| Truncated exchanges | 0                | 0                |
| Duration            | 9s               | 35s              |

## Deployment steps

Steps: 3 of 7 states on both sides
Transitions: 2 of 8 state transitions on both sides

| State                    | Ref | Cand | Flags          |
| ------------------------ | --- | ---- | -------------- |
| refresh-bmc-data         | yes | yes  |                |
| check-bios               | yes | no   | reference only |
| apply-bios               | yes | no   | reference only |
| verify-bios              | yes | no   | reference only |
| secure-boot-certificates | yes | no   | reference only |
| attach-media             | yes | yes  |                |
| power-on-install         | yes | yes  |                |

State transitions on one side only:

- reference only: refresh-bmc-data → check-bios
- reference only: check-bios → apply-bios
- reference only: apply-bios → verify-bios
- reference only: verify-bios → secure-boot-certificates
- reference only: secure-boot-certificates → attach-media
- candidate only: refresh-bmc-data → attach-media

## Operations per step

Operations: 2 of 5 method and kind pairs on both sides

The kind columns count pairs of method and kind. The run columns count runs and exchanges.
Only steps with exchanges on both sides count for the parity.

| State                    | Both | Ref only | Cand only | Ref runs | Cand runs |
| ------------------------ | ---- | -------- | --------- | -------- | --------- |
| refresh-bmc-data         | 1    | 0        | 0         | 1 / 1    | 1 / 1     |
| check-bios               | 0    | 1        | 0         | 1 / 1    | 0 / 0     |
| apply-bios               | 0    | 1        | 0         | 1 / 1    | 0 / 0     |
| verify-bios              | 0    | 1        | 0         | 1 / 1    | 0 / 0     |
| secure-boot-certificates | 0    | 3        | 0         | 3 / 3    | 0 / 0     |
| attach-media             | 0    | 1        | 1         | 1 / 1    | 1 / 1     |
| power-on-install         | 1    | 0        | 1         | 1 / 1    | 3 / 4     |

Operations on one side only:

- `check-bios`:
  - reference only: GET /redfish/v1/systems/{id}/bios
- `apply-bios`:
  - reference only: PATCH /redfish/v1/systems/{id}/bios
- `verify-bios`:
  - reference only: GET /redfish/v1/systems/{id}/bios
- `secure-boot-certificates`:
  - reference only: GET /redfish/v1/systems/{id}/secureboot
  - reference only: GET /redfish/v1/systems/{id}/secureboot/securebootdatabases/db/certificates
  - reference only: POST /redfish/v1/systems/{id}/secureboot/securebootdatabases/db/certificates
- `attach-media`:
  - reference only: POST VirtualMedia.InsertMedia
  - candidate only: POST Oem.InsertVirtualMedia
- `power-on-install`:
  - candidate only: PATCH /redfish/v1/systems/{id}

## Errors and retries

Errors: 0 of 0 error outcomes of the reference also in the candidate

| State            | Operation                      | Outcome                                   | Ref | Cand | Flags          |
| ---------------- | ------------------------------ | ----------------------------------------- | --- | ---- | -------------- |
| power-on-install | PATCH /redfish/v1/systems/{id} | 400 Vendor.UnableToModifyDuringSystemPOST | 0   | 2    | candidate only |

## Profile keys

Keys: 1 of 6 profile keys equal in both drafts

| Key                                         | Ref      | Cand     | Differences            |
| ------------------------------------------- | -------- | -------- | ---------------------- |
| bios.apply_time.default                     | gap      | gap      |                        |
| bios.apply_time.supported                   | gap      | gap      |                        |
| bios.errors.deferred_rejected               | gap      | gap      |                        |
| bios.errors.unknown_attribute               | gap      | gap      |                        |
| bios.patch_requires_power_off               | gap      | gap      |                        |
| bios.patch_response                         | observed | gap      | ref≠cand               |
| bios.pending                                | observed | gap      |                        |
| bios.registry.attributes                    | inferred | gap      | ref≠cand               |
| bios.registry.id                            | observed | gap      | ref≠cand               |
| bios.settings_object                        | observed | gap      |                        |
| bios.task_resource                          | gap      | gap      |                        |
| power.busy_reads.error                      | gap      | gap      |                        |
| power.busy_reads.failures                   | gap      | gap      |                        |
| power.busy_reads.resources                  | gap      | gap      |                        |
| power.delay                                 | gap      | gap      |                        |
| power.not_settled_error                     | gap      | observed | cand≠profile, ref≠cand |
| power.not_settled_window                    | gap      | inferred | cand≠profile, ref≠cand |
| power.redundant_reset_errors.off            | gap      | gap      |                        |
| power.redundant_reset_errors.on             | gap      | gap      |                        |
| power.reset_types                           | observed | observed | cand≠profile, ref≠cand |
| power.response                              | observed | observed | cand≠profile, ref≠cand |
| responses.patch                             | gap      | observed |                        |
| secure_boot.certificate_id_prefix           | gap      | gap      |                        |
| secure_boot.certificate_id_style            | observed | gap      |                        |
| secure_boot.database_add_response           | observed | gap      |                        |
| secure_boot.database_delete_response        | gap      | gap      |                        |
| secure_boot.database_write                  | observed | gap      |                        |
| secure_boot.database_write_error            | gap      | gap      |                        |
| secure_boot.empty_after_power_on.for        | gap      | gap      |                        |
| secure_boot.empty_after_power_on.times      | observed | gap      |                        |
| secure_boot.enable_requires_power_off       | gap      | gap      |                        |
| secure_boot.power_cycle.after               | gap      | gap      |                        |
| secure_boot.power_cycle.off_after           | gap      | gap      |                        |
| secure_boot.power_cycle.off_for             | gap      | gap      |                        |
| secure_boot.reset_keys.applies              | gap      | gap      |                        |
| secure_boot.reset_keys.disables_secure_boot | gap      | gap      |                        |
| secure_boot.reset_keys.response             | gap      | gap      |                        |
| secure_boot.reset_keys.supported            | observed | gap      |                        |
| secure_boot.reset_keys.types                | gap      | gap      |                        |
| secure_boot.reset_keys.unreachable.after    | gap      | gap      |                        |
| secure_boot.reset_keys.unreachable.for      | gap      | gap      |                        |
| virtual_media.boot_override_once            | observed | observed | cand≠profile, ref≠cand |
| virtual_media.delivery                      | gap      | gap      |                        |
| virtual_media.id                            | observed | observed |                        |
| virtual_media.insert                        | observed | observed | cand≠profile, ref≠cand |
| virtual_media.insert_response               | observed | observed | cand≠profile, ref≠cand |
| virtual_media.locations                     | gap      | gap      |                        |
| virtual_media.oem_action                    | fixed    | observed |                        |
| virtual_media.refused_parameters.error      | gap      | fixed    |                        |
| virtual_media.refused_parameters.names      | observed | fixed    |                        |

Values and sequence numbers of the keys:

- `bios.apply_time.default`:
  - both drafts: `"Immediate"`
  - profile.yaml: `"Immediate"`
- `bios.apply_time.supported`:
  - both drafts: `[]`
  - profile.yaml: `[]`
- `bios.errors.deferred_rejected`:
  - both drafts: `{"message_id":"Base.1.18.PropertyUnknown","status":400}`
  - profile.yaml: `{"message_id":"Base.1.18.PropertyUnknown","status":400}`
- `bios.errors.unknown_attribute`:
  - both drafts: `{"message_id":"Base.1.18.PropertyUnknown","status":400}`
  - profile.yaml: `{"message_id":"Base.1.18.PropertyUnknown","status":400}`
- `bios.patch_requires_power_off`:
  - both drafts: `true`
  - profile.yaml: `true`
- `bios.patch_response`:
  - reference draft: `"204"`
  - candidate draft: `"202-taskmonitor"`
  - profile.yaml: `"204"`
  - reference seq: 3
  - reference draft ≠ candidate draft
- `bios.pending`:
  - both drafts: `false`
  - profile.yaml: `false`
  - reference seq: 3, 4
- `bios.registry.attributes`:
  - reference draft: `["Boolean","Integer","String"]`
  - candidate draft: `[]`
  - profile.yaml: `["Boolean","Integer","String"]`
  - reference seq: 3
  - reference draft ≠ candidate draft
- `bios.registry.id`:
  - reference draft: `true`
  - candidate draft: `false`
  - profile.yaml: `true`
  - reference seq: 3
  - reference draft ≠ candidate draft
- `bios.settings_object`:
  - both drafts: `false`
  - profile.yaml: `false`
  - reference seq: 3
- `bios.task_resource`:
  - both drafts: `false`
  - profile.yaml: `false`
- `power.busy_reads.error`:
  - both drafts: `{"message_id":"Base.1.18.ServiceTemporarilyUnavailable","status":503}`
  - profile.yaml: `{"message_id":"Base.1.18.ServiceTemporarilyUnavailable","status":503}`
- `power.busy_reads.failures`:
  - both drafts: `0`
  - profile.yaml: `0`
- `power.busy_reads.resources`:
  - both drafts: `[]`
  - profile.yaml: `[]`
- `power.delay`:
  - both drafts: `{"off":"0s","on":"0s"}`
  - profile.yaml: `{"off":"0s","on":"0s"}`
- `power.not_settled_error`:
  - reference draft: `{"message_id":"Base.1.18.ResourceInStandby","status":400}`
  - candidate draft: `{"message_id":"Vendor.1.0.UnableToModifyDuringSystemPOST","status":400}`
  - profile.yaml: `{"message_id":"Base.1.18.ResourceInStandby","status":400}`
  - candidate seq: 5
  - candidate draft ≠ profile.yaml
  - reference draft ≠ candidate draft
- `power.not_settled_window`:
  - reference draft: `"0s"`
  - candidate draft: `"19s"`
  - profile.yaml: `"0s"`
  - candidate seq: 3, 4, 5
  - candidate draft ≠ profile.yaml
  - reference draft ≠ candidate draft
- `power.redundant_reset_errors.off`:
  - both drafts: `null`
  - profile.yaml: `null`
- `power.redundant_reset_errors.on`:
  - both drafts: `null`
  - profile.yaml: `null`
- `power.reset_types`:
  - reference draft: `["On","ForceOff","GracefulShutdown","ForceRestart"]`
  - candidate draft: `["On","ForceOff"]`
  - profile.yaml: `["On","ForceOff","GracefulShutdown","ForceRestart"]`
  - reference seq: 1
  - candidate seq: 1
  - candidate draft ≠ profile.yaml
  - reference draft ≠ candidate draft
- `power.response`:
  - reference draft: `"204"`
  - candidate draft: `"202-task"`
  - profile.yaml: `"204"`
  - reference seq: 9
  - candidate seq: 3
  - candidate draft ≠ profile.yaml
  - reference draft ≠ candidate draft
- `responses.patch`:
  - both drafts: `"204"`
  - profile.yaml: `"204"`
  - candidate seq: 6
- `secure_boot.certificate_id_prefix`:
  - both drafts: `""`
  - profile.yaml: `""`
- `secure_boot.certificate_id_style`:
  - both drafts: `"hash"`
  - profile.yaml: `"hash"`
  - reference seq: 7
- `secure_boot.database_add_response`:
  - both drafts: `"201"`
  - profile.yaml: `"201"`
  - reference seq: 6
- `secure_boot.database_delete_response`:
  - both drafts: `"204"`
  - profile.yaml: `"204"`
- `secure_boot.database_write`:
  - both drafts: `true`
  - profile.yaml: `true`
  - reference seq: 6
- `secure_boot.database_write_error`:
  - both drafts: `{"message_id":"Base.1.18.ActionNotSupported","status":405}`
  - profile.yaml: `{"message_id":"Base.1.18.ActionNotSupported","status":405}`
- `secure_boot.empty_after_power_on.for`:
  - both drafts: `"0s"`
  - profile.yaml: `"0s"`
- `secure_boot.empty_after_power_on.times`:
  - both drafts: `0`
  - profile.yaml: `0`
  - reference seq: 6
- `secure_boot.enable_requires_power_off`:
  - both drafts: `true`
  - profile.yaml: `true`
- `secure_boot.power_cycle.after`:
  - both drafts: `[]`
  - profile.yaml: `[]`
- `secure_boot.power_cycle.off_after`:
  - both drafts: `"0s"`
  - profile.yaml: `"0s"`
- `secure_boot.power_cycle.off_for`:
  - both drafts: `"0s"`
  - profile.yaml: `"0s"`
- `secure_boot.reset_keys.applies`:
  - both drafts: `"immediately"`
  - profile.yaml: `"immediately"`
- `secure_boot.reset_keys.disables_secure_boot`:
  - both drafts: `false`
  - profile.yaml: `false`
- `secure_boot.reset_keys.response`:
  - both drafts: `"204"`
  - profile.yaml: `"204"`
- `secure_boot.reset_keys.supported`:
  - both drafts: `false`
  - profile.yaml: `false`
  - reference seq: 5
- `secure_boot.reset_keys.types`:
  - both drafts: `["DeleteAllKeys","DeletePK"]`
  - profile.yaml: `["DeleteAllKeys","DeletePK"]`
- `secure_boot.reset_keys.unreachable.after`:
  - both drafts: `"0s"`
  - profile.yaml: `"0s"`
- `secure_boot.reset_keys.unreachable.for`:
  - both drafts: `"0s"`
  - profile.yaml: `"0s"`
- `virtual_media.boot_override_once`:
  - reference draft: `false`
  - candidate draft: `true`
  - profile.yaml: `false`
  - reference seq: 1
  - candidate seq: 1
  - candidate draft ≠ profile.yaml
  - reference draft ≠ candidate draft
- `virtual_media.delivery`:
  - both drafts: `"full-fetch"`
  - profile.yaml: `"full-fetch"`
- `virtual_media.id`:
  - both drafts: `"CD1"`
  - profile.yaml: `"CD1"`
  - reference seq: 8
  - candidate seq: 2
- `virtual_media.insert`:
  - reference draft: `"standard"`
  - candidate draft: `"oem"`
  - profile.yaml: `"standard"`
  - reference seq: 8
  - candidate seq: 2
  - candidate draft ≠ profile.yaml
  - reference draft ≠ candidate draft
- `virtual_media.insert_response`:
  - reference draft: `"204"`
  - candidate draft: `"202-task"`
  - profile.yaml: `"204"`
  - reference seq: 8
  - candidate seq: 2
  - candidate draft ≠ profile.yaml
  - reference draft ≠ candidate draft
- `virtual_media.locations`:
  - both drafts: `["managers"]`
  - profile.yaml: `["managers"]`
- `virtual_media.oem_action`:
  - both drafts: `"InsertVirtualMedia"`
  - profile.yaml: `"InsertVirtualMedia"`
  - candidate seq: 2
- `virtual_media.refused_parameters.error`:
  - both drafts: `{"message_id":"Base.1.18.ActionParameterUnknown","status":400}`
  - profile.yaml: `{"message_id":"Base.1.18.ActionParameterUnknown","status":400}`
- `virtual_media.refused_parameters.names`:
  - both drafts: `[]`
  - profile.yaml: `[]`
  - reference seq: 8

## Timing

Informational only. The VM boots faster than a real server, and the delays of the proxy are settings.

| State                    | Reference | Candidate | Ratio |
| ------------------------ | --------- | --------- | ----- |
| refresh-bmc-data         | 1s        | 1s        | 1.00  |
| check-bios               | 1s        |           |       |
| apply-bios               | 1s        |           |       |
| verify-bios              | 1s        |           |       |
| secure-boot-certificates | 3s        |           |       |
| attach-media             | 1s        | 8s        | 8.00  |
| power-on-install         | 0s        | 25s       |       |
| total                    | 9s        | 35s       | 3.89  |

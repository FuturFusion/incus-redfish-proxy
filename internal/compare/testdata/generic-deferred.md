# Recording comparison

## Summary

- Steps: 3 of 9 states on both sides
- Transitions: 3 of 9 state transitions on both sides
- Operations: 2 of 6 method and kind pairs on both sides
- Errors: 0 of 0 error outcomes of the reference also in the candidate
- Keys: 2 of 6 profile keys equal in both drafts

## Header

|                     | Reference        | Candidate        |
| ------------------- | ---------------- | ---------------- |
| Server model        | Example Server 1 | Example Server 1 |
| BMC firmware        | 1.30.20          | 1.30.20          |
| BIOS                | 2.4.1            | 2.4.1            |
| Operations Center   | 0.11.0           | 0.11.0           |
| Final state         | completed        | completed        |
| Exchanges           | 9                | 8                |
| Truncated exchanges | 0                | 0                |
| Duration            | 9s               | 1m0s             |

## Deployment steps

Steps: 3 of 9 states on both sides
Transitions: 3 of 9 state transitions on both sides

| State                    | Ref | Cand | Flags          |
| ------------------------ | --- | ---- | -------------- |
| refresh-bmc-data         | yes | yes  |                |
| check-bios               | yes | yes  |                |
| apply-bios               | yes | yes  |                |
| verify-bios              | yes | no   | reference only |
| secure-boot-certificates | yes | no   | reference only |
| attach-media             | yes | no   | reference only |
| power-on-install         | yes | no   | reference only |
| power-on-bios            | no  | yes  | candidate only |
| apply-bios-deferred      | no  | yes  | candidate only |

State transitions on one side only:

- reference only: apply-bios → verify-bios
- reference only: verify-bios → secure-boot-certificates
- reference only: secure-boot-certificates → attach-media
- reference only: attach-media → power-on-install
- candidate only: apply-bios → power-on-bios
- candidate only: power-on-bios → apply-bios-deferred

## Operations per step

Operations: 2 of 6 method and kind pairs on both sides

The kind columns count pairs of method and kind. The run columns count runs and exchanges.
Only steps with exchanges on both sides count for the parity.

| State                    | Both | Ref only | Cand only | Ref runs | Cand runs |
| ------------------------ | ---- | -------- | --------- | -------- | --------- |
| refresh-bmc-data         | 1    | 0        | 0         | 1 / 1    | 1 / 1     |
| check-bios               | 1    | 0        | 1         | 1 / 1    | 2 / 2     |
| apply-bios               | 0    | 1        | 2         | 1 / 1    | 3 / 3     |
| verify-bios              | 0    | 1        | 0         | 1 / 1    | 0 / 0     |
| secure-boot-certificates | 0    | 3        | 0         | 3 / 3    | 0 / 0     |
| attach-media             | 0    | 1        | 0         | 1 / 1    | 0 / 0     |
| power-on-install         | 0    | 1        | 0         | 1 / 1    | 0 / 0     |
| power-on-bios            | 0    | 0        | 1         | 0 / 0    | 1 / 1     |
| apply-bios-deferred      | 0    | 0        | 1         | 0 / 0    | 1 / 1     |

Operations on one side only:

- `check-bios`:
  - candidate only: GET /redfish/v1/registries/biosattributeregistry.v1_0_0/biosattributeregistry.v1_0_0.json
- `apply-bios`:
  - reference only: PATCH /redfish/v1/systems/{id}/bios
  - candidate only: GET /redfish/v1/systems/{id}/bios
  - candidate only: PATCH /redfish/v1/systems/{id}/bios/settings
- `verify-bios`:
  - reference only: GET /redfish/v1/systems/{id}/bios
- `secure-boot-certificates`:
  - reference only: GET /redfish/v1/systems/{id}/secureboot
  - reference only: GET /redfish/v1/systems/{id}/secureboot/securebootdatabases/db/certificates
  - reference only: POST /redfish/v1/systems/{id}/secureboot/securebootdatabases/db/certificates
- `attach-media`:
  - reference only: POST VirtualMedia.InsertMedia
- `power-on-install`:
  - reference only: POST ComputerSystem.Reset
- `power-on-bios`:
  - candidate only: POST ComputerSystem.Reset
- `apply-bios-deferred`:
  - candidate only: PATCH /redfish/v1/systems/{id}/bios/settings

## Errors and retries

Errors: 0 of 0 error outcomes of the reference also in the candidate

| State      | Operation                                    | Outcome                  | Ref | Cand | Flags          |
| ---------- | -------------------------------------------- | ------------------------ | --- | ---- | -------------- |
| apply-bios | PATCH /redfish/v1/systems/{id}/bios/settings | 400 Base.PropertyUnknown | 0   | 1    | candidate only |

## Profile keys

Keys: 2 of 6 profile keys equal in both drafts

| Key                                         | Ref      | Cand     | Differences |
| ------------------------------------------- | -------- | -------- | ----------- |
| bios.apply_time.default                     | gap      | observed | ref≠cand    |
| bios.apply_time.supported                   | gap      | inferred | ref≠cand    |
| bios.errors.deferred_rejected               | gap      | observed | ref≠cand    |
| bios.errors.unknown_attribute               | gap      | gap      |             |
| bios.patch_requires_power_off               | gap      | observed | ref≠cand    |
| bios.patch_response                         | observed | observed | ref≠cand    |
| bios.pending                                | observed | observed | ref≠cand    |
| bios.registry.attributes                    | inferred | inferred | ref≠cand    |
| bios.registry.id                            | observed | observed |             |
| bios.settings_object                        | observed | observed | ref≠cand    |
| bios.task_resource                          | gap      | gap      |             |
| power.busy_reads.error                      | gap      | gap      |             |
| power.busy_reads.failures                   | gap      | gap      |             |
| power.busy_reads.resources                  | gap      | gap      |             |
| power.delay                                 | gap      | gap      |             |
| power.not_settled_error                     | gap      | gap      |             |
| power.not_settled_window                    | gap      | gap      |             |
| power.redundant_reset_errors.off            | gap      | gap      |             |
| power.redundant_reset_errors.on             | gap      | gap      |             |
| power.reset_types                           | observed | gap      | ref≠cand    |
| power.response                              | observed | observed |             |
| responses.patch                             | gap      | gap      |             |
| secure_boot.certificate_id_prefix           | gap      | gap      |             |
| secure_boot.certificate_id_style            | observed | gap      |             |
| secure_boot.database_add_response           | observed | gap      |             |
| secure_boot.database_delete_response        | gap      | gap      |             |
| secure_boot.database_write                  | observed | gap      |             |
| secure_boot.database_write_error            | gap      | gap      |             |
| secure_boot.empty_after_power_on.for        | gap      | gap      |             |
| secure_boot.empty_after_power_on.times      | observed | gap      |             |
| secure_boot.enable_requires_power_off       | gap      | gap      |             |
| secure_boot.power_cycle.after               | gap      | gap      |             |
| secure_boot.power_cycle.off_after           | gap      | gap      |             |
| secure_boot.power_cycle.off_for             | gap      | gap      |             |
| secure_boot.reset_keys.applies              | gap      | gap      |             |
| secure_boot.reset_keys.disables_secure_boot | gap      | gap      |             |
| secure_boot.reset_keys.response             | gap      | gap      |             |
| secure_boot.reset_keys.supported            | observed | gap      |             |
| secure_boot.reset_keys.types                | gap      | gap      |             |
| secure_boot.reset_keys.unreachable.after    | gap      | gap      |             |
| secure_boot.reset_keys.unreachable.for      | gap      | gap      |             |
| virtual_media.boot_override_once            | observed | gap      |             |
| virtual_media.delivery                      | gap      | gap      |             |
| virtual_media.id                            | observed | gap      | ref≠cand    |
| virtual_media.insert                        | observed | gap      |             |
| virtual_media.insert_response               | observed | gap      |             |
| virtual_media.locations                     | gap      | gap      |             |
| virtual_media.oem_action                    | fixed    | gap      |             |
| virtual_media.refused_parameters.error      | gap      | gap      |             |
| virtual_media.refused_parameters.names      | observed | gap      |             |

Values and sequence numbers of the keys:

- `bios.apply_time.default`:
  - reference draft: `"Immediate"`
  - candidate draft: `"OnReset"`
  - candidate seq: 4
  - reference draft ≠ candidate draft
- `bios.apply_time.supported`:
  - reference draft: `[]`
  - candidate draft: `["OnReset","Immediate"]`
  - candidate seq: 2
  - reference draft ≠ candidate draft
- `bios.errors.deferred_rejected`:
  - reference draft: `{"message_id":"Base.1.18.PropertyUnknown","status":400}`
  - candidate draft: `{"message_id":"Base.1.12.PropertyUnknown","status":400}`
  - candidate seq: 6
  - reference draft ≠ candidate draft
- `bios.errors.unknown_attribute`:
  - both drafts: `{"message_id":"Base.1.18.PropertyUnknown","status":400}`
- `bios.patch_requires_power_off`:
  - reference draft: `true`
  - candidate draft: `false`
  - candidate seq: 1, 4
  - reference draft ≠ candidate draft
- `bios.patch_response`:
  - reference draft: `"204"`
  - candidate draft: `"202-taskmonitor"`
  - reference seq: 3
  - candidate seq: 4
  - reference draft ≠ candidate draft
- `bios.pending`:
  - reference draft: `false`
  - candidate draft: `true`
  - reference seq: 3, 4
  - candidate seq: 4, 5
  - reference draft ≠ candidate draft
- `bios.registry.attributes`:
  - reference draft: `["Boolean","Integer","String"]`
  - candidate draft: `["Enumeration","Enumeration","Enumeration requires","Integer"]`
  - reference seq: 3
  - candidate seq: 3, 4, 8
  - reference draft ≠ candidate draft
- `bios.registry.id`:
  - both drafts: `true`
  - reference seq: 3
  - candidate seq: 3, 4, 8
- `bios.settings_object`:
  - reference draft: `false`
  - candidate draft: `true`
  - reference seq: 3
  - candidate seq: 4
  - reference draft ≠ candidate draft
- `bios.task_resource`:
  - both drafts: `false`
- `power.busy_reads.error`:
  - both drafts: `{"message_id":"Base.1.18.ServiceTemporarilyUnavailable","status":503}`
- `power.busy_reads.failures`:
  - both drafts: `0`
- `power.busy_reads.resources`:
  - both drafts: `[]`
- `power.delay`:
  - both drafts: `{"off":"0s","on":"0s"}`
- `power.not_settled_error`:
  - both drafts: `{"message_id":"Base.1.18.ResourceInStandby","status":400}`
- `power.not_settled_window`:
  - both drafts: `"0s"`
- `power.redundant_reset_errors.off`:
  - both drafts: `null`
- `power.redundant_reset_errors.on`:
  - both drafts: `null`
- `power.reset_types`:
  - reference draft: `["On","ForceOff","GracefulShutdown","ForceRestart"]`
  - candidate draft: `[]`
  - reference seq: 1
  - reference draft ≠ candidate draft
- `power.response`:
  - both drafts: `"204"`
  - reference seq: 9
  - candidate seq: 7
- `responses.patch`:
  - both drafts: `"204"`
- `secure_boot.certificate_id_prefix`:
  - both drafts: `""`
- `secure_boot.certificate_id_style`:
  - both drafts: `"hash"`
  - reference seq: 7
- `secure_boot.database_add_response`:
  - both drafts: `"201"`
  - reference seq: 6
- `secure_boot.database_delete_response`:
  - both drafts: `"204"`
- `secure_boot.database_write`:
  - both drafts: `true`
  - reference seq: 6
- `secure_boot.database_write_error`:
  - both drafts: `{"message_id":"Base.1.18.ActionNotSupported","status":405}`
- `secure_boot.empty_after_power_on.for`:
  - both drafts: `"0s"`
- `secure_boot.empty_after_power_on.times`:
  - both drafts: `0`
  - reference seq: 6
- `secure_boot.enable_requires_power_off`:
  - both drafts: `true`
- `secure_boot.power_cycle.after`:
  - both drafts: `[]`
- `secure_boot.power_cycle.off_after`:
  - both drafts: `"0s"`
- `secure_boot.power_cycle.off_for`:
  - both drafts: `"0s"`
- `secure_boot.reset_keys.applies`:
  - both drafts: `"immediately"`
- `secure_boot.reset_keys.disables_secure_boot`:
  - both drafts: `false`
- `secure_boot.reset_keys.response`:
  - both drafts: `"204"`
- `secure_boot.reset_keys.supported`:
  - both drafts: `false`
  - reference seq: 5
- `secure_boot.reset_keys.types`:
  - both drafts: `["DeleteAllKeys","DeletePK"]`
- `secure_boot.reset_keys.unreachable.after`:
  - both drafts: `"0s"`
- `secure_boot.reset_keys.unreachable.for`:
  - both drafts: `"0s"`
- `virtual_media.boot_override_once`:
  - both drafts: `false`
  - reference seq: 1
- `virtual_media.delivery`:
  - both drafts: `"full-fetch"`
- `virtual_media.id`:
  - reference draft: `"CD1"`
  - candidate draft: `"CD"`
  - reference seq: 8
  - reference draft ≠ candidate draft
- `virtual_media.insert`:
  - both drafts: `"standard"`
  - reference seq: 8
- `virtual_media.insert_response`:
  - both drafts: `"204"`
  - reference seq: 8
- `virtual_media.locations`:
  - both drafts: `["managers"]`
- `virtual_media.oem_action`:
  - both drafts: `"InsertVirtualMedia"`
- `virtual_media.refused_parameters.error`:
  - both drafts: `{"message_id":"Base.1.18.ActionParameterUnknown","status":400}`
- `virtual_media.refused_parameters.names`:
  - both drafts: `[]`
  - reference seq: 8

## Timing

Informational only. The VM boots faster than a real server, and the delays of the proxy are settings.

| State                    | Reference | Candidate | Ratio |
| ------------------------ | --------- | --------- | ----- |
| refresh-bmc-data         | 1s        | 1s        | 1.00  |
| check-bios               | 1s        | 2s        | 2.00  |
| apply-bios               | 1s        | 3s        | 3.00  |
| verify-bios              | 1s        |           |       |
| secure-boot-certificates | 3s        |           |       |
| attach-media             | 1s        |           |       |
| power-on-install         | 0s        |           |       |
| power-on-bios            |           | 53s       |       |
| apply-bios-deferred      |           | 0s        |       |
| total                    | 9s        | 1m0s      | 6.67  |

# dell-idrac9

- Hardware: Dell PowerEdge R740xd, BMC firmware iDRAC 9 7.00.00.184, BIOS 2.27.0
- Recorded: 2026-11-09 with Operations Center 0.10.1, by a maintainer
- Recording: recording.jsonl.gz (anonymized), manifest.json
- Unverified values: `bios.task_resource`, `bios.patch_requires_power_off`,
  `bios.errors`, the types of the synthetic BIOS attributes,
  `secure_boot.database_write_error`, `secure_boot.reset_keys.response`,
  `secure_boot.reset_keys.applies`, `secure_boot.enable_requires_power_off`

BIOS settings stay pending in `Bios/Settings` until the next reset. One
attribute only exists once another attribute is `On` in effect. The synthetic
attributes stand for the vendor attributes of the recording. Certificates are
written over Redfish. A new certificate is answered with 204, a deletion with
200 and the messages inside an `error` object. The IDs are numbered with one
counter for all databases. A ForceOff of a server that is off fails with 409
and `IDRAC.2.8.PSU502`. `InsertMedia` refuses the parameter `MediaType` with
400 and `Base.1.12.ActionParameterUnknown`. The virtual media shows below
`Systems` and below `Managers`.

After a power on, the BMC answers some reads of `Systems/<id>`, `Bios` and
`SecureBoot` with 503 and `IDRAC.2.8.SYS518`. Operations Center asks again,
and a retry succeeds after up to 3 refused reads. The profile refuses the
first 3 reads of each of these resources after every power on. The recording
shows the refusals minutes after a power on, and also after a reboot of the
operating system.

After the first two power ons that follow a write to the key databases, the
certificate and signature collections read empty for several minutes. The
check of Operations Center after the settle boot reads them empty, so it runs
the certificate step again. The third power on reads the certificates. The
profile reads the collections empty for 10 minutes after the first power on
after a write only. One failed check covers the retry of Operations Center. A
second round would add about 7 minutes to the end to end test, but no new
code path.

Not modeled by the proxy:

- After a power on, `Bios` shows only part of its attributes for a while.
- After a reboot, the BMC numbers the certificates again without gaps.
- The member IDs of the virtual media. The BMC shows `1` below `Systems` and
  `CD` below `Managers`, the profile shows `CD` at both.

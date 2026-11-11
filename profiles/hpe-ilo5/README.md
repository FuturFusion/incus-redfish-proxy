# hpe-ilo5

- Hardware: HPE ProLiant DL385 Gen10, BMC firmware iLO 5 v3.20, BIOS A40 v3.70 (01/09/2026)
- Recorded: 2026-11-09 with Operations Center 0.10.1, by a maintainer
- Recording: recording.jsonl.gz (anonymized), manifest.json
- Unverified values: every `bios` key except `settings_object` and `pending`,
  `secure_boot.enable_requires_power_off`, `virtual_media.delivery`. Operations
  Center sent no BIOS PATCH and enrolled the keys from media.

The BMC answers most successful writes with 200 and the messages inside an
`error` object. A reset of the secure boot keys takes effect on the next power
on. The BMC publishes no key databases, so the overlay removes
`SecureBootDatabases`. A ForceOff of a server that is off fails with 400 and
`iLO.2.25.InvalidOperationForSystemState`. `InsertMedia` refuses the
parameter `MediaType` with 400 and `Base.1.18.ActionParameterUnknown`.

The reset of the keys also switches `SecureBootEnable` off, so Operations
Center enables secure boot again. About 2 minutes after the power on that
applies the reset of the keys or the enabled secure boot, the server powers
itself off, and on again about 20 seconds later.

Not modeled by the proxy:

- The power state is `Reset` for about 10 seconds after a power on.
- The OEM post state of the server.

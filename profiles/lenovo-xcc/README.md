# lenovo-xcc

- Hardware: Lenovo ThinkSystem SR630 V2, BMC firmware XClarity Controller AFBT58B 5.70 2025-08-11, BIOS AFE128F
- Recorded: 2026-11-05 with Operations Center 0.10.1, by a maintainer
- Recording: recording.jsonl.gz (anonymized), manifest.json
- Unverified values: `bios.patch_response`, `bios.task_resource`,
  `bios.patch_requires_power_off`, `bios.errors`,
  `secure_boot.enable_requires_power_off`, `secure_boot.reset_keys.types`.
  Operations Center sent no BIOS PATCH and enrolled the keys from media.

Virtual media is inserted and ejected by a PATCH of the resource. The BMC
answers a PATCH with the changed resource. The device shows below `Systems`
and below `Managers`. Operations Center inserted through `Systems` and ejected
through `Managers`. A reset of the secure boot keys
takes effect on the next power on. The BMC publishes no key databases, so the
overlay removes `SecureBootDatabases`.

A power off waits 10 seconds, which the recording does not show. Without the
delay, a ForceOff can stop the instance while OVMF writes a variable. Incus then
fails to read the NVRAM of the instance.

About 1m30s after the power on that follows a key reset, the BMC answers no
request for a while. Operations Center only sees its own timeouts. A request
that arrives in the window of the profile gets no answer. The window lasts 20
seconds, which catches one request. Each request in the window costs the end
to end test up to 1 minute, so a longer window adds time, but no new code
path.

Not modeled by the proxy:

- The full length of the window without answers. The BMC answered no request
  for at least 1m41s, the profile for 20 seconds.
- The member ID `EXT1` of the virtual media. The profile uses `CD`.
- One insert PATCH took 49 seconds.

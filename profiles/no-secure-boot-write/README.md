# no-secure-boot-write

- Hardware: synthetic, not derived from hardware
- Recorded: no
- Recording: none
- Unverified values: `secure_boot.database_write_error`

The secure boot databases are read only over Redfish. A client can only reset
the keys with `#SecureBoot.ResetKeys`. Operations Center enrolls the certificates from
the guest instead.

The reset of the keys takes effect on the next reset that powers the system
on, like on real hardware. Until then, `SecureBootMode` stays `UserMode`.

The reset, the reset of the keys and the PATCHes answer 200 with a body, in
the shapes that the recorded BMCs use.

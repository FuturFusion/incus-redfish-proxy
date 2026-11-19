# no-secure-boot-write

- Hardware: synthetic, not derived from hardware
- Recorded: no
- Recording: none
- Unverified values: `secure_boot.database_write_error`

The secure boot databases are read only over Redfish. A client can only reset
the keys with `#SecureBoot.ResetKeys`. Operations Center enrolls the certificates from
the guest instead.

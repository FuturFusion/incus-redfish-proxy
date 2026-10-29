# deferred-bios

- Hardware: synthetic, not derived from hardware
- Recorded: no
- Recording: none
- Unverified values: `bios.apply_time.supported`

The proxy stages BIOS settings in `Bios/Settings` and applies them on the next
reset. It hides and refuses the attribute `ModeOption` until `Mode` is
`Performance` in effect. This drives the deferred BIOS pass of Operations Center.

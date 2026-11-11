# stream-media

- Hardware: synthetic, not derived from hardware
- Recorded: no
- Recording: none
- Unverified values: none

The proxy streams virtual media with range requests through a FUSE
(Filesystem in Userspace) mount. If the
image server does not support range requests, the insert fails instead of
falling back to a full download. See the main README for the requirements.

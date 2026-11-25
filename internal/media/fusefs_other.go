//go:build !linux

package media

// MountOptions configure a Mount.
type MountOptions struct {
	// AllowOther lets other users read the file. QEMU needs it, as it runs as an unprivileged user.
	AllowOther bool
}

// Mount is a fetcher exposed as a single read only file. Only Linux supports it.
type Mount struct{}

// NewMount returns ErrNotSupported, FUSE mounts are only implemented for Linux.
func NewMount(dir string, name string, fetcher *Fetcher, opts MountOptions) (*Mount, error) {
	return nil, ErrNotSupported
}

// Path returns the path of the file.
func (m *Mount) Path() string {
	return ""
}

// Unmount removes the mount.
func (m *Mount) Unmount() error {
	return nil
}

// Cleanup detaches a mount that a previous process left in dir.
func Cleanup(dir string) error {
	return nil
}

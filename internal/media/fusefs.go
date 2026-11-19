//go:build linux

package media

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"golang.org/x/sys/unix"
)

// MountOptions configure a Mount.
type MountOptions struct {
	// AllowOther lets other users read the file. QEMU needs it, as it runs as an unprivileged user.
	AllowOther bool
}

// Mount is a fetcher exposed as a single read only file.
type Mount struct {
	dir    string
	path   string
	server *fuse.Server
}

// NewMount exposes the fetcher as the file name in the directory dir, which becomes a FUSE mount point.
func NewMount(dir string, name string, fetcher *Fetcher, opts MountOptions) (*Mount, error) {
	err := os.MkdirAll(dir, 0o755)
	if err != nil {
		return nil, err
	}

	name = filepath.Base(name)
	root := &rootNode{name: name, file: &imageFile{fetcher: fetcher}}

	server, err := fs.Mount(dir, root, &fs.Options{
		MountOptions: fuse.MountOptions{
			AllowOther:  opts.AllowOther,
			FsName:      "incus-redfish-proxy",
			Name:        "media",
			Options:     []string{"ro"},
			DirectMount: true,
		},
	})
	if err != nil {
		return nil, err
	}

	return &Mount{dir: dir, path: filepath.Join(dir, name), server: server}, nil
}

// Path returns the path of the file.
func (m *Mount) Path() string {
	return m.path
}

// Unmount removes the mount. A busy mount is detached and goes away once its last reader closes the file.
func (m *Mount) Unmount() error {
	err := m.server.Unmount()
	if err == nil {
		return nil
	}

	return lazyUnmount(m.dir)
}

// Cleanup detaches a mount that a previous process left in dir.
func Cleanup(dir string) error {
	mounted, err := isMountPoint(dir)
	if err != nil || !mounted {
		return err
	}

	return lazyUnmount(dir)
}

func lazyUnmount(dir string) error {
	err := unix.Unmount(dir, unix.MNT_DETACH)
	if !errors.Is(err, unix.EPERM) {
		return err
	}

	// A user without the right to unmount relies on the setuid helper of FUSE.
	output, err := exec.Command("fusermount3", "-u", "-z", dir).CombinedOutput()
	if err != nil {
		return errors.Join(err, errors.New(strings.TrimSpace(string(output))))
	}

	return nil
}

func isMountPoint(dir string) (bool, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return false, err
	}

	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return false, err
	}

	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) > 4 && fields[4] == dir {
			return true, nil
		}
	}

	return false, scanner.Err()
}

type rootNode struct {
	fs.Inode

	name string
	file *imageFile
}

var _ fs.NodeOnAdder = (*rootNode)(nil)

func (r *rootNode) OnAdd(ctx context.Context) {
	child := r.NewPersistentInode(ctx, r.file, fs.StableAttr{Mode: fuse.S_IFREG})
	r.AddChild(r.name, child, false)
}

type imageFile struct {
	fs.Inode

	fetcher *Fetcher
}

var (
	_ fs.NodeOpener    = (*imageFile)(nil)
	_ fs.NodeReader    = (*imageFile)(nil)
	_ fs.NodeGetattrer = (*imageFile)(nil)
)

func (f *imageFile) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFREG | 0o444
	out.Size = uint64(f.fetcher.Size())

	return 0
}

func (f *imageFile) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	if flags&(syscall.O_WRONLY|syscall.O_RDWR) != 0 {
		return nil, 0, syscall.EROFS
	}

	// Incus probes with O_DIRECT and uses the page cache, if the open fails.
	if flags&syscall.O_DIRECT != 0 {
		return nil, 0, syscall.EINVAL
	}

	return nil, fuse.FOPEN_KEEP_CACHE, 0
}

func (f *imageFile) Read(ctx context.Context, fh fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	n, err := f.fetcher.ReadAt(dest, off)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, syscall.EIO
	}

	return fuse.ReadResultData(dest[:n]), 0
}

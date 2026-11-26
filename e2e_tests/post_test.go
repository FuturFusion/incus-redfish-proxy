//go:build e2e

package e2e_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	incus "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/stretchr/testify/require"
)

// The POST duration of testdata/profiles/e2e-post-duration.yaml.
const postDuration = 30 * time.Second

// GRUB of the Alpine image shows this header in its menu and on its command line.
const grubHeader = "GNU GRUB"

// console is the text console of a VM. It records what the VM writes and sends keys to the VM.
type console struct {
	mu     sync.Mutex
	output bytes.Buffer
	keys   *io.PipeReader
	typing *io.PipeWriter
}

func (c *console) Read(p []byte) (int, error) {
	return c.keys.Read(p)
}

func (c *console) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.output.Write(p)
}

func (c *console) Close() error {
	return c.keys.Close()
}

func attachConsole(t *testing.T, vm string) *console {
	t.Helper()

	keys, typing := io.Pipe()
	c := &console{keys: keys, typing: typing}
	disconnect := make(chan bool)

	op, err := env.client.ConsoleInstance(vm, api.InstanceConsolePost{Type: "console", Width: 80, Height: 25}, &incus.InstanceConsoleArgs{
		Terminal: c,
		Control: func(conn *websocket.Conn) {
			for {
				_, _, err := conn.ReadMessage()
				if err != nil {
					return
				}
			}
		},
		ConsoleDisconnect: disconnect,
	})
	require.NoError(t, err, "attach to the console of the VM %s", vm)

	t.Cleanup(func() {
		close(disconnect)
		_ = typing.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		_ = op.WaitContext(ctx)
	})

	return c
}

func (c *console) length() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.output.Len()
}

// waitFor returns the position after the first text that the VM writes after the position from.
func (c *console) waitFor(t *testing.T, from int, text string, d time.Duration) int {
	t.Helper()

	deadline := time.Now().Add(timeout(d))

	for {
		c.mu.Lock()
		output := c.output.String()
		c.mu.Unlock()

		index := strings.Index(output[from:], text)
		if index >= 0 {
			return from + index + len(text)
		}

		require.False(t, time.Now().After(deadline), "the console shows %q in time, it shows:\n%s", text, output[from:])

		// GRUB waits only 1s for a key, so poll faster than the other tests.
		time.Sleep(50 * time.Millisecond)
	}
}

func (c *console) send(t *testing.T, keys string) {
	t.Helper()

	_, err := c.typing.Write([]byte(keys))
	require.NoError(t, err, "send %q to the console", keys)
}

func instancePid(t *testing.T, name string) int64 {
	t.Helper()

	state, _, err := env.client.GetInstanceState(name)
	require.NoError(t, err, "read the state of the VM %s", name)

	return state.Pid
}

func TestPower_POSTDuration(t *testing.T) {
	t.Parallel()

	// Setup
	vm := newVM(t)
	p := startProxy(t, vm, "testdata/profiles/e2e-post-duration.yaml")
	images := newImageServer(t, "alpine.iso", alpineImage(t))

	resp := p.insert(t, images.url)
	require.Equal(t, http.StatusNoContent, resp.status, "insert: %s", resp.body)

	resp = p.setBootOverride(t, "Continuous")
	require.Equal(t, http.StatusNoContent, resp.status, "boot from the media: %s", resp.body)

	// Run test: power on.
	poweredOn := time.Now()

	resp = p.reset(t, "On")
	require.Equal(t, http.StatusNoContent, resp.status, "power on: %s", resp.body)

	c := attachConsole(t, vm)
	menu := c.waitFor(t, 0, grubHeader, 3*time.Minute)
	booted := time.Now()

	// Assert
	require.Contains(t, instanceConfig(t, vm)["raw.qemu.conf"], "splash-time = \"30000\"", "the proxy writes the POST duration before the start")
	require.GreaterOrEqual(t, booted.Sub(poweredOn), postDuration, "the firmware boots after the POST duration")

	// Run test: reset from inside the VM. GRUB of the Alpine image has no reboot command.
	// So boot Linux with a console on the serial port and reboot it.
	c.send(t, "c")
	prompt := c.waitFor(t, menu, "grub>", time.Minute)

	c.send(t, "linux /boot/vmlinuz-virt modules=loop,squashfs,sd-mod,usb-storage console=ttyS0,115200\r")
	prompt = c.waitFor(t, prompt, "grub>", time.Minute)

	c.send(t, "initrd /boot/initramfs-virt\r")
	prompt = c.waitFor(t, prompt, "grub>", time.Minute)

	c.send(t, "boot\r")
	login := c.waitFor(t, prompt, "login:", 3*time.Minute)

	// The live system of Alpine has no root password.
	c.send(t, "root\r")
	c.waitFor(t, login, ":~# ", time.Minute)
	pid := instancePid(t, vm)

	// The command line shows the header too, so only output after the reboot counts.
	reboot := c.length()
	rebooted := time.Now()

	c.send(t, "reboot\r")
	c.waitFor(t, reboot, grubHeader, 3*time.Minute)
	bootedAgain := time.Now()

	// Assert
	require.Equal(t, pid, instancePid(t, vm), "the reset from inside the VM keeps the QEMU process")
	require.GreaterOrEqual(t, bootedAgain.Sub(rebooted), postDuration, "the firmware boots after the POST duration after a reset from inside the VM")

	// Run test: a profile without POST duration removes it at the next start.
	resp = p.reset(t, "ForceOff")
	require.Equal(t, http.StatusNoContent, resp.status, "power off: %s", resp.body)

	p.close(t)

	generic := startProxy(t, vm, "generic")
	resp = generic.reset(t, "On")

	// Assert
	require.Equal(t, http.StatusNoContent, resp.status, "power on with the generic profile: %s", resp.body)
	require.NotContains(t, instanceConfig(t, vm), "raw.qemu.conf", "the generic profile removes the POST duration")
}

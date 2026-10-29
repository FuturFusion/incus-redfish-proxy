//go:build e2e

// Package e2e_test runs the proxy against a real Incus server with KVM.
package e2e_test

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	incus "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/cliconfig"
)

// requiredExtensions are the Incus API extensions the proxy uses.
var requiredExtensions = []string{"projects", "instances", "custom_volume_iso", "instance_nvram"}

// env is the environment shared by all tests.
var env struct {
	client        incus.InstanceServer
	remote        string
	project       string
	pool          string
	mediaDir      string
	logDir        string
	keep          bool
	timeoutFactor float64
}

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	err := setup()
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e preconditions are not met:\n%v\n", err)

		return 1
	}

	code := m.Run()

	if env.keep {
		fmt.Fprintf(os.Stderr, "keeping the project %q, IRP_E2E_KEEP is set\n", env.project)

		return code
	}

	err = deleteProject()
	if err != nil {
		fmt.Fprintf(os.Stderr, "delete project %q: %v\n", env.project, err)

		return max(code, 1)
	}

	return code
}

// setup checks every precondition and reports all missing ones at once.
func setup() error {
	env.remote = os.Getenv("IRP_E2E_REMOTE")
	env.pool = getenv("IRP_E2E_POOL", "default")
	env.keep = os.Getenv("IRP_E2E_KEEP") != ""
	env.logDir = getenv("IRP_E2E_LOG_DIR", "logs")
	env.timeoutFactor = 1

	var problems []error

	factor := os.Getenv("IRP_E2E_TIMEOUT_FACTOR")
	if factor != "" {
		value, err := strconv.ParseFloat(factor, 64)
		if err != nil || value <= 0 {
			problems = append(problems, fmt.Errorf("IRP_E2E_TIMEOUT_FACTOR %q is not a positive number", factor))
		}

		env.timeoutFactor = max(value, 1)
	}

	if runtime.GOOS != "linux" {
		return fmt.Errorf("- the tests need Linux, not %s", runtime.GOOS)
	}

	problems = append(problems, checkDevice("/dev/kvm", "KVM is needed to run Incus VMs"))
	problems = append(problems, checkFUSE()...)

	home, err := os.UserHomeDir()
	if err != nil {
		problems = append(problems, err)
	}

	env.mediaDir = getenv("IRP_E2E_MEDIA_DIR", filepath.Join(home, ".cache", "irp-e2e", "media"))

	err = os.MkdirAll(env.mediaDir, 0o755)
	if err != nil {
		problems = append(problems, fmt.Errorf("media dir: %w", err))
	}

	err = os.MkdirAll(env.logDir, 0o755)
	if err != nil {
		problems = append(problems, fmt.Errorf("log dir: %w", err))
	}

	problems = append(problems, connectIncus()...)

	err = errors.Join(problems...)
	if err != nil {
		return err
	}

	err = createProject()
	if err != nil {
		return fmt.Errorf("- create the test project: %w", err)
	}

	logFile, err := os.Create(filepath.Join(env.logDir, "proxy.log"))
	if err != nil {
		return err
	}

	// The proxy logs through the standard logger.
	log.SetOutput(io.MultiWriter(os.Stderr, logFile))

	return nil
}

func getenv(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	return value
}

func checkDevice(path string, reason string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("- %s: %w", reason, err)
	}

	return f.Close()
}

func checkFUSE() []error {
	problems := []error{checkDevice("/dev/fuse", "FUSE is needed for streamed media")}

	_, err := exec.LookPath("fusermount3")
	if err != nil {
		problems = append(problems, errors.New("- fusermount3 is missing, install the package fuse3"))
	}

	if os.Geteuid() == 0 {
		return problems
	}

	allowed, err := fuseAllowsOther()
	if err != nil || !allowed {
		problems = append(problems, errors.New("- /etc/fuse.conf must contain user_allow_other, so QEMU can read the mounts of a non root user"))
	}

	return problems
}

func fuseAllowsOther() (bool, error) {
	f, err := os.Open("/etc/fuse.conf")
	if err != nil {
		return false, err
	}

	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "user_allow_other" {
			return true, nil
		}
	}

	return false, scanner.Err()
}

func connectIncus() []error {
	cfg, err := cliconfig.LoadConfig("")
	if err != nil {
		return []error{fmt.Errorf("- load the Incus client config: %w", err)}
	}

	if env.remote == "" {
		env.remote = cfg.DefaultRemote
	}

	client, err := cfg.GetInstanceServer(env.remote)
	if err != nil {
		return []error{fmt.Errorf("- connect to the Incus remote %q, the user needs the group incus-admin or a trusted remote in IRP_E2E_REMOTE: %w", env.remote, err)}
	}

	env.client = client

	var problems []error

	server, _, err := client.GetServer()
	if err != nil {
		return []error{fmt.Errorf("- read the Incus server: %w", err)}
	}

	if server.Auth != "trusted" {
		problems = append(problems, fmt.Errorf("- the Incus remote %q does not trust this client", env.remote))
	}

	for _, extension := range requiredExtensions {
		if !slices.Contains(server.APIExtensions, extension) {
			problems = append(problems, fmt.Errorf("- the Incus server %s lacks the API extension %q, install a current stable release", server.Environment.ServerVersion, extension))
		}
	}

	_, _, err = client.GetStoragePool("default")
	if err != nil {
		problems = append(problems, fmt.Errorf("- the storage pool default is missing, the full fetch of virtual media imports into it: %w", err))
	}

	if env.pool != "default" {
		_, _, err = client.GetStoragePool(env.pool)
		if err != nil {
			problems = append(problems, fmt.Errorf("- the storage pool %q of IRP_E2E_POOL is missing: %w", env.pool, err))
		}
	}

	return problems
}

// createProject keeps the test instances apart. Images and volumes stay in the default project, as the proxy imports into the pool default.
func createProject() error {
	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)

	env.project = "irp-e2e-" + hex.EncodeToString(suffix)

	err := env.client.CreateProject(api.ProjectsPost{
		Name: env.project,
		ProjectPut: api.ProjectPut{
			Description: "incus-redfish-proxy end to end tests",
			Config: map[string]string{
				"features.images":          "false",
				"features.storage.volumes": "false",
			},
		},
	})
	if err != nil {
		return err
	}

	env.client = env.client.UseProject(env.project)

	return nil
}

func deleteProject() error {
	names, err := env.client.GetInstanceNames(api.InstanceTypeAny)
	if err != nil {
		return err
	}

	for _, name := range names {
		deleteInstance(name)
	}

	return env.client.DeleteProject(env.project)
}

// timeout stretches a timeout for slow machines.
func timeout(d time.Duration) time.Duration {
	return time.Duration(float64(d) * env.timeoutFactor)
}

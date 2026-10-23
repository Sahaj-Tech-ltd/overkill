// Package localdb owns a private, bundled PostgreSQL cluster for one application
// session. It never discovers, initializes or stops a system PostgreSQL service.
package localdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/atomicfile"
	"github.com/Sahaj-Tech-ltd/overkill/internal/processrunner"
)

var ErrInUse = errors.New("localdb: this private cluster already has an application owner")

type Config struct {
	BinDir          string
	DataDir         string
	SocketDir       string // Empty disables Unix sockets; TCP is always loopback-only.
	StartupTimeout  time.Duration
	ShutdownTimeout time.Duration
}

type ConnectionInfo struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Database string `json:"database"`
	User     string `json:"user"`
	DataDir  string `json:"data_dir"`
	password string
}

func (info ConnectionInfo) DSN() string { return info.dsn(info.Database) }
func (info ConnectionInfo) dsn(database string) string {
	value := url.URL{Scheme: "postgres", User: url.UserPassword(info.User, info.password), Host: net.JoinHostPort(info.Host, strconv.Itoa(info.Port)), Path: "/" + database}
	value.RawQuery = "connect_timeout=2&sslmode=disable"
	return value.String()
}
func (info ConnectionInfo) String() string {
	return fmt.Sprintf("localdb{host:%s port:%d database:%s data_dir:%q credentials:[redacted]}", info.Host, info.Port, info.Database, info.DataDir)
}
func (info ConnectionInfo) GoString() string { return info.String() }

type metadata struct {
	Format    int    `json:"format"`
	Major     int    `json:"major"`
	ClusterID string `json:"cluster_id"`
	Password  string `json:"password"`
	Phase     string `json:"phase"`
	Port      int    `json:"port"`
	PID       int    `json:"pid"`
	Started   int64  `json:"started"`
}

type Manager struct {
	config Config
	mu     sync.Mutex
	run    *running
}

type running struct {
	owner       *lease
	info        ConnectionInfo
	cancel      context.CancelFunc
	exited      chan struct{}
	err         error // Read only after exited closes.
	diagnostics *boundedOutput
}

type lease struct {
	file *os.File
	once sync.Once
}

func (owner *lease) release() {
	owner.once.Do(func() { _ = unlockFile(owner.file); _ = owner.file.Close() })
}

type boundedOutput struct {
	mu   sync.Mutex
	data []byte
}

func (output *boundedOutput) Write(data []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	output.data = append(output.data, data...)
	if len(output.data) > 64*1024 {
		output.data = append([]byte(nil), output.data[len(output.data)-64*1024:]...)
	}
	return len(data), nil
}
func (output *boundedOutput) String() string {
	output.mu.Lock()
	defer output.mu.Unlock()
	return string(output.data)
}

func NewManager(config Config) (*Manager, error) {
	if !filepath.IsAbs(config.BinDir) || !filepath.IsAbs(config.DataDir) {
		return nil, fmt.Errorf("localdb: explicit absolute binary and cluster paths are required")
	}
	if config.StartupTimeout < 0 || config.ShutdownTimeout < 0 {
		return nil, fmt.Errorf("localdb: timeouts must be positive")
	}
	if config.StartupTimeout == 0 {
		config.StartupTimeout = 30 * time.Second
	}
	if config.ShutdownTimeout == 0 {
		config.ShutdownTimeout = 10 * time.Second
	}
	bin, err := filepath.EvalSymlinks(config.BinDir)
	if err != nil {
		return nil, fmt.Errorf("localdb: bundled binary directory is unavailable: %w", err)
	}
	config.BinDir = bin
	for _, name := range []string{"postgres", "initdb", "pg_ctl"} {
		info, err := os.Stat(binary(config.BinDir, name))
		if err != nil || info.IsDir() {
			return nil, fmt.Errorf("localdb: bundled %s executable is unavailable", name)
		}
	}
	parent := filepath.Dir(filepath.Clean(config.DataDir))
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return nil, fmt.Errorf("localdb: create private parent: %w", err)
	}
	parent, err = filepath.EvalSymlinks(parent)
	if err != nil {
		return nil, err
	}
	config.DataDir = filepath.Join(parent, filepath.Base(filepath.Clean(config.DataDir)))
	if config.DataDir == parent || filepath.Base(config.DataDir) == "." {
		return nil, fmt.Errorf("localdb: cluster must be a private child directory")
	}
	if err := rejectSymlink(config.DataDir); err != nil {
		return nil, err
	}
	if config.SocketDir != "" {
		if !filepath.IsAbs(config.SocketDir) {
			return nil, fmt.Errorf("localdb: socket path must be absolute")
		}
		if err := os.MkdirAll(config.SocketDir, 0o700); err != nil {
			return nil, err
		}
		config.SocketDir, err = filepath.EvalSymlinks(config.SocketDir)
		if err != nil {
			return nil, err
		}
		if len(filepath.Join(config.SocketDir, ".s.PGSQL.65535")) > 100 {
			return nil, fmt.Errorf("localdb: socket path exceeds portable Unix socket length; use TCP-only mode")
		}
		info, err := os.Stat(config.SocketDir)
		if err != nil {
			return nil, err
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
			return nil, fmt.Errorf("localdb: socket directory must be private (0700)")
		}
	}
	return &Manager{config: config}, nil
}
func binary(dir, name string) string {
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(dir, name)
}
func rejectSymlink(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("localdb: managed path must not be a symlink: %s", path)
	}
	return nil
}
func (m *Manager) metadataPath() string { return m.config.DataDir + ".manager.json" }

func (m *Manager) acquire() (*lease, error) {
	path := m.config.DataDir + ".manager.lock"
	if err := rejectSymlink(path); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(file); err != nil {
		file.Close()
		return nil, err
	}
	return &lease{file: file}, nil
}
func randomSecret() (string, error) {
	var data [32]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}

var postgresVersion = regexp.MustCompile(`PostgreSQL\) ([0-9]+)\.`)

func (m *Manager) major(ctx context.Context) (int, error) {
	output, err := processrunner.CombinedOutput(ctx, exec.Command(binary(m.config.BinDir, "postgres"), "--version"))
	if err != nil {
		return 0, fmt.Errorf("localdb: bundled PostgreSQL cannot run: %w", err)
	}
	match := postgresVersion.FindStringSubmatch(string(output))
	if len(match) != 2 {
		return 0, fmt.Errorf("localdb: unrecognized bundled PostgreSQL version")
	}
	return strconv.Atoi(match[1])
}
func (m *Manager) save(state metadata) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(m.metadataPath(), data, 0o600)
}
func (m *Manager) loadOrInitialize(ctx context.Context, major int) (metadata, error) {
	path := m.metadataPath()
	if err := rejectSymlink(path); err != nil {
		return metadata{}, err
	}
	data, err := os.ReadFile(path)
	var state metadata
	if errors.Is(err, os.ErrNotExist) {
		entries, readErr := os.ReadDir(m.config.DataDir)
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return state, readErr
		}
		if len(entries) > 0 {
			return state, fmt.Errorf("localdb: existing cluster has no ownership metadata; data was preserved")
		}
		password, err := randomSecret()
		if err != nil {
			return state, err
		}
		id, err := randomSecret()
		if err != nil {
			return state, err
		}
		state = metadata{Format: 1, Major: major, ClusterID: id, Password: password, Phase: "initializing"}
		if err := m.save(state); err != nil {
			return state, err
		}
	} else if err != nil {
		return state, err
	} else {
		if err := json.Unmarshal(data, &state); err != nil {
			return state, fmt.Errorf("localdb: corrupt ownership metadata; data was preserved: %w", err)
		}
		if state.Format != 1 || len(state.Password) != 64 || len(state.ClusterID) != 64 || (state.Phase != "initializing" && state.Phase != "ready") {
			return state, fmt.Errorf("localdb: invalid ownership metadata; data was preserved")
		}
		if state.Major != major {
			return state, fmt.Errorf("localdb: PostgreSQL major upgrade %d to %d requires explicit migration; data was preserved", state.Major, major)
		}
	}
	version, err := os.ReadFile(filepath.Join(m.config.DataDir, "PG_VERSION"))
	if errors.Is(err, os.ErrNotExist) {
		entries, readErr := os.ReadDir(m.config.DataDir)
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return state, readErr
		}
		if state.Phase != "initializing" || len(entries) > 0 {
			return state, fmt.Errorf("localdb: cluster data is missing or incomplete; data was preserved")
		}
		if err := m.initialize(ctx, state); err != nil {
			return state, err
		}
		state.Phase = "ready"
		if err := m.save(state); err != nil {
			return state, err
		}
	} else if err != nil {
		return state, err
	} else if strings.TrimSpace(string(version)) != strconv.Itoa(major) {
		return state, fmt.Errorf("localdb: cluster PG_VERSION differs from bundled PostgreSQL major %d; data was preserved", major)
	}
	marker, err := os.ReadFile(filepath.Join(m.config.DataDir, ".overkill-cluster-id"))
	if err != nil || string(marker) != state.ClusterID {
		return state, fmt.Errorf("localdb: cluster ownership marker does not match; data was preserved")
	}
	return state, nil
}
func (m *Manager) initialize(ctx context.Context, state metadata) error {
	stage, err := os.MkdirTemp(filepath.Dir(m.config.DataDir), ".overkill-pg-init-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage) // Only this invocation's unpublished staging cluster.
	password, err := os.CreateTemp(filepath.Dir(m.config.DataDir), ".overkill-pg-password-*")
	if err != nil {
		return err
	}
	defer os.Remove(password.Name())
	if _, err := password.WriteString(state.Password + "\n"); err != nil {
		password.Close()
		return err
	}
	if err := password.Close(); err != nil {
		return err
	}
	output, err := processrunner.CombinedOutput(ctx, exec.Command(binary(m.config.BinDir, "initdb"), "-D", stage, "-U", "overkill_owner", "--auth-local=scram-sha-256", "--auth-host=scram-sha-256", "--pwfile="+password.Name(), "--encoding=UTF8", "--no-locale"))
	if err != nil {
		return fmt.Errorf("localdb: initialize private cluster: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if err := atomicfile.WriteFile(filepath.Join(stage, ".overkill-cluster-id"), []byte(state.ClusterID), 0o600); err != nil {
		return err
	}
	if entries, err := os.ReadDir(m.config.DataDir); err == nil {
		if len(entries) > 0 {
			return fmt.Errorf("localdb: destination gained data during initialization; preserved")
		}
		if err := os.Remove(m.config.DataDir); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(stage, m.config.DataDir)
}

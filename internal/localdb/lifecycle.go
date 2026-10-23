package localdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/processrunner"
	"github.com/lib/pq"
)

// Start owns the cluster until Stop or ctx cancellation. The supplied context
// is the application/database lifetime, not merely the startup handshake.
func (m *Manager) Start(ctx context.Context) (ConnectionInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ConnectionInfo{}, err
	}
	if m.run != nil {
		select {
		case <-m.run.exited:
			m.run.owner.release()
			m.run = nil
		default:
			return m.run.info, nil
		}
	}
	owner, err := m.acquire()
	if err != nil {
		return ConnectionInfo{}, err
	}
	transferred := false
	defer func() {
		if !transferred {
			owner.release()
		}
	}()
	startup, cancel := context.WithTimeout(ctx, m.config.StartupTimeout)
	defer cancel()
	major, err := m.major(startup)
	if err != nil {
		return ConnectionInfo{}, err
	}
	state, err := m.loadOrInitialize(startup, major)
	if err != nil {
		return ConnectionInfo{}, err
	}
	if err := m.recoverOrphan(startup, state); err != nil {
		return ConnectionInfo{}, err
	}
	for attempt := 0; attempt < 3; attempt++ {
		reservation, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			return ConnectionInfo{}, err
		}
		port := reservation.Addr().(*net.TCPAddr).Port
		state.Port, state.PID, state.Started = port, 0, 0
		if err := m.save(state); err != nil {
			reservation.Close()
			return ConnectionInfo{}, err
		}
		info := ConnectionInfo{Host: "127.0.0.1", Port: port, Database: "overkill", User: "overkill_owner", DataDir: m.config.DataDir, password: state.Password}
		lifetime, stop := context.WithCancel(ctx)
		run := &running{owner: owner, info: info, cancel: stop, exited: make(chan struct{}), diagnostics: &boundedOutput{}}
		command := exec.Command(binary(m.config.BinDir, "postgres"), "-D", m.config.DataDir, "-h", "127.0.0.1", "-p", strconv.Itoa(port), "-c", "unix_socket_directories="+m.config.SocketDir, "-c", "password_encryption=scram-sha-256", "-c", "shared_buffers=32MB")
		command.Stdout, command.Stderr = run.diagnostics, run.diagnostics
		reservation.Close() // PostgreSQL must bind itself; conflicts are retried below.
		go func() { run.err = processrunner.Run(lifetime, command); close(run.exited) }()
		err = m.waitReady(startup, run, &state)
		if err == nil {
			err = m.ensureDatabase(startup, info)
		}
		if err != nil {
			stop()
			<-run.exited
			if attempt < 2 && startup.Err() == nil && strings.Contains(run.diagnostics.String(), "could not create any TCP/IP sockets") {
				continue
			}
			if startup.Err() != nil {
				return ConnectionInfo{}, fmt.Errorf("localdb: private PostgreSQL startup deadline: %w: %s", err, strings.TrimSpace(run.diagnostics.String()))
			}
			return ConnectionInfo{}, fmt.Errorf("localdb: private PostgreSQL startup failed: %w: %s", err, strings.TrimSpace(run.diagnostics.String()))
		}
		m.run = run
		transferred = true
		go func() { <-run.exited; owner.release() }()
		return info, nil
	}
	return ConnectionInfo{}, fmt.Errorf("localdb: could not reserve a loopback PostgreSQL port")
}

func (m *Manager) waitReady(ctx context.Context, run *running, state *metadata) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	failure := func() error {
		if lastErr == nil {
			return ctx.Err()
		}
		detail := lastErr.Error()
		if run.info.password != "" {
			detail = strings.ReplaceAll(detail, run.info.password, "[redacted]")
		}
		return fmt.Errorf("%w (last readiness check: %s)", ctx.Err(), detail)
	}
	for {
		select {
		case <-ctx.Done():
			return failure()
		case <-run.exited:
			return fmt.Errorf("bundled PostgreSQL exited before readiness: %w", run.err)
		default:
		}
		pid, err := readPostmaster(m.config.DataDir)
		if err == nil && pid.Port == run.info.Port && pid.DataDir == m.config.DataDir {
			if state.PID != pid.PID || state.Started != pid.Started {
				state.PID, state.Started = pid.PID, pid.Started
				if err := m.save(*state); err != nil {
					return err
				}
			}
			check, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
			err = m.verifyConnection(check, run.info, pid)
			cancel()
			if err == nil {
				return nil
			}
		} else if err == nil {
			err = fmt.Errorf("localdb: PID file identity differs from expected data directory or port")
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return failure()
		case <-run.exited:
			return fmt.Errorf("bundled PostgreSQL exited before readiness: %w", run.err)
		case <-ticker.C:
		}
	}
}

type postmaster struct {
	PID     int
	DataDir string
	Started int64
	Port    int
}

func readPostmaster(dataDir string) (postmaster, error) {
	var result postmaster
	file, err := os.Open(filepath.Join(dataDir, "postmaster.pid"))
	if err != nil {
		return result, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 8193))
	if err != nil {
		return result, err
	}
	return parsePostmaster(data, dataDir)
}

func parsePostmaster(data []byte, dataDir string) (postmaster, error) {
	var result postmaster
	var err error
	if len(data) > 8192 {
		return result, fmt.Errorf("localdb: postmaster identity file exceeds 8192 bytes")
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) < 4 {
		return result, fmt.Errorf("localdb: incomplete postmaster identity file")
	}
	result.PID, err = strconv.Atoi(lines[0])
	if err != nil || result.PID < 1 {
		return result, fmt.Errorf("localdb: invalid postmaster PID")
	}
	result.DataDir = filepath.Clean(lines[1])
	result.Started, err = strconv.ParseInt(lines[2], 10, 64)
	if err != nil || result.Started < 1 {
		return result, fmt.Errorf("localdb: invalid postmaster start time")
	}
	result.Port, err = strconv.Atoi(lines[3])
	if err != nil || result.Port < 1 || result.Port > 65535 {
		return result, fmt.Errorf("localdb: invalid postmaster port")
	}
	if result.DataDir != dataDir {
		return result, fmt.Errorf("localdb: postmaster data directory identity does not match")
	}
	return result, nil
}
func (m *Manager) verifyConnection(ctx context.Context, info ConnectionInfo, pid postmaster) error {
	connection, err := sql.Open("postgres", info.dsn("postgres"))
	if err != nil {
		return err
	}
	defer connection.Close()
	var dataDir string
	var port int
	var identity string
	// pg_postmaster_start_time() uses PgStartTime, whereas postmaster.pid uses
	// MyStartTime. Those are distinct clocks and can cross a second boundary.
	// Authenticate the exact PID-file identity through the owned SQL connection.
	err = connection.QueryRowContext(ctx, `SELECT current_setting('data_directory'),current_setting('port')::int,pg_read_file('postmaster.pid',0,8192)`).Scan(&dataDir, &port, &identity)
	if err != nil {
		return err
	}
	if filepath.Clean(dataDir) != m.config.DataDir || info.DataDir != m.config.DataDir || pid.DataDir != m.config.DataDir {
		return fmt.Errorf("localdb: authenticated server data directory differs from the owned cluster")
	}
	if port != pid.Port || port != info.Port {
		return fmt.Errorf("localdb: authenticated server port differs from the owned cluster")
	}
	authenticated, err := parsePostmaster([]byte(identity), m.config.DataDir)
	if err != nil {
		return fmt.Errorf("localdb: authenticated server PID file: %w", err)
	}
	if authenticated.PID != pid.PID {
		return fmt.Errorf("localdb: authenticated server PID differs from the owned cluster")
	}
	if authenticated.Started != pid.Started {
		return fmt.Errorf("localdb: authenticated server PID-file start time differs from the owned cluster")
	}
	if authenticated.Port != pid.Port {
		return fmt.Errorf("localdb: authenticated server PID-file port differs from the owned cluster")
	}
	return nil
}
func (m *Manager) ensureDatabase(ctx context.Context, info ConnectionInfo) error {
	connection, err := sql.Open("postgres", info.dsn("postgres"))
	if err != nil {
		return err
	}
	defer connection.Close()
	var exists bool
	if err := connection.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)`, info.Database).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		_, err = connection.ExecContext(ctx, "CREATE DATABASE "+pq.QuoteIdentifier(info.Database))
		return err
	}
	return nil
}

// recoverOrphan never adopts an unmanaged server or removes a PID file itself.
// Only a matching private credential/marker/PID/start/port/datadir can authorize
// controlled pg_ctl shutdown. PostgreSQL handles provably dead PID files itself.
func (m *Manager) recoverOrphan(ctx context.Context, state metadata) error {
	pid, err := readPostmaster(m.config.DataDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("localdb: cannot verify existing postmaster; preserved: %w", err)
	}
	statusOutput, statusErr := processrunner.CombinedOutput(ctx, exec.Command(binary(m.config.BinDir, "pg_ctl"), "-D", m.config.DataDir, "status"))
	if statusErr != nil {
		var exit *exec.ExitError
		if errors.As(statusErr, &exit) && exit.ExitCode() == 3 {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("localdb: cannot verify previous postmaster status: %w: %s", statusErr, strings.TrimSpace(string(statusOutput)))
	}
	if pid.PID != state.PID || pid.Started != state.Started || pid.Port != state.Port {
		return fmt.Errorf("localdb: existing postmaster does not match recorded owner; no process was stopped")
	}
	info := ConnectionInfo{Host: "127.0.0.1", Port: state.Port, Database: "overkill", User: "overkill_owner", DataDir: m.config.DataDir, password: state.Password}
	verification, cancel := context.WithTimeout(ctx, 2*time.Second)
	err = m.verifyConnection(verification, info, pid)
	cancel()
	if err != nil {
		return fmt.Errorf("localdb: cannot authenticate previous cluster owner; no process was stopped")
	}
	shutdown, cancel := context.WithTimeout(ctx, m.config.ShutdownTimeout)
	defer cancel()
	if err := m.fastStop(shutdown); err != nil {
		return fmt.Errorf("localdb: previous owned cluster could not stop safely: %w", err)
	}
	return nil
}
func (m *Manager) fastStop(ctx context.Context) error {
	seconds := int(m.config.ShutdownTimeout.Round(time.Second) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	output, err := processrunner.CombinedOutput(ctx, exec.Command(binary(m.config.BinDir, "pg_ctl"), "-D", m.config.DataDir, "-m", "fast", "-w", "-t", strconv.Itoa(seconds), "stop"))
	if err != nil {
		return fmt.Errorf("controlled PostgreSQL shutdown: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// Stop closes the exact owned process tree. Close database pools first. A
// shutdown deadline forces termination and reaping, while preserving cluster
// files for PostgreSQL's normal crash recovery on the next launch.
func (m *Manager) Stop(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.run == nil {
		return nil
	}
	run := m.run
	defer run.owner.release()
	select {
	case <-run.exited:
		m.run = nil
		return nil
	default:
	}
	shutdown, cancel := context.WithTimeout(ctx, m.config.ShutdownTimeout)
	defer cancel()
	pid, identityErr := readPostmaster(m.config.DataDir)
	stateData, readErr := os.ReadFile(m.metadataPath())
	var state metadata
	if readErr == nil {
		readErr = json.Unmarshal(stateData, &state)
	}
	var stopErr error
	if identityErr != nil || readErr != nil || pid.PID != state.PID || pid.Started != state.Started || pid.Port != run.info.Port {
		stopErr = fmt.Errorf("localdb: shutdown identity changed; stopping only the tracked child process group")
	} else {
		stopErr = m.fastStop(shutdown)
	}
	if stopErr == nil {
		select {
		case <-run.exited:
			m.run = nil
			return nil
		case <-shutdown.Done():
			stopErr = shutdown.Err()
		}
	}
	run.cancel()
	<-run.exited // processrunner owns and reaps every descendant, with bounded pipe draining.
	m.run = nil
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return stopErr
}

// Wait observes the current owned database process. An unexpected database exit
// must stop its dependent service rather than leave a healthy-looking API.
func (m *Manager) Wait(ctx context.Context) error {
	m.mu.Lock()
	run := m.run
	m.mu.Unlock()
	if run == nil {
		return fmt.Errorf("localdb: database is not running")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-run.exited:
		if run.err != nil {
			return fmt.Errorf("localdb: owned database exited: %w", run.err)
		}
		return fmt.Errorf("localdb: owned database stopped")
	}
}

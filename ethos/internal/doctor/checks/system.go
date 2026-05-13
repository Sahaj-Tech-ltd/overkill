package checks

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/doctor"
)

// dirsToCheck enumerates the runtime directories ethos writes into. Each
// must exist (created if missing) and be writable. Failure means the user
// cannot save sessions, plugins, or journal entries.
func dirsToCheck(configDir string) []struct{ path, label string } {
	return []struct{ path, label string }{
		{configDir, "~/.overkill"},
		{filepath.Join(configDir, "sessions"), "~/.overkill/sessions"},
		{filepath.Join(configDir, "plugins"), "~/.overkill/plugins"},
		{filepath.Join(configDir, "cache"), "~/.overkill/cache"},
		{filepath.Join(configDir, "journal"), "~/.overkill/journal"},
	}
}

// RegisterFilesystem registers one check per ethos data directory. Serial —
// these are mkdir + write probes against the same parent.
func RegisterFilesystem(r *doctor.Runner, d Deps) {
	for _, dir := range dirsToCheck(d.ConfigDir) {
		dir := dir
		r.Register(doctor.SubsystemCheck{
			ID:       "fs." + dir.label,
			Name:     "Writable: " + dir.label,
			Category: doctor.CatSystem,
			Fn: func(ctx context.Context) doctor.Result {
				if err := os.MkdirAll(dir.path, 0o755); err != nil {
					return failf("chmod or chown "+dir.path+" so the current user owns it",
						"mkdir: %v", err)
				}
				probe := filepath.Join(dir.path, ".doctor-write-probe")
				if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
					return failf("chmod u+w "+dir.path,
						"write probe: %v", err)
				}
				_ = os.Remove(probe)
				return okf("read+write at %s", dir.path)
			},
		})
	}
}

// RegisterDisk inspects free space at the ethos data root. We thread the
// ConfigDir through so it works the same on both real installs and CI.
func RegisterDisk(r *doctor.Runner, d Deps) {
	r.Register(doctor.SubsystemCheck{
		ID:       "disk.free",
		Name:     "Disk space",
		Category: doctor.CatSystem,
		Fn: func(ctx context.Context) doctor.Result {
			free, total, err := diskFree(d.ConfigDir)
			if err != nil {
				return info("%s", "could not stat filesystem: "+err.Error())
			}
			const mb = 1024 * 1024
			freeMB := free / mb
			totalMB := total / mb
			switch {
			case freeMB < 50:
				return failf("free space immediately on the disk hosting "+d.ConfigDir,
					"only %d MB free of %d MB", freeMB, totalMB)
			case freeMB < 500:
				return warnf("free space on the disk hosting "+d.ConfigDir,
					"%d MB free of %d MB", freeMB, totalMB)
			default:
				return okf("%d MB free", freeMB)
			}
		},
	})
}

// diskFree returns (free, total) bytes for the filesystem holding path. We
// keep the syscall in this file so the rest of the package stays unix-clean.
func diskFree(path string) (uint64, uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}
	free := stat.Bavail * uint64(stat.Bsize)
	total := stat.Blocks * uint64(stat.Bsize)
	return free, total, nil
}

// RegisterBridge is info-only until the Python bridge ships.
func RegisterBridge(r *doctor.Runner, d Deps) {
	r.Register(doctor.SubsystemCheck{
		ID:       "bridge.python",
		Name:     "Python bridge",
		Category: doctor.CatBackend,
		Fn: func(ctx context.Context) doctor.Result {
			return info("python bridge not configured (deferred per master plan)")
		},
	})
}

// RegisterMemory is info-only — no production memory backend ships yet.
func RegisterMemory(r *doctor.Runner, d Deps) {
	r.Register(doctor.SubsystemCheck{
		ID:       "memory.backend",
		Name:     "Memory backend",
		Category: doctor.CatBackend,
		Fn:       func(ctx context.Context) doctor.Result { return info("memory backend not configured") },
	})
}

// RegisterCellRenderer reports the savings of the experimental TUI renderer
// when ETHOS_CELL_RENDER=1. Otherwise it stays out of the way.
func RegisterCellRenderer(r *doctor.Runner, d Deps) {
	r.Register(doctor.SubsystemCheck{
		ID:       "ui.cell_renderer",
		Name:     "Cell renderer",
		Category: doctor.CatSystem,
		Fn: func(ctx context.Context) doctor.Result {
			if os.Getenv("ETHOS_CELL_RENDER") != "1" {
				return info("cell renderer disabled (set ETHOS_CELL_RENDER=1 to benchmark)")
			}
			// Tiny micro-benchmark: count how many bytes a naive line-by-line
			// render would emit against a single buffered write. We do not
			// import the renderer here to avoid pulling pkg/tui into doctor.
			lines := []string{"alpha", "beta", "gamma", "delta", "epsilon"}
			naive := 0
			for _, l := range lines {
				naive += len(l) + 1
			}
			ratio := float64(naive) / float64(len(strings.Join(lines, "\n")))
			return info("cell renderer ratio %.2fx (sample)", ratio)
		},
	})
}

// RegisterAnimations reports the current animation kill-switch state by
// reading both the config field and the override env var.
func RegisterAnimations(r *doctor.Runner, d Deps) {
	r.Register(doctor.SubsystemCheck{
		ID:       "ui.animations",
		Name:     "Animation kill-switch",
		Category: doctor.CatSystem,
		Fn: func(ctx context.Context) doctor.Result {
			env := os.Getenv("ETHOS_NO_ANIMATIONS")
			cfg := false
			if d.Cfg != nil {
				cfg = d.Cfg.UI.Animations
			}
			return info("config animations=%v; ETHOS_NO_ANIMATIONS=%q", cfg, env)
		},
	})
}

// RegisterVersion checks GitHub releases for a newer ethos version. Quiet
// failure mode — the network is allowed to be down.
func RegisterVersion(r *doctor.Runner, d Deps) {
	r.Register(doctor.SubsystemCheck{
		ID:       "version.freshness",
		Name:     "Version freshness",
		Category: doctor.CatSystem,
		Parallel: true,
		Fn: func(ctx context.Context) doctor.Result {
			ctx, cancel := withTimeout(ctx, 4*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
				"https://api.github.com/repos/Sahaj-Tech-ltd/ethos/releases/latest", nil)
			req.Header.Set("Accept", "application/vnd.github+json")
			req.Header.Set("User-Agent", "ethos-doctor/"+runtime.Version())
			resp, err := d.HTTP.Do(req)
			if err != nil {
				return info("offline; skipped version check")
			}
			defer resp.Body.Close()
			if resp.StatusCode == 404 {
				return info("no published releases yet")
			}
			if resp.StatusCode != 200 {
				return info("HTTP %d from github releases", resp.StatusCode)
			}
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
			// Cheap tag scrape: avoid a JSON dep to keep this self-contained.
			tag := extractTag(string(body))
			if tag == "" {
				return info("could not parse release JSON")
			}
			return info("latest release: %s", tag)
		},
	})
}

// extractTag pulls the first "tag_name":"vX.Y.Z" value from the GitHub
// response without importing encoding/json — we deliberately keep this check
// dependency-free so it can never break the rest of the doctor.
func extractTag(body string) string {
	const key = `"tag_name":"`
	i := strings.Index(body, key)
	if i < 0 {
		return ""
	}
	rest := body[i+len(key):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		return ""
	}
	return rest[:j]
}

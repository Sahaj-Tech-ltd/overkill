package checks

import (
	"context"
	"net"
	"os"
	"os/exec"
	"strings"

	"github.com/Sahaj-Tech-ltd/ethos/internal/doctor"
	syncpkg "github.com/Sahaj-Tech-ltd/ethos/internal/sync"
)

// RegisterSync pings whatever sync backend is configured. Implementation
// keeps the network call cheap: file → stat the path; s3 → List with limit;
// git → ls-remote on the configured URL.
func RegisterSync(r *doctor.Runner, d Deps) {
	if d.Cfg == nil || d.Cfg.Sync.Backend == "" {
		r.Register(doctor.SubsystemCheck{
			ID:       "sync.disabled",
			Name:     "Sync backend",
			Category: doctor.CatBackend,
			Fn:       func(ctx context.Context) doctor.Result { return info("sync disabled in config") },
		})
		return
	}
	cfg := d.Cfg.Sync
	r.Register(doctor.SubsystemCheck{
		ID:       "sync." + cfg.Backend,
		Name:     "Sync backend: " + cfg.Backend,
		Category: doctor.CatBackend,
		Parallel: true,
		Fn: func(ctx context.Context) doctor.Result {
			switch cfg.Backend {
			case "file":
				if cfg.File.Path == "" {
					return failf("set sync.file.path in config", "no path configured")
				}
				if _, err := os.Stat(cfg.File.Path); err != nil {
					return failf("create the directory at "+cfg.File.Path,
						"stat: %v", err)
				}
				return okf("file backend dir exists: %s", cfg.File.Path)
			case "git":
				if cfg.Git.RemoteURL == "" {
					return failf("set sync.git.remote_url in config", "no remote configured")
				}
				if _, err := exec.LookPath("git"); err != nil {
					return failf("install git", "git not on PATH")
				}
				cmd := exec.CommandContext(ctx, "git", "ls-remote", cfg.Git.RemoteURL)
				out, err := cmd.CombinedOutput()
				if err != nil {
					return failf("verify the remote URL and your credentials",
						"git ls-remote: %v: %s", err, strings.TrimSpace(string(out)))
				}
				return okf("git remote reachable")
			case "s3":
				be, err := syncpkg.NewBackend(cfg)
				if err != nil {
					return failf("verify sync.s3 settings in config",
						"backend init: %v", err)
				}
				if _, err := be.List(ctx); err != nil {
					return failf("verify s3 endpoint, bucket, and credentials",
						"List: %v", err)
				}
				return okf("s3 bucket reachable: %s", cfg.S3.Bucket)
			default:
				return failf("set sync.backend to file/s3/git",
					"unknown backend %q", cfg.Backend)
			}
		},
	})
}

// RegisterACP exercises the ACP listener if enabled — opens, then closes the
// configured socket address to confirm we can bind.
func RegisterACP(r *doctor.Runner, d Deps) {
	if d.Cfg == nil || !d.Cfg.ACP.Enabled {
		r.Register(doctor.SubsystemCheck{
			ID:       "acp.disabled",
			Name:     "ACP server",
			Category: doctor.CatBackend,
			Fn:       func(ctx context.Context) doctor.Result { return info("acp disabled in config") },
		})
		return
	}
	addr := d.Cfg.ACP.Listen
	if addr == "" {
		addr = "127.0.0.1:34567"
	}
	r.Register(doctor.SubsystemCheck{
		ID:       "acp.bind",
		Name:     "ACP listener bind",
		Category: doctor.CatBackend,
		Fn: func(ctx context.Context) doctor.Result {
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return failf("change acp.listen to a free port (e.g. 127.0.0.1:34567)",
					"bind %s: %v", addr, err)
			}
			_ = ln.Close()
			return okf("port %s available", addr)
		},
	})
}

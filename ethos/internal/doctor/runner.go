// Package doctor's runner.go implements the comprehensive `ethos doctor`
// self-test described in master plan §4.13. The legacy NewDoctor / Check API
// (see checks.go, fixes.go) covers config bootstrap and remains in place; the
// Runner here orchestrates the full subsystem sweep and produces a Report
// suitable for both terminal and JSON output.
package doctor

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Severity is the granular status used by Result. It is a superset of the
// legacy Status enum: ok / warn / fail / info / skip.
type Severity string

const (
	SevOK   Severity = "ok"
	SevWarn Severity = "warn"
	SevFail Severity = "fail"
	SevInfo Severity = "info"
	SevSkip Severity = "skip"
)

// Category groups checks for grouped output.
type Category string

const (
	CatCore     Category = "Core"
	CatProvider Category = "Providers"
	CatStorage  Category = "Storage"
	CatBackend  Category = "Backends"
	CatPlugin   Category = "Plugins"
	CatSystem   Category = "System"
)

// Result is the per-check output. ID is a stable machine-readable name; Detail
// expands on the status; Fix is a concrete remediation hint that should be
// actionable (e.g. "run /config to set api key", not "check your config").
type Result struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Category Category      `json:"category"`
	Status   Severity      `json:"status"`
	Detail   string        `json:"detail,omitempty"`
	Fix      string        `json:"fix,omitempty"`
	Duration time.Duration `json:"duration_ms"`
}

// CheckFunc is the per-check entrypoint. It must respect the supplied context
// for cancellation and should be safe to call concurrently with peers when its
// Parallel flag is true.
type CheckFunc func(ctx context.Context) Result

// SubsystemCheck is the unit a Runner consumes. Parallel checks run together
// in a worker pool; serial checks run one after another (used for filesystem
// ops where ordering matters or shared state is fragile).
type SubsystemCheck struct {
	ID       string
	Name     string
	Category Category
	Parallel bool
	Timeout  time.Duration // per-check; defaults to runner.PerCheckTimeout
	Fn       CheckFunc
}

// Runner executes a set of SubsystemChecks under an overall budget.
type Runner struct {
	checks          []SubsystemCheck
	PerCheckTimeout time.Duration
	OverallTimeout  time.Duration
	Concurrency     int
}

// NewRunner builds an empty runner with sensible defaults (5s per check, 30s
// total, 8 concurrent network checks).
func NewRunner() *Runner {
	return &Runner{
		PerCheckTimeout: 5 * time.Second,
		OverallTimeout:  30 * time.Second,
		Concurrency:     8,
	}
}

// Register adds a check. Order is preserved within the parallel and serial
// pools.
func (r *Runner) Register(c SubsystemCheck) {
	r.checks = append(r.checks, c)
}

// Run executes every registered check. Results are returned in a stable order:
// (Category, then registration order).
func (r *Runner) Run(ctx context.Context) Summary {
	if r.OverallTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.OverallTimeout)
		defer cancel()
	}

	results := make([]Result, len(r.checks))

	var wg sync.WaitGroup
	sem := make(chan struct{}, r.Concurrency)

	runOne := func(i int, c SubsystemCheck) {
		timeout := c.Timeout
		if timeout <= 0 {
			timeout = r.PerCheckTimeout
		}
		cctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		start := time.Now()
		res := safeInvoke(cctx, c.Fn)
		res.Duration = time.Since(start)
		if res.ID == "" {
			res.ID = c.ID
		}
		if res.Name == "" {
			res.Name = c.Name
		}
		if res.Category == "" {
			res.Category = c.Category
		}
		results[i] = res
	}

	// Parallel phase.
	for i, c := range r.checks {
		if !c.Parallel {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, c SubsystemCheck) {
			defer wg.Done()
			defer func() { <-sem }()
			runOne(i, c)
		}(i, c)
	}
	wg.Wait()

	// Serial phase (filesystem-sensitive, BadgerDB open, etc.).
	for i, c := range r.checks {
		if c.Parallel {
			continue
		}
		runOne(i, c)
	}

	return summarize(results)
}

// safeInvoke wraps the check function so a panic inside one check does not
// take down the whole sweep. The panic surfaces as a fail Result.
func safeInvoke(ctx context.Context, fn CheckFunc) (out Result) {
	defer func() {
		if rec := recover(); rec != nil {
			out = Result{
				Status: SevFail,
				Detail: "check panicked",
				Fix:    "this is a doctor bug; please file an issue at github.com/Sahaj-Tech-ltd/overkill/issues",
			}
		}
	}()
	return fn(ctx)
}

// Summary is the top-level shape returned by Runner.Run and serialized to
// JSON / pretty-printed.
type Summary struct {
	Timestamp time.Time     `json:"ts"`
	Version   string        `json:"version"`
	Checks    []Result      `json:"checks"`
	Counts    SummaryCounts `json:"summary"`
}

type SummaryCounts struct {
	OK   int `json:"ok"`
	Warn int `json:"warn"`
	Fail int `json:"fail"`
	Info int `json:"info"`
	Skip int `json:"skip"`
}

func summarize(results []Result) Summary {
	// Stable group order: Core, Providers, Storage, Backends, Plugins, System,
	// then anything unknown alphabetically.
	order := map[Category]int{
		CatCore: 0, CatProvider: 1, CatStorage: 2, CatBackend: 3,
		CatPlugin: 4, CatSystem: 5,
	}
	sorted := make([]Result, len(results))
	copy(sorted, results)
	sort.SliceStable(sorted, func(i, j int) bool {
		oi, oki := order[sorted[i].Category]
		oj, okj := order[sorted[j].Category]
		if !oki {
			oi = 99
		}
		if !okj {
			oj = 99
		}
		if oi != oj {
			return oi < oj
		}
		return false // preserve registration order within category
	})

	s := Summary{
		Timestamp: time.Now().UTC(),
		Checks:    sorted,
	}
	for _, r := range sorted {
		switch r.Status {
		case SevOK:
			s.Counts.OK++
		case SevWarn:
			s.Counts.Warn++
		case SevFail:
			s.Counts.Fail++
		case SevInfo:
			s.Counts.Info++
		case SevSkip:
			s.Counts.Skip++
		}
	}
	return s
}

// Package measure holds the pure parts of the resource measurement (ADR 0018):
// parsing what Docker reports, comparing results with thresholds and writing
// the report. Seeding the example household and driving the container live in
// seed.go and cmd/hejmo-measure.
package measure

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Result is what one measurement run found.
type Result struct {
	IdleMemoryMiB  float64 `json:"idle_memory_mib"`  // working set after start, nothing happening
	BurstMemoryMiB float64 `json:"burst_memory_mib"` // peak working set while all calendars sync at once
	StartSeconds   float64 `json:"start_seconds"`    // container start until /healthz answers
	IdleCPUPercent float64 `json:"idle_cpu_percent"` // share of the one allowed CPU while idle
}

// Thresholds are the ceilings, in the units of Result; a result above one
// fails the build.
type Thresholds = Result

// LoadThresholds reads the thresholds file (ci/resource-thresholds.json).
func LoadThresholds(path string) (Thresholds, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Thresholds{}, err
	}
	var t Thresholds
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		return Thresholds{}, fmt.Errorf("%s: %w", path, err)
	}
	return t, nil
}

// Check returns one line per threshold the result exceeds.
func Check(r Result, t Thresholds) []string {
	var out []string
	over := func(name string, got, limit float64, unit string) {
		if got > limit {
			out = append(out, fmt.Sprintf("%s %.1f %s is above the threshold of %.1f %s", name, got, unit, limit, unit))
		}
	}
	over("idle memory", r.IdleMemoryMiB, t.IdleMemoryMiB, "MiB")
	over("burst memory", r.BurstMemoryMiB, t.BurstMemoryMiB, "MiB")
	over("start time", r.StartSeconds, t.StartSeconds, "s")
	over("idle CPU", r.IdleCPUPercent, t.IdleCPUPercent, "%")
	return out
}

// Report is a Markdown table for the CI job summary.
func Report(r Result, t Thresholds) string {
	var b strings.Builder
	b.WriteString("| Measurement | Measured | Threshold |\n|---|---|---|\n")
	fmt.Fprintf(&b, "| Idle memory | %.1f MiB | %.0f MiB |\n", r.IdleMemoryMiB, t.IdleMemoryMiB)
	fmt.Fprintf(&b, "| Sync burst memory (peak) | %.1f MiB | %.0f MiB |\n", r.BurstMemoryMiB, t.BurstMemoryMiB)
	fmt.Fprintf(&b, "| Start time to /healthz | %.1f s | %.0f s |\n", r.StartSeconds, t.StartSeconds)
	fmt.Fprintf(&b, "| Idle CPU | %.1f %% | %.0f %% |\n", r.IdleCPUPercent, t.IdleCPUPercent)
	return b.String()
}

// ParseMemUsage reads the memory part of `docker stats` ("23.5MiB / 1GiB")
// and returns MiB.
func ParseMemUsage(s string) (float64, error) {
	used, _, _ := strings.Cut(s, "/")
	used = strings.TrimSpace(used)
	units := []struct {
		suffix string
		bytes  float64
	}{
		{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10},
		{"GB", 1e9}, {"MB", 1e6}, {"kB", 1e3}, {"B", 1},
	}
	for _, u := range units {
		if num, ok := strings.CutSuffix(used, u.suffix); ok {
			v, err := strconv.ParseFloat(strings.TrimSpace(num), 64)
			if err != nil {
				return 0, fmt.Errorf("memory %q: %w", s, err)
			}
			return v * u.bytes / (1 << 20), nil
		}
	}
	return 0, fmt.Errorf("memory %q: unknown format", s)
}

// ParseCPU reads the CPU part of `docker stats` ("12.50%").
func ParseCPU(s string) (float64, error) {
	v, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(s), "%"), 64)
	if err != nil {
		return 0, fmt.Errorf("cpu %q: %w", s, err)
	}
	return v, nil
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

// StatsFormat is the --format that makes `docker stats` print what ParseStats reads.
const StatsFormat = "{{.MemUsage}}|{{.CPUPerc}}"

// ParseStats reads one line of `docker stats --format StatsFormat`, also from
// the stream, which prefixes lines with terminal escape sequences. It returns
// memory in MiB and CPU in percent.
func ParseStats(line string) (mem, cpu float64, err error) {
	line = strings.TrimSpace(ansi.ReplaceAllString(line, ""))
	m, c, ok := strings.Cut(line, "|")
	if !ok {
		return 0, 0, fmt.Errorf("unexpected docker stats line %q", line)
	}
	if mem, err = ParseMemUsage(m); err != nil {
		return
	}
	cpu, err = ParseCPU(c)
	return
}

// PeakTracker keeps the highest memory of the streamed stats lines it is fed.
type PeakTracker struct {
	mu      sync.Mutex
	highest float64
	n       int
}

// Add parses one line and records it; lines that do not parse are ignored.
func (p *PeakTracker) Add(line string) {
	mem, _, err := ParseStats(line)
	if err != nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.highest = max(p.highest, mem)
	p.n++
}

// Peak returns the highest memory in MiB, or an error when no line parsed, so
// that a burst nobody measured cannot pass the gate.
func (p *PeakTracker) Peak() (float64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.n == 0 {
		return 0, errors.New("the sync burst produced no docker stats samples")
	}
	return p.highest, nil
}

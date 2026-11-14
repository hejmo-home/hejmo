package measure_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/measure"
)

func TestParseMemUsage(t *testing.T) {
	for in, want := range map[string]float64{
		"23.5MiB / 1GiB": 23.5,
		"1.5GiB / 1GiB":  1536,
		"512KiB / 1GiB":  0.5,
		"900MB / 1GB":    900 * 1e6 / (1 << 20),
	} {
		got, err := measure.ParseMemUsage(in)
		if err != nil || got < want-0.001 || got > want+0.001 {
			t.Errorf("ParseMemUsage(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := measure.ParseMemUsage("lots"); err == nil {
		t.Error("garbage must not parse")
	}
}

func TestParseCPU(t *testing.T) {
	got, err := measure.ParseCPU("12.50%")
	if err != nil || got != 12.5 {
		t.Fatalf("ParseCPU = %v, %v", got, err)
	}
}

func TestCheckFailsAboveThresholds(t *testing.T) {
	th := measure.Thresholds{IdleMemoryMiB: 50, BurstMemoryMiB: 150, StartSeconds: 5, IdleCPUPercent: 10}
	ok := measure.Result{IdleMemoryMiB: 30, BurstMemoryMiB: 100, StartSeconds: 2, IdleCPUPercent: 1}
	if v := measure.Check(ok, th); len(v) != 0 {
		t.Fatalf("within thresholds, got violations %v", v)
	}
	bad := measure.Result{IdleMemoryMiB: 51, BurstMemoryMiB: 151, StartSeconds: 5.1, IdleCPUPercent: 11}
	v := measure.Check(bad, th)
	if len(v) != 4 {
		t.Fatalf("want 4 violations, got %v", v)
	}
	for _, name := range []string{"idle memory", "burst memory", "start time", "idle CPU"} {
		if !strings.Contains(strings.Join(v, "\n"), name) {
			t.Errorf("no violation names %q: %v", name, v)
		}
	}
}

func TestExactlyAtThresholdPasses(t *testing.T) {
	th := measure.Thresholds{IdleMemoryMiB: 50, BurstMemoryMiB: 150, StartSeconds: 5, IdleCPUPercent: 10}
	if v := measure.Check(measure.Result{IdleMemoryMiB: 50, BurstMemoryMiB: 150, StartSeconds: 5, IdleCPUPercent: 10}, th); len(v) != 0 {
		t.Fatal(v)
	}
}

func TestReportQuotesAllThreeMeasurements(t *testing.T) {
	r := measure.Report(measure.Result{IdleMemoryMiB: 31.4, BurstMemoryMiB: 96.2, StartSeconds: 1.3, IdleCPUPercent: 0.4},
		measure.Thresholds{IdleMemoryMiB: 60, BurstMemoryMiB: 200, StartSeconds: 8, IdleCPUPercent: 10})
	for _, want := range []string{"Idle memory", "31.4", "Sync burst", "96.2", "Start time", "1.3", "60", "200"} {
		if !strings.Contains(r, want) {
			t.Errorf("report lacks %q:\n%s", want, r)
		}
	}
}

func TestLoadThresholdsFile(t *testing.T) {
	th, err := measure.LoadThresholds("../../ci/resource-thresholds.json")
	if err != nil {
		t.Fatal(err)
	}
	if th.IdleMemoryMiB <= 0 || th.BurstMemoryMiB <= th.IdleMemoryMiB || th.StartSeconds <= 0 || th.IdleCPUPercent <= 0 {
		t.Fatalf("implausible thresholds %+v", th)
	}
	if th.BurstMemoryMiB >= 1024 {
		t.Fatal("burst threshold must stay below the 1 GB limit")
	}
}

// The install guide's hardware section quotes the values the build enforces,
// marked as tested in emulation (ADR 0018).
func TestHardwareSectionQuotesThresholds(t *testing.T) {
	th, err := measure.LoadThresholds("../../ci/resource-thresholds.json")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("../../docs/install/hardware.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	if !strings.Contains(doc, "tested in emulation") {
		t.Error("hardware section must say the Raspberry Pi is tested in emulation")
	}
	for _, want := range []string{
		fmt.Sprintf("%.0f MiB", th.IdleMemoryMiB), fmt.Sprintf("%.0f MiB", th.BurstMemoryMiB),
		fmt.Sprintf("%.0f s", th.StartSeconds), fmt.Sprintf("%.0f %%", th.IdleCPUPercent),
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("hardware section does not quote %q; update it with ci/resource-thresholds.json", want)
		}
	}
}

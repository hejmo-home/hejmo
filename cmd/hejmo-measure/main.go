// Command hejmo-measure measures the box's resource use in a container limited
// like a Raspberry Pi (1 CPU, 1 GB; ADR 0018) with a seeded example household,
// and fails when a measurement is above its threshold.
//
//	hejmo-measure -image hejmo:measure [-thresholds ci/resource-thresholds.json] [-report report.md]
//
// It needs Docker. It measures idle memory and CPU, peak memory while every
// synced calendar syncs at once (the sync burst), and the time from starting
// the container until /healthz answers.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/hejmo-home/hejmo/internal/measure"
	"github.com/hejmo-home/hejmo/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "hejmo-measure:", err)
		os.Exit(1)
	}
}

type options struct {
	image, thresholds, report, jsonOut string
	calendars, events, rounds          int
}

func run() error {
	var o options
	flag.StringVar(&o.image, "image", "hejmo:measure", "box image to measure")
	flag.StringVar(&o.thresholds, "thresholds", "ci/resource-thresholds.json", "thresholds file")
	flag.StringVar(&o.report, "report", "", "write the Markdown report here (CI job summary)")
	flag.StringVar(&o.jsonOut, "json", "", "write the measured values as JSON here")
	flag.IntVar(&o.calendars, "calendars", 8, "synced calendars in the sync burst")
	flag.IntVar(&o.events, "events", 2000, "events per feed")
	flag.IntVar(&o.rounds, "rounds", 6, "sync bursts, one after the other, so the sampling catches the peak")
	flag.Parse()

	th, err := measure.LoadThresholds(o.thresholds)
	if err != nil {
		return err
	}
	res, err := measureBox(o)
	if err != nil {
		return err
	}
	report := measure.Report(res, th)
	fmt.Println(report)
	if o.report != "" {
		if err := os.WriteFile(o.report, []byte("### Resource measurement (tested in emulation: 1 CPU, 1 GB)\n\n"+report), 0o644); err != nil {
			return err
		}
	}
	if o.jsonOut != "" {
		b, _ := json.MarshalIndent(res, "", "  ")
		if err := os.WriteFile(o.jsonOut, append(b, '\n'), 0o644); err != nil {
			return err
		}
	}
	if v := measure.Check(res, th); len(v) > 0 {
		return errors.New("above thresholds:\n  " + strings.Join(v, "\n  "))
	}
	return nil
}

func measureBox(o options) (measure.Result, error) {
	var res measure.Result
	dir, err := os.MkdirTemp("", "hejmo-measure-")
	if err != nil {
		return res, err
	}
	defer os.RemoveAll(dir)

	// The feed the synced calendars subscribe to, served from this host.
	feed := measure.Feed(o.events, time.Now())
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		return res, err
	}
	feedSrv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/calendar")
		_, _ = w.Write(feed)
	})}
	go feedSrv.Serve(ln)
	defer feedSrv.Close()
	feedURL := fmt.Sprintf("http://host.docker.internal:%d/cal.ics", ln.Addr().(*net.TCPAddr).Port)

	db, err := store.Open(filepath.Join(dir, "hejmo.db"))
	if err != nil {
		return res, err
	}
	seeded, err := measure.Seed(db, measure.SeedOptions{FeedURL: feedURL, Calendars: o.calendars, EventsPerFeed: o.events})
	if err != nil {
		db.Close()
		return res, err
	}
	if err := db.Close(); err != nil {
		return res, err
	}

	u, err := user.Current()
	if err != nil {
		return res, err
	}
	name := fmt.Sprintf("hejmo-measure-%d", os.Getpid())
	defer exec.Command("docker", "rm", "-f", name).Run()

	t0 := time.Now()
	out, err := exec.Command("docker", "run", "-d", "--name", name,
		"--cpus", "1", "--memory", "1g", "--memory-swap", "1g", // like a Raspberry Pi with 1 GB
		"--add-host", "host.docker.internal:host-gateway",
		"--user", u.Uid+":"+u.Gid,
		"-p", "127.0.0.1::8080", "-v", dir+":/data", o.image).CombinedOutput()
	if err != nil {
		return res, fmt.Errorf("docker run: %w\n%s", err, out)
	}
	portOut, err := exec.Command("docker", "port", name, "8080/tcp").Output()
	if err != nil {
		return res, fmt.Errorf("docker port: %w", err)
	}
	addr := strings.TrimSpace(strings.Split(string(portOut), "\n")[0])
	base := "http://127.0.0.1" + addr[strings.LastIndex(addr, ":"):]

	if err := waitHealthy(base, 60*time.Second); err != nil {
		logs, _ := exec.Command("docker", "logs", name).CombinedOutput()
		return res, fmt.Errorf("%w\n%s", err, logs)
	}
	res.StartSeconds = time.Since(t0).Seconds()

	// Idle: let start-up work settle, then take the highest of three samples.
	time.Sleep(8 * time.Second)
	var cpus []float64
	for range 3 {
		mem, cpu, err := sample(name)
		if err != nil {
			return res, err
		}
		res.IdleMemoryMiB = max(res.IdleMemoryMiB, mem)
		cpus = append(cpus, cpu)
	}
	slices.Sort(cpus)
	res.IdleCPUPercent = cpus[len(cpus)/2]

	// Sync burst: every calendar syncs at once, round after round, while the
	// stats stream records the peak.
	peak, stop, err := streamPeak(name)
	if err != nil {
		return res, err
	}
	for range o.rounds {
		if err := burst(base, seeded.Token, seeded.CalendarIDs); err != nil {
			stop()
			return res, err
		}
	}
	time.Sleep(2500 * time.Millisecond) // the stream reports once a second
	stop()
	res.BurstMemoryMiB = max(peak(), res.IdleMemoryMiB)
	return res, nil
}

func waitHealthy(base string, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	client := &http.Client{Timeout: time.Second}
	for time.Now().Before(deadline) {
		if r, err := client.Get(base + "/healthz"); err == nil {
			r.Body.Close()
			if r.StatusCode == 200 {
				return nil
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("/healthz did not answer within %s", limit)
}

func burst(base, token string, ids []string) error {
	var wg sync.WaitGroup
	errs := make(chan error, len(ids))
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest("POST", base+"/api/v1/calendars/"+id+"/sync", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			r, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
			if err != nil {
				errs <- err
				return
			}
			defer r.Body.Close()
			body, _ := io.ReadAll(r.Body)
			if r.StatusCode != 200 || !strings.Contains(string(body), `"state":"ok"`) {
				errs <- fmt.Errorf("sync of %s: %d %s", id, r.StatusCode, body)
			}
		}()
	}
	wg.Wait()
	close(errs)
	return <-errs
}

const statsFormat = "{{.MemUsage}}|{{.CPUPerc}}"

var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

func parseStats(line string) (mem, cpu float64, err error) {
	line = strings.TrimSpace(ansi.ReplaceAllString(line, ""))
	m, c, ok := strings.Cut(line, "|")
	if !ok {
		return 0, 0, fmt.Errorf("unexpected docker stats line %q", line)
	}
	if mem, err = measure.ParseMemUsage(m); err != nil {
		return
	}
	cpu, err = measure.ParseCPU(c)
	return
}

func sample(name string) (mem, cpu float64, err error) {
	out, err := exec.Command("docker", "stats", "--no-stream", "--format", statsFormat, name).Output()
	if err != nil {
		return 0, 0, err
	}
	return parseStats(string(out))
}

// streamPeak follows `docker stats` and returns the highest memory seen.
func streamPeak(name string) (peak func() float64, stop func(), err error) {
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "docker", "stats", "--format", statsFormat, name)
	out, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, nil, err
	}
	var mu sync.Mutex
	var highest float64
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			if mem, _, err := parseStats(sc.Text()); err == nil {
				mu.Lock()
				highest = max(highest, mem)
				mu.Unlock()
			}
		}
	}()
	return func() float64 { mu.Lock(); defer mu.Unlock(); return highest },
		func() { cancel(); _ = cmd.Wait(); <-done }, nil
}

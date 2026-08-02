package bench

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseSize(t *testing.T) {
	tests := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"1", 1, true}, {"2KiB", 2 << 10, true}, {"3MiB", 3 << 20, true},
		{"4GiB", 4 << 30, true}, {"0", 0, false}, {"-1", 0, false},
		{"1MB", 0, false}, {"999999999999999999999GiB", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := parseSize(tt.in)
			if (err == nil) != tt.ok || err == nil && got != tt.want {
				t.Fatalf("parseSize(%q) = %d, %v; want %d, ok=%v", tt.in, got, err, tt.want, tt.ok)
			}
		})
	}
}

func TestResolveDiskSizeWithAvailable(t *testing.T) {
	const MiB = int64(1 << 20)
	tests := []struct {
		name      string
		requested int64
		explicit  bool
		available uint64
		want      int64
		adjusted  bool
		wantErr   bool
	}{
		{"fits by default", 64 * MiB, false, uint64(256 * MiB), 64 * MiB, false, false},
		{"halves constrained space", 64 * MiB, false, uint64(60*MiB + 512), 30 * MiB, true, false},
		{"explicit size is preserved", 32 * MiB, true, uint64(64 * MiB), 32 * MiB, false, false},
		{"explicit size fails when unavailable", 64 * MiB, true, uint64(32 * MiB), 0, false, true},
		{"too little space", 64 * MiB, false, uint64(2*MiB - 1), 0, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, adjusted, err := resolveDiskSizeWithAvailable(tt.requested, tt.explicit, tt.available)
			if (err != nil) != tt.wantErr || got != tt.want || adjusted != tt.adjusted {
				t.Fatalf("resolveDiskSizeWithAvailable() = %d, %t, %v; want %d, %t, error=%t", got, adjusted, err, tt.want, tt.adjusted, tt.wantErr)
			}
		})
	}
}

func TestFormatAvailableSaturates(t *testing.T) {
	if got := formatAvailable(^uint64(0)); got != "9223372036854775807" {
		t.Fatalf("formatAvailable(max) = %q", got)
	}
}

func TestRunSuiteRejectsBadSelectors(t *testing.T) {
	for _, pattern := range []string{"[", "DoesNotExist"} {
		err := runSuite(context.Background(), &bytes.Buffer{}, suiteConfig{count: 1, size: 1, pattern: pattern, diskDir: t.TempDir()})
		if _, ok := err.(regexpError); !ok {
			t.Errorf("pattern %q error = %T %v, want regexpError", pattern, err, err)
		}
	}
}

func TestRunSuiteSelectedOutput(t *testing.T) {
	var output bytes.Buffer
	err := runSuite(context.Background(), &output, suiteConfig{count: 0, size: 1, pattern: "^CPUCount$", diskDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	got := output.String()
	if strings.Count(got, "goos: ") != 1 || !strings.Contains(got, "BenchmarkCPUCount-0") {
		t.Fatalf("unexpected output:\n%s", got)
	}
	for _, unwanted := range []string{"BenchmarkCPUMulticore", "BenchmarkMemoryCopy", "BenchmarkDiskWrite", "BenchmarkDiskRead"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("output unexpectedly contains %q", unwanted)
		}
	}
}

func TestRunSuiteSelectsCPUMulticore(t *testing.T) {
	var output bytes.Buffer
	workers := runtime.GOMAXPROCS(0)
	err := runSuite(context.Background(), &output, suiteConfig{count: 0, size: 1, pattern: "^CPUMulticore$", diskDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	got := output.String()
	want := "BenchmarkCPUMulticore-0-" + strconv.Itoa(workers) + "workers"
	if !strings.Contains(got, want) {
		t.Fatalf("output does not contain %q:\n%s", want, got)
	}
	for _, unwanted := range []string{"BenchmarkCPUCount", "BenchmarkMemoryCopy", "BenchmarkDiskWrite", "BenchmarkDiskRead"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("output unexpectedly contains %q", unwanted)
		}
	}
}

func TestCPUParrots(t *testing.T) {
	tests := []struct {
		name    string
		result  testing.BenchmarkResult
		count   int64
		workers int
		want    float64
	}{
		{"single core", testing.BenchmarkResult{N: 2, T: 4 * time.Second}, 1_000_000, 1, 0.5},
		{"multicore", testing.BenchmarkResult{N: 2, T: 4 * time.Second}, 1_000_000, 8, 4},
		{"zero count", testing.BenchmarkResult{N: 2, T: time.Second}, 0, 1, 0},
		{"zero duration", testing.BenchmarkResult{N: 2}, 1_000_000, 1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extra := withCPUParrots(map[string]float64{"existing": 1}, tt.result, tt.count, tt.workers)
			if got := extra["parrots"]; got != tt.want {
				t.Errorf("parrots = %v, want %v", got, tt.want)
			}
			if extra["existing"] != 1 {
				t.Errorf("existing metric was not preserved: %v", extra)
			}
		})
	}
}

func TestRunSuiteReportsParrotsForCPUOnly(t *testing.T) {
	var output bytes.Buffer
	err := runSuite(context.Background(), &output, suiteConfig{count: 0, size: 1, benchMem: true, pattern: "CPU|MemoryCopy", diskDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(output.String(), "\n") {
		switch {
		case strings.HasPrefix(line, "BenchmarkCPU"):
			if !strings.Contains(line, "parrots") || !strings.Contains(line, "ns/op") || !strings.Contains(line, "B/op") {
				t.Errorf("CPU result is missing metrics: %q", line)
			}
		case strings.HasPrefix(line, "BenchmarkMemoryCopy"):
			if strings.Contains(line, "parrots") {
				t.Errorf("memory result unexpectedly contains parrots: %q", line)
			}
		}
	}
}

func TestMemoryBenchmarkCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := benchMemory(ctx, 32, true); err != context.Canceled {
		t.Fatalf("benchMemory() error = %v, want context.Canceled", err)
	}
}

func TestPrepareReadFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "disk-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := prepareReadFile(context.Background(), f, 2050); err != nil {
		t.Fatal(err)
	}
	info, err := f.Stat()
	if err != nil || info.Size() != 2050 {
		t.Fatalf("file size = %v, %v; want 2050", info.Size(), err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := prepareReadFile(context.Background(), f, 1); err == nil {
		t.Fatal("prepareReadFile() on closed file succeeded")
	}
}

func TestDiskTemporaryFileCleanupAndDirectory(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := runSuite(ctx, &bytes.Buffer{}, suiteConfig{count: 1, size: 16, pattern: "^DiskRead$", diskDir: dir})
	if err != context.Canceled {
		t.Fatalf("runSuite() error = %v, want context.Canceled", err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "rut-bench-*"))
	if err != nil || len(files) != 0 {
		t.Fatalf("temporary files after cancellation = %v, %v", files, err)
	}
}

func TestBenchCPUCountsToN(t *testing.T) {
	const n = 10

	result, err := benchCPU(context.Background(), n, false)
	if err != nil {
		t.Fatalf("benchCPU() error = %v", err)
	}
	if result.N <= 0 {
		t.Fatalf("benchCPU() iterations = %d, want positive", result.N)
	}
	if cpuCountResult != n {
		t.Fatalf("benchCPU() count = %d, want %d", cpuCountResult, n)
	}
}

func TestBenchCPUMulticoreCountsToNPerWorker(t *testing.T) {
	const (
		n       = 10
		workers = 2
	)

	result, err := benchCPUMulticore(context.Background(), n, workers, false)
	if err != nil {
		t.Fatalf("benchCPUMulticore() error = %v", err)
	}
	if result.N <= 0 {
		t.Fatalf("benchCPUMulticore() iterations = %d, want positive", result.N)
	}
	if len(cpuMulticoreResults) != workers {
		t.Fatalf("benchCPUMulticore() results = %v, want %d workers", cpuMulticoreResults, workers)
	}
	for worker, count := range cpuMulticoreResults {
		if count != n {
			t.Errorf("benchCPUMulticore() worker %d count = %d, want %d", worker, count, n)
		}
	}
}

func TestPrintBenchmarkPreamble(t *testing.T) {
	var output bytes.Buffer
	printBenchmarkPreamble(&output)

	for _, want := range []string{
		"goos: " + runtime.GOOS + "\n",
		"goarch: " + runtime.GOARCH + "\n",
		"pkg: " + packagePath + "\n",
		"cpu: ",
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("preamble %q does not contain %q", output.String(), want)
		}
	}
}

func TestBenchCPUWithCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := benchCPU(ctx, 10, false); err != context.Canceled {
		t.Fatalf("benchCPU() error = %v, want %v", err, context.Canceled)
	}
}

func TestBenchCPUMulticoreWithCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := benchCPUMulticore(ctx, 10, 2, false); err != context.Canceled {
		t.Fatalf("benchCPUMulticore() error = %v, want %v", err, context.Canceled)
	}
}

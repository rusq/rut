package bench

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
	for _, unwanted := range []string{"BenchmarkMemoryCopy", "BenchmarkDiskWrite", "BenchmarkDiskRead"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("output unexpectedly contains %q", unwanted)
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

package bench

import (
	"bytes"
	"context"
	"runtime"
	"strings"
	"testing"
)

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

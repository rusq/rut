package bench

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/rusq/rut/cmd/rut/internal/cfg"
	"github.com/rusq/rut/cmd/rut/internal/golang/base"
)

const defaultCount int64 = 1_000_000
const packagePath = "github.com/rusq/rut/cmd/rut/internal/bench"

var countTo int64
var benchMem bool

var CmdBench = &base.Command{
	Run:        runBench,
	UsageLine:  "rut bench [flags]",
	Short:      "runs simple benchmarks of the system",
	FlagMask:   cfg.OmitSomeFlag,
	PrintFlags: true,
	Long: `
Bench runs a CPU benchmark that counts from zero to N. The benchmark runner
automatically chooses how many times to repeat that work.
`,
}

func init() {
	CmdBench.Flag.Int64Var(&countTo, "n", defaultCount, "count from zero to N in each benchmark iteration")
	CmdBench.Flag.BoolVar(&benchMem, "benchmem", false, "print memory allocation statistics")
}

func runBench(ctx context.Context, cmd *base.Command, args []string) error {
	if len(args) > 0 {
		base.SetExitStatus(base.SInvalidParameters)
		return fmt.Errorf("unexpected arguments: %v", args)
	}
	if countTo < 0 {
		base.SetExitStatus(base.SInvalidParameters)
		return fmt.Errorf("-n must be non-negative")
	}

	printBenchmarkPreamble(os.Stdout)
	result, err := benchCPU(ctx, countTo, benchMem)
	if err != nil {
		return err
	}
	fmt.Printf("BenchmarkCPUCount-%d\t%s", countTo, result)
	if benchMem {
		fmt.Printf("\t%s", result.MemString())
	}
	fmt.Println()
	return nil
}

func printBenchmarkPreamble(w io.Writer) {
	fmt.Fprintf(w, "goos: %s\n", runtime.GOOS)
	fmt.Fprintf(w, "goarch: %s\n", runtime.GOARCH)
	fmt.Fprintf(w, "pkg: %s\n", packagePath)
	fmt.Fprintf(w, "cpu: %s\n", cpuName())
}

func cpuName() string {
	if runtime.GOOS == "darwin" {
		for _, key := range []string{"machdep.cpu.brand_string", "hw.model"} {
			if out, err := exec.Command("sysctl", "-n", key).Output(); err == nil {
				if name := strings.TrimSpace(string(out)); name != "" {
					return name
				}
			}
		}
	}
	if runtime.GOOS == "linux" {
		if data, err := os.ReadFile("/proc/cpuinfo"); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				key, value, ok := strings.Cut(line, ":")
				if ok && (strings.TrimSpace(key) == "model name" || strings.TrimSpace(key) == "Hardware") {
					if name := strings.TrimSpace(value); name != "" {
						return name
					}
				}
			}
		}
	}
	if name := strings.TrimSpace(os.Getenv("PROCESSOR_IDENTIFIER")); name != "" {
		return name
	}
	return runtime.GOARCH
}

var cpuCountResult int64

func benchCPU(ctx context.Context, n int64, reportAllocs bool) (testing.BenchmarkResult, error) {
	var canceled bool
	result := testing.Benchmark(func(b *testing.B) {
		if reportAllocs {
			b.ReportAllocs()
		}
		var count int64
		for i := 0; i < b.N; i++ {
			if ctx.Err() != nil {
				canceled = true
				break
			}
			for count = 0; count < n; count++ {
			}
		}
		cpuCountResult = count
	})
	if canceled {
		return result, ctx.Err()
	}
	return result, nil
}

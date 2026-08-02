package bench

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/rusq/rut/cmd/rut/internal/cfg"
	"github.com/rusq/rut/cmd/rut/internal/golang/base"
)

const (
	defaultCount  int64 = 1_000_000
	defaultSize         = int64(64 << 20)
	packagePath         = "github.com/rusq/rut/cmd/rut/internal/bench"
	diskChunkSize       = int64(1 << 20)
)

var (
	countTo    int64
	benchMem   bool
	runPattern string
	benchSize  = byteSize(defaultSize)
	diskDir    string
)

var CmdBench = &base.Command{
	Run: runBench, UsageLine: "rut bench [flags]", Short: "runs CPU, memory, and disk benchmarks",
	FlagMask: cfg.OmitSomeFlag, PrintFlags: true,
	Long: `
Bench runs single-core and multicore CPU counting, memory-copy, synced
sequential-disk-write, and buffered sequential-disk-read benchmarks. The
multicore benchmark uses GOMAXPROCS workers. Use -run to select benchmark
names. Disk reads may be served by the operating system page cache. Disk
benchmarks use a uniquely named temporary file in -disk-dir and remove it when
finished, including after errors or cancellation. CPU results include parrots,
where one parrot is one million completed counter increments per second.

Examples:
  rut bench
  rut bench -run 'Memory|Disk' -size 256MiB
  rut bench -run DiskRead -disk-dir /path/to/disk
`,
}

func init() {
	CmdBench.Flag.Int64Var(&countTo, "n", defaultCount, "count from zero to N per worker in each CPU benchmark iteration")
	CmdBench.Flag.BoolVar(&benchMem, "benchmem", false, "print memory allocation statistics for every selected benchmark")
	CmdBench.Flag.StringVar(&runPattern, "run", ".", "run only benchmarks with names matching regexp")
	CmdBench.Flag.Var(&benchSize, "size", "memory buffer and disk file size in bytes, KiB, MiB, or GiB")
	CmdBench.Flag.StringVar(&diskDir, "disk-dir", os.TempDir(), "directory for the temporary disk benchmark file")
}

type byteSize int64

func (s *byteSize) String() string { return formatSize(int64(*s)) }
func (s *byteSize) Set(v string) error {
	n, err := parseSize(v)
	if err != nil {
		return err
	}
	*s = byteSize(n)
	return nil
}

func parseSize(v string) (int64, error) {
	mult := int64(1)
	num := v
	for _, suffix := range []struct {
		name string
		mult int64
	}{{"KiB", 1 << 10}, {"MiB", 1 << 20}, {"GiB", 1 << 30}} {
		if before, ok := strings.CutSuffix(v, suffix.name); ok {
			num, mult = before, suffix.mult
			break
		}
	}
	n, err := strconv.ParseInt(num, 10, 64)
	if err != nil || n <= 0 || n > int64(^uint64(0)>>1)/mult {
		return 0, fmt.Errorf("invalid size %q: must be a positive byte count with optional KiB, MiB, or GiB suffix", v)
	}
	return n * mult, nil
}

func formatSize(n int64) string {
	for _, unit := range []struct {
		n int64
		s string
	}{{1 << 30, "GiB"}, {1 << 20, "MiB"}, {1 << 10, "KiB"}} {
		if n%unit.n == 0 && n >= unit.n {
			return fmt.Sprintf("%d%s", n/unit.n, unit.s)
		}
	}
	return strconv.FormatInt(n, 10)
}

type suiteConfig struct {
	count, size      int64
	benchMem         bool
	sizeExplicit     bool
	pattern, diskDir string
}

func runBench(ctx context.Context, cmd *base.Command, args []string) error {
	if len(args) > 0 {
		return invalid(fmt.Errorf("unexpected arguments: %v", args))
	}
	if countTo < 0 {
		return invalid(fmt.Errorf("-n must be non-negative"))
	}
	if int64(benchSize) <= 0 {
		return invalid(fmt.Errorf("-size must be positive"))
	}
	sizeExplicit := false
	cmd.Flag.Visit(func(f *flag.Flag) {
		if f.Name == "size" {
			sizeExplicit = true
		}
	})
	err := runSuite(ctx, os.Stdout, suiteConfig{countTo, int64(benchSize), benchMem, sizeExplicit, runPattern, diskDir})
	if err != nil {
		if _, ok := err.(regexpError); ok {
			return invalid(err)
		}
		base.SetExitStatus(base.SGenericError)
	}
	return err
}

// regexpError lets runBench distinguish selector errors from runtime failures.
type regexpError struct{ error }

func (e regexpError) Error() string { return e.error.Error() }
func invalid(err error) error       { base.SetExitStatus(base.SInvalidParameters); return err }

func runSuite(ctx context.Context, w io.Writer, c suiteConfig) error {
	re, err := regexp.Compile(c.pattern)
	if err != nil {
		return regexpError{fmt.Errorf("invalid -run regexp: %w", err)}
	}
	names := []string{"CPUCount", "CPUMulticore", "MemoryCopy", "DiskWrite", "DiskRead"}
	selected := make([]bool, len(names))
	any, disk := false, false
	for i, name := range names {
		selected[i] = re.MatchString(name)
		any = any || selected[i]
		disk = disk || ((name == "DiskWrite" || name == "DiskRead") && selected[i])
	}
	if !any {
		return regexpError{fmt.Errorf("-run %q matches no benchmarks", c.pattern)}
	}

	var f *os.File
	if disk {
		f, err = os.CreateTemp(c.diskDir, "rut-bench-*")
		if err != nil {
			return fmt.Errorf("create disk benchmark file: %w", err)
		}
		name := f.Name()
		defer os.Remove(name)
		defer f.Close()
	}
	diskSize := c.size
	var diskNotice string
	if disk {
		diskSize, diskNotice, err = resolveDiskSize(f.Name(), c.size, c.sizeExplicit)
		if err != nil {
			return err
		}
	}
	printBenchmarkPreamble(w)
	if diskNotice != "" {
		fmt.Fprintln(w, diskNotice)
	}
	for i, name := range names {
		if !selected[i] {
			continue
		}
		var result testing.BenchmarkResult
		workers := 0
		switch name {
		case "CPUCount":
			result, err = benchCPU(ctx, c.count, c.benchMem)
		case "CPUMulticore":
			workers = runtime.GOMAXPROCS(0)
			result, err = benchCPUMulticore(ctx, c.count, workers, c.benchMem)
		case "MemoryCopy":
			result, err = benchMemory(ctx, c.size, c.benchMem)
		case "DiskWrite":
			result, err = benchDiskWrite(ctx, f, diskSize, c.benchMem)
		case "DiskRead":
			result, err = benchDiskRead(ctx, f, diskSize, c.benchMem)
		}
		if err != nil {
			return err
		}
		resultSize := c.size
		if name == "DiskWrite" || name == "DiskRead" {
			resultSize = diskSize
		}
		label := fmt.Sprintf("Benchmark%s-%s", name, formatSize(resultSize))
		switch name {
		case "CPUCount":
			result.Extra = withCPUParrots(result.Extra, result, c.count, 1)
			label = fmt.Sprintf("BenchmarkCPUCount-%d", c.count)
		case "CPUMulticore":
			result.Extra = withCPUParrots(result.Extra, result, c.count, workers)
			label = fmt.Sprintf("BenchmarkCPUMulticore-%d-%dworkers", c.count, workers)
		}
		fmt.Fprintf(w, "%s\t%s", label, result)
		if c.benchMem {
			fmt.Fprintf(w, "\t%s", result.MemString())
		}
		fmt.Fprintln(w)
	}
	return nil
}

func withCPUParrots(extra map[string]float64, result testing.BenchmarkResult, count int64, workers int) map[string]float64 {
	if extra == nil {
		extra = make(map[string]float64)
	}
	if result.N <= 0 || result.T <= 0 || count <= 0 || workers <= 0 {
		extra["parrots"] = 0
		return extra
	}
	extra["parrots"] = float64(result.N) * float64(count) * float64(workers) / result.T.Seconds() / 1_000_000
	return extra
}

func printBenchmarkPreamble(w io.Writer) {
	fmt.Fprintf(w, "goos: %s\ngoarch: %s\npkg: %s\ncpu: %s\n", runtime.GOOS, runtime.GOARCH, packagePath, cpuName())
}

func cpuName() string {
	if runtime.GOOS == "darwin" {
		for _, key := range []string{"machdep.cpu.brand_string", "hw.model"} {
			if out, err := exec.Command("sysctl", "-n", key).Output(); err == nil && strings.TrimSpace(string(out)) != "" {
				return strings.TrimSpace(string(out))
			}
		}
	}
	if runtime.GOOS == "linux" {
		if data, err := os.ReadFile("/proc/cpuinfo"); err == nil {
			for line := range strings.SplitSeq(string(data), "\n") {
				key, value, ok := strings.Cut(line, ":")
				if ok && (strings.TrimSpace(key) == "model name" || strings.TrimSpace(key) == "Hardware") && strings.TrimSpace(value) != "" {
					return strings.TrimSpace(value)
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
var cpuMulticoreResults []int64

func benchCPU(ctx context.Context, n int64, allocs bool) (testing.BenchmarkResult, error) {
	var canceled bool
	r := testing.Benchmark(func(b *testing.B) {
		if allocs {
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
		return r, ctx.Err()
	}
	return r, nil
}

func benchCPUMulticore(ctx context.Context, n int64, workers int, allocs bool) (testing.BenchmarkResult, error) {
	if workers <= 0 {
		return testing.BenchmarkResult{}, fmt.Errorf("multicore benchmark requires at least one worker")
	}

	var canceled bool
	r := testing.Benchmark(func(b *testing.B) {
		if allocs {
			b.ReportAllocs()
		}

		results := make([]int64, workers)
		cancellations := make([]bool, workers)
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(workers)
		for worker := range workers {
			go func() {
				defer wg.Done()
				<-start
				var count int64
				for i := 0; i < b.N; i++ {
					if ctx.Err() != nil {
						cancellations[worker] = true
						return
					}
					for count = 0; count < n; count++ {
					}
				}
				results[worker] = count
			}()
		}

		b.ResetTimer()
		close(start)
		wg.Wait()
		b.StopTimer()

		for _, wasCanceled := range cancellations {
			canceled = canceled || wasCanceled
		}
		cpuMulticoreResults = append(cpuMulticoreResults[:0], results...)
	})
	if canceled {
		return r, ctx.Err()
	}
	return r, nil
}

func benchMemory(ctx context.Context, size int64, allocs bool) (testing.BenchmarkResult, error) {
	src, dst := make([]byte, int(size)), make([]byte, int(size))
	for i := range src {
		src[i] = byte(i)
	}
	var canceled bool
	r := testing.Benchmark(func(b *testing.B) {
		if allocs {
			b.ReportAllocs()
		}
		b.SetBytes(size)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if ctx.Err() != nil {
				canceled = true
				break
			}
			copy(dst, src)
		}
	})
	runtime.KeepAlive(dst)
	if canceled {
		return r, ctx.Err()
	}
	return r, nil
}

func eachChunk(ctx context.Context, size int64, fn func([]byte) error) error {
	buf := make([]byte, min(size, diskChunkSize))
	for i := range buf {
		buf[i] = byte(i)
	}
	return eachChunkBuffer(ctx, size, buf, fn)
}

func eachChunkBuffer(ctx context.Context, size int64, buf []byte, fn func([]byte) error) error {
	for left := size; left > 0; {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := min(left, int64(len(buf)))
		if err := fn(buf[:n]); err != nil {
			return err
		}
		left -= n
	}
	return nil
}

func benchDiskWrite(ctx context.Context, f *os.File, size int64, allocs bool) (testing.BenchmarkResult, error) {
	buf := make([]byte, min(size, diskChunkSize))
	for i := range buf {
		buf[i] = byte(i)
	}
	var runErr error
	r := testing.Benchmark(func(b *testing.B) {
		if allocs {
			b.ReportAllocs()
		}
		b.SetBytes(size)
		for i := 0; i < b.N && runErr == nil; i++ {
			if _, runErr = f.Seek(0, 0); runErr != nil {
				break
			}
			runErr = eachChunkBuffer(ctx, size, buf, func(p []byte) error {
				for len(p) > 0 {
					n, err := f.Write(p)
					if err != nil {
						return err
					}
					if n == 0 {
						return io.ErrShortWrite
					}
					p = p[n:]
				}
				return nil
			})
			if runErr == nil {
				runErr = f.Sync()
			}
		}
	})
	return r, runErr
}

func prepareReadFile(ctx context.Context, f *os.File, size int64) error {
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.Seek(0, 0); err != nil {
		return err
	}
	if err := eachChunk(ctx, size, func(p []byte) error { _, e := f.Write(p); return e }); err != nil {
		return err
	}
	return f.Sync()
}

func benchDiskRead(ctx context.Context, f *os.File, size int64, allocs bool) (testing.BenchmarkResult, error) {
	if err := prepareReadFile(ctx, f, size); err != nil {
		return testing.BenchmarkResult{}, err
	}
	buf := make([]byte, min(size, diskChunkSize))
	var runErr error
	r := testing.Benchmark(func(b *testing.B) {
		if allocs {
			b.ReportAllocs()
		}
		b.SetBytes(size)
		for i := 0; i < b.N && runErr == nil; i++ {
			if _, runErr = f.Seek(0, 0); runErr != nil {
				break
			}
			left := size
			for left > 0 {
				if runErr = ctx.Err(); runErr != nil {
					break
				}
				n := min(left, int64(len(buf)))
				_, runErr = io.ReadFull(f, buf[:n])
				if runErr != nil {
					break
				}
				left -= n
			}
		}
	})
	return r, runErr
}

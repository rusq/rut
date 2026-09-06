package sdbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rusq/rut/cmd/rut/internal/cfg"
	"github.com/rusq/rut/cmd/rut/internal/golang/base"
)

const (
	defaultSize          int64 = 4 << 30
	minimumSize          int64 = 1 << 20
	sequentialBlock      int64 = 1 << 20
	randomBlock          int64 = 4 << 10
	defaultRandomTime          = 10 * time.Second
	defaultSyncOps             = 100
	defaultMetadataFiles       = 1000
	maxLatencySamples          = 100000
)

var (
	cleanupTimeout             = 5 * time.Second
	sequentialProgressInterval = 5 * time.Second
)

var (
	sizeFlag            = "auto"
	randomTime          = defaultRandomTime
	syncOps             = defaultSyncOps
	metadataFiles       = defaultMetadataFiles
	seed          int64 = 1
	jsonOutput    bool
)

var CmdSDBench = &base.Command{
	Run:        run,
	UsageLine:  "rut sdbench [flags] <mount-dir>",
	Short:      "runs a verified, non-destructive SD-card filesystem benchmark",
	FlagMask:   cfg.OmitSomeFlag,
	PrintFlags: true,
	Long: `
SDBench tests free space inside a mounted SD-card directory. It creates a
private temporary workspace, writes only files inside that workspace, verifies
the data after reading it, and removes the workspace when finished. Existing
files are never opened or changed. The test still consumes flash write cycles
and temporarily uses free space.

The default size is the smaller of 4 GiB and 75% of currently available space.
Use -size all to test 90% of available space, or provide a byte size with an
optional KiB, MiB, or GiB suffix. Results are measurements only; they are not
an SD Association speed-class certification. Reads may still be served by
device or filesystem caches, and the report identifies the cache hint used.

Examples:
  rut sdbench /Volumes/CARD
  rut sdbench -size 512MiB /media/card
  rut sdbench -size all -json /media/card
`,
}

func init() {
	CmdSDBench.Flag.StringVar(&sizeFlag, "size", sizeFlag, "test size: auto, all, or a positive byte count with KiB, MiB, or GiB suffix")
	CmdSDBench.Flag.DurationVar(&randomTime, "random-duration", defaultRandomTime, "duration of each random read and write phase")
	CmdSDBench.Flag.IntVar(&syncOps, "sync-ops", defaultSyncOps, "number of 4 KiB write-and-sync latency operations")
	CmdSDBench.Flag.IntVar(&metadataFiles, "metadata-files", defaultMetadataFiles, "number of small files in the metadata phase")
	CmdSDBench.Flag.Int64Var(&seed, "seed", 1, "deterministic data and random-operation seed")
	CmdSDBench.Flag.BoolVar(&jsonOutput, "json", false, "write a versioned JSON report")
}

type sizeMode struct{ value int64 }

func parseSizeSpec(s string, available uint64) (sizeMode, error) {
	if s == "auto" || s == "" {
		want := defaultSize
		threeQuarters := available / 4 * 3
		if uint64(want) > threeQuarters {
			if threeQuarters > uint64(^uint64(0)>>1) {
				want = int64(^uint64(0) >> 1)
			} else {
				want = int64(threeQuarters)
			}
		}
		return usableSize(want, available)
	}
	if s == "all" {
		nineTenths := available / 10 * 9
		if nineTenths > uint64(^uint64(0)>>1) {
			nineTenths = uint64(^uint64(0) >> 1)
		}
		return usableSize(int64(nineTenths), available)
	}
	n, err := parseSize(s)
	if err != nil {
		return sizeMode{}, err
	}
	return usableSize(n, available)
}

func usableSize(n int64, available uint64) (sizeMode, error) {
	if n < minimumSize {
		return sizeMode{}, fmt.Errorf("test size %s is below the 1MiB minimum", formatSize(n))
	}
	reserve := uint64(1 << 20)
	if available < reserve || uint64(n) > available-reserve {
		return sizeMode{}, fmt.Errorf("test size %s does not fit in available space (%s, retaining a 1MiB reserve)", formatSize(n), formatSizeU(available))
	}
	n -= n % randomBlock
	if n < minimumSize {
		return sizeMode{}, errors.New("available space is too small for a 1MiB test")
	}
	return sizeMode{value: n}, nil
}

func parseSize(s string) (int64, error) {
	mult := int64(1)
	number := s
	for _, item := range []struct {
		name string
		mult int64
	}{{"KiB", 1 << 10}, {"MiB", 1 << 20}, {"GiB", 1 << 30}} {
		if strings.HasSuffix(s, item.name) {
			number, mult = strings.TrimSuffix(s, item.name), item.mult
			break
		}
	}
	n, err := strconv.ParseInt(number, 10, 64)
	if err != nil || n <= 0 || n > int64(^uint64(0)>>1)/mult {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	return n * mult, nil
}

func formatSize(n int64) string {
	for _, u := range []struct {
		n int64
		s string
	}{{1 << 30, "GiB"}, {1 << 20, "MiB"}, {1 << 10, "KiB"}} {
		if n >= u.n && n%u.n == 0 {
			return fmt.Sprintf("%d%s", n/u.n, u.s)
		}
	}
	return strconv.FormatInt(n, 10)
}
func formatSizeU(n uint64) string {
	if n > uint64(^uint64(0)>>1) {
		return formatSize(int64(^uint64(0) >> 1))
	}
	return formatSize(int64(n))
}

type report struct {
	SchemaVersion int           `json:"schema_version"`
	Status        string        `json:"status"`
	Error         string        `json:"error,omitempty"`
	Target        targetReport  `json:"target"`
	Config        configReport  `json:"config"`
	CacheControl  string        `json:"cache_control"`
	Warnings      []string      `json:"warnings,omitempty"`
	Phases        []phaseReport `json:"phases"`
}
type targetReport struct {
	RequestedPath   string `json:"requested_path,omitempty"`
	ResolvedPath    string `json:"resolved_path,omitempty"`
	MountPoint      string `json:"mount_point,omitempty"`
	Device          string `json:"device,omitempty"`
	Filesystem      string `json:"filesystem,omitempty"`
	TotalBytes      uint64 `json:"total_bytes,omitempty"`
	AvailableBefore uint64 `json:"available_before"`
	AvailableAfter  uint64 `json:"available_after,omitempty"`
}
type configReport struct {
	SizeBytes      int64  `json:"size_bytes"`
	RandomDuration string `json:"random_duration"`
	SyncOps        int    `json:"sync_ops"`
	MetadataFiles  int    `json:"metadata_files"`
	Seed           int64  `json:"seed"`
}
type phaseReport struct {
	Name                     string        `json:"name"`
	Bytes                    int64         `json:"bytes,omitempty"`
	Operations               int64         `json:"operations,omitempty"`
	DurationNS               int64         `json:"duration_ns"`
	ThroughputBytesPerSecond float64       `json:"throughput_bytes_per_second,omitempty"`
	IOPS                     float64       `json:"iops,omitempty"`
	LatencyNS                latencyReport `json:"latency_ns,omitempty"`
	VerifiedBytes            int64         `json:"verified_bytes,omitempty"`
}
type latencyReport struct {
	P50 int64 `json:"p50,omitempty"`
	P95 int64 `json:"p95,omitempty"`
	P99 int64 `json:"p99,omitempty"`
	Max int64 `json:"max,omitempty"`
}

func run(ctx context.Context, _ *base.Command, args []string) error {
	if len(args) != 1 {
		base.SetExitStatus(base.SInvalidParameters)
		return fmt.Errorf("usage requires exactly one mount directory")
	}
	if randomTime <= 0 || syncOps <= 0 || metadataFiles <= 0 {
		base.SetExitStatus(base.SInvalidParameters)
		return errors.New("random duration, sync operations, and metadata files must be positive")
	}
	r, err := execute(ctx, args[0], sizeFlag, randomTime, syncOps, metadataFiles, seed)
	if jsonOutput {
		encErr := json.NewEncoder(os.Stdout).Encode(r)
		if err == nil {
			err = encErr
		}
	} else {
		printReport(os.Stdout, r)
	}
	if err != nil {
		base.SetExitStatus(base.SGenericError)
	}
	return err
}

func execute(ctx context.Context, requested, sizeText string, randomDuration time.Duration, syncCount, fileCount int, randomSeed int64) (r report, err error) {
	r.SchemaVersion, r.Status = 1, "failed"
	r.Target.RequestedPath = requested
	resolved, err := filepath.EvalSymlinks(requested)
	if err != nil {
		r.Error = err.Error()
		return r, fmt.Errorf("resolve mount directory: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		if err == nil {
			err = errors.New("path is not a directory")
		}
		r.Error = err.Error()
		return r, fmt.Errorf("invalid mount directory: %w", err)
	}
	r.Target.ResolvedPath = resolved
	vol, err := inspectVolume(resolved)
	if err != nil {
		r.Error = err.Error()
		return r, fmt.Errorf("inspect volume: %w", err)
	}
	r.Target.MountPoint, r.Target.Device, r.Target.Filesystem = vol.MountPoint, vol.Device, vol.Filesystem
	r.Target.TotalBytes, r.Target.AvailableBefore = vol.TotalBytes, vol.AvailableBytes
	sz, err := parseSizeSpec(sizeText, vol.AvailableBytes)
	if err != nil {
		r.Error = err.Error()
		return r, err
	}
	r.Config = configReport{SizeBytes: sz.value, RandomDuration: randomDuration.String(), SyncOps: syncCount, MetadataFiles: fileCount, Seed: randomSeed}
	r.CacheControl = cacheMethod()
	cfg.Log.Info("sdbench started", "target", resolved, "size_bytes", sz.value)
	if r.CacheControl == "uncontrolled" {
		r.Warnings = append(r.Warnings, "the platform cannot request cache eviction; read results may include filesystem cache")
	}
	workspace, err := os.MkdirTemp(resolved, ".rut-sdbench-*")
	if err != nil {
		r.Error = err.Error()
		return r, fmt.Errorf("create private workspace: %w", err)
	}
	defer func() {
		if e := runCleanup(func() error { return os.RemoveAll(workspace) }); e != nil {
			r.Warnings = append(r.Warnings, "could not remove temporary workspace: "+e.Error())
			if err == nil {
				err = fmt.Errorf("remove temporary workspace: %w", e)
				r.Error = err.Error()
				r.Status = "failed"
			}
		}
	}()
	dataPath := filepath.Join(workspace, "data.bin")
	phaseCount := 0
	logCompleted := func() { logCompletedPhases(&r, &phaseCount) }
	cfg.Log.Info("sdbench phase started", "phase", "sequential_write")
	if err = phaseSequential(ctx, dataPath, sz.value, randomSeed, &r); err != nil {
		r.Error = err.Error()
		return r, err
	}
	logCompleted()
	cfg.Log.Info("sdbench phase started", "phase", "random_read")
	if err = phaseRandomRead(ctx, dataPath, sz.value, randomDuration, randomSeed, &r); err != nil {
		r.Error = err.Error()
		return r, err
	}
	logCompleted()
	writeSeed := randomSeed + 1
	syncSeed := randomSeed + 2
	cfg.Log.Info("sdbench phase started", "phase", "random_write")
	writeState, err := phaseRandomWrite(ctx, dataPath, sz.value, randomDuration, writeSeed, &r)
	if err != nil {
		r.Error = err.Error()
		return r, err
	}
	logCompleted()
	cfg.Log.Info("sdbench phase started", "phase", "sync_latency")
	if err = phaseSync(ctx, dataPath, sz.value, syncCount, syncSeed, writeState, &r); err != nil {
		r.Error = err.Error()
		return r, err
	}
	logCompleted()
	cfg.Log.Info("sdbench phase started", "phase", "final_read_verify")
	if err = phaseVerify(ctx, dataPath, sz.value, randomSeed, writeSeed, syncSeed, writeState, &r); err != nil {
		r.Error = err.Error()
		return r, err
	}
	logCompleted()
	if err = runCleanup(func() error { return os.Remove(dataPath) }); err != nil {
		r.Error = err.Error()
		return r, fmt.Errorf("remove data file: %w", err)
	}
	cfg.Log.Info("sdbench phase started", "phase", "metadata_create")
	if err = phaseMetadata(ctx, workspace, fileCount, randomSeed, &r); err != nil {
		r.Error = err.Error()
		return r, err
	}
	logCompleted()
	if after, e := inspectVolume(resolved); e == nil {
		r.Target.AvailableAfter = after.AvailableBytes
	}
	r.Status = "passed"
	cfg.Log.Info("sdbench completed", "target", resolved)
	return r, nil
}

type cleanupTimeoutError struct{ timeout time.Duration }

func (e cleanupTimeoutError) Error() string { return "cleanup timed out after " + e.timeout.String() }

func runCleanup(fn func() error) error {
	done := make(chan error, 1)
	go func() { done <- fn() }()
	timer := time.NewTimer(cleanupTimeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		return cleanupTimeoutError{cleanupTimeout}
	}
}

func closeDataFile(r *report, f *os.File) error {
	if err := runCleanup(f.Close); err != nil {
		r.Warnings = append(r.Warnings, "could not close benchmark data file: "+err.Error())
		return fmt.Errorf("close benchmark data file: %w", err)
	}
	return nil
}

func logCompletedPhases(r *report, phaseCount *int) {
	for *phaseCount < len(r.Phases) {
		p := r.Phases[*phaseCount]
		cfg.Log.Info("sdbench phase completed", "phase", p.Name, "bytes", p.Bytes, "operations", p.Operations)
		*phaseCount++
	}
}

func logSequentialProgress(phase string, completed, total int64) {
	percentage := float64(completed) * 100 / float64(total)
	cfg.Log.Info("sdbench progress", "phase", phase, "completed_bytes", completed, "total_bytes", total, "percentage", percentage)
}

func patternFill(buf []byte, offset int64, seed int64) {
	for i := range buf {
		x := uint64(seed) ^ uint64(offset+int64(i))*0x9e3779b97f4a7c15
		x ^= x >> 30
		x *= 0xbf58476d1ce4e5b9
		x ^= x >> 27
		x *= 0x94d049bb133111eb
		x ^= x >> 31
		buf[i] = byte(x >> 56)
	}
}
func checkPattern(buf []byte, offset, seed int64) error {
	want := make([]byte, len(buf))
	patternFill(want, offset, seed)
	for i := range buf {
		if buf[i] != want[i] {
			return fmt.Errorf("data verification failed at byte %d (expected 0x%02x, got 0x%02x)", offset+int64(i), want[i], buf[i])
		}
	}
	return nil
}
func checkContext(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func phaseSequential(ctx context.Context, path string, size, seed int64, r *report) (err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			closeErr := closeDataFile(r, f)
			if err == nil && closeErr != nil {
				err = closeErr
			}
		}
	}()
	start := time.Now()
	nextProgress := start.Add(sequentialProgressInterval)
	buf := make([]byte, sequentialBlock)
	for off := int64(0); off < size; off += int64(len(buf)) {
		if err = checkContext(ctx); err != nil {
			return err
		}
		n := min64(int64(len(buf)), size-off)
		patternFill(buf[:n], off, seed)
		if _, err = f.Write(buf[:n]); err != nil {
			return err
		}
		if time.Now().After(nextProgress) {
			logSequentialProgress("sequential_write", off+n, size)
			nextProgress = time.Now().Add(sequentialProgressInterval)
		}
	}
	if err = f.Sync(); err != nil {
		return err
	}
	elapsed := time.Since(start)
	if err = closeDataFile(r, f); err != nil {
		return err
	}
	closed = true
	r.Phases = append(r.Phases, metric("sequential_write", size, 1, elapsed, latencyReport{}))
	cfg.Log.Info("sdbench phase started", "phase", "sequential_read_verify")
	f, err = os.Open(path)
	if err != nil {
		return err
	}
	closed = false
	if err = prepareCache(f); err != nil {
		return err
	}
	start = time.Now()
	nextProgress = start.Add(sequentialProgressInterval)
	verified := int64(0)
	for off := int64(0); off < size; off += int64(len(buf)) {
		if err = checkContext(ctx); err != nil {
			return err
		}
		n := min64(int64(len(buf)), size-off)
		if _, err = io.ReadFull(f, buf[:n]); err != nil {
			return err
		}
		if err = checkPattern(buf[:n], off, seed); err != nil {
			return err
		}
		verified += n
		if time.Now().After(nextProgress) {
			logSequentialProgress("sequential_read_verify", verified, size)
			nextProgress = time.Now().Add(sequentialProgressInterval)
		}
	}
	if err = closeDataFile(r, f); err != nil {
		return err
	}
	closed = true
	p := metric("sequential_read_verify", size, 1, time.Since(start), latencyReport{})
	p.VerifiedBytes = verified
	r.Phases = append(r.Phases, p)
	return nil
}

func phaseRandomRead(ctx context.Context, path string, size int64, duration time.Duration, seed int64, r *report) (err error) {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := closeDataFile(r, f); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	if err = prepareCache(f); err != nil {
		return err
	}
	return randomPhase(ctx, f, size, duration, seed, r, "random_read")
}

const (
	baseBlock byte = iota
	randomWriteBlock
	syncWriteBlock
)

func phaseRandomWrite(ctx context.Context, path string, size int64, duration time.Duration, seed int64, r *report) (state []byte, err error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := closeDataFile(r, f); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	// One byte per 4KiB block records which deterministic pattern owns the
	// block. This bounds bookkeeping at 1MiB for the default 4GiB test.
	state = make([]byte, size/randomBlock)
	err = randomPhaseWithExpected(ctx, f, size, duration, seed, state, randomWriteBlock, r, "random_write")
	if err != nil {
		return nil, err
	}
	if err = f.Sync(); err != nil {
		return nil, err
	}
	return state, nil
}

func randomPhase(ctx context.Context, f *os.File, size int64, duration time.Duration, seed int64, r *report, name string) error {
	return randomPhaseWithExpected(ctx, f, size, duration, seed, nil, baseBlock, r, name)
}
func randomPhaseWithExpected(ctx context.Context, f *os.File, size int64, duration time.Duration, seed int64, state []byte, stateValue byte, r *report, name string) error {
	if size < randomBlock {
		return errors.New("test size is smaller than the random block")
	}
	rng := rand.New(rand.NewSource(seed))
	buf := make([]byte, randomBlock)
	samples := make([]int64, 0, 4096)
	start := time.Now()
	deadline := time.Now().Add(duration)
	var ops int64
	for time.Now().Before(deadline) {
		if err := checkContext(ctx); err != nil {
			return err
		}
		off := (rng.Int63n(size / randomBlock)) * randomBlock
		opStart := time.Now()
		if state != nil {
			patternFill(buf, off, seed)
			if _, err := f.WriteAt(buf, off); err != nil {
				return err
			}
			state[off/randomBlock] = stateValue
		} else {
			if _, err := f.ReadAt(buf, off); err != nil {
				return err
			}
			if err := checkPattern(buf, off, seed); err != nil {
				return err
			}
		}
		samples = appendSample(samples, time.Since(opStart).Nanoseconds(), ops)
		ops++
	}
	p := metric(name, ops*randomBlock, ops, time.Since(start), latencyReportFor(samples))
	r.Phases = append(r.Phases, p)
	return nil
}

func phaseSync(ctx context.Context, path string, size int64, count int, seed int64, state []byte, r *report) (err error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := closeDataFile(r, f); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	buf := make([]byte, randomBlock)
	samples := make([]int64, 0, count)
	start := time.Now()
	for i := 0; i < count; i++ {
		if err = checkContext(ctx); err != nil {
			return err
		}
		off := int64(i%int(size/randomBlock)) * randomBlock
		patternFill(buf, off, seed)
		opStart := time.Now()
		if _, err = f.WriteAt(buf, off); err != nil {
			return err
		}
		state[off/randomBlock] = syncWriteBlock
		if err = f.Sync(); err != nil {
			return err
		}
		samples = append(samples, time.Since(opStart).Nanoseconds())
	}
	r.Phases = append(r.Phases, metric("sync_latency", int64(count)*randomBlock, int64(count), time.Since(start), latencyReportFor(samples)))
	return nil
}

func phaseVerify(ctx context.Context, path string, size, baseSeed, randomWriteSeed, syncSeed int64, state []byte, r *report) (err error) {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := closeDataFile(r, f); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	if err = prepareCache(f); err != nil {
		return err
	}
	buf := make([]byte, sequentialBlock)
	start := time.Now()
	nextProgress := start.Add(sequentialProgressInterval)
	var verified int64
	for off := int64(0); off < size; off += int64(len(buf)) {
		if err = checkContext(ctx); err != nil {
			return err
		}
		n := min64(int64(len(buf)), size-off)
		if _, err = io.ReadFull(f, buf[:n]); err != nil {
			return err
		}
		want := make([]byte, n)
		patternFill(want, off, baseSeed)
		for block := off; block < off+n; block += randomBlock {
			switch state[block/randomBlock] {
			case randomWriteBlock:
				patternFill(want[block-off:block-off+randomBlock], block, randomWriteSeed)
			case syncWriteBlock:
				patternFill(want[block-off:block-off+randomBlock], block, syncSeed)
			}
		}
		if err = checkBytes(buf[:n], want, off); err != nil {
			return err
		}
		verified += n
		if time.Now().After(nextProgress) {
			logSequentialProgress("final_read_verify", verified, size)
			nextProgress = time.Now().Add(sequentialProgressInterval)
		}
	}
	p := metric("final_read_verify", size, 1, time.Since(start), latencyReport{})
	p.VerifiedBytes = verified
	r.Phases = append(r.Phases, p)
	return nil
}

func checkBytes(got, want []byte, offset int64) error {
	for i := range got {
		if got[i] != want[i] {
			return fmt.Errorf("data verification failed at byte %d (expected 0x%02x, got 0x%02x)", offset+int64(i), want[i], got[i])
		}
	}
	return nil
}

func phaseMetadata(ctx context.Context, dir string, count int, seed int64, r *report) error {
	meta := filepath.Join(dir, "metadata")
	if err := os.Mkdir(meta, 0700); err != nil {
		return err
	}
	buf := make([]byte, randomBlock)
	start := time.Now()
	for i := 0; i < count; i++ {
		if err := checkContext(ctx); err != nil {
			return err
		}
		patternFill(buf, int64(i)*randomBlock, seed)
		if err := os.WriteFile(filepath.Join(meta, fmt.Sprintf("%06d", i)), buf, 0600); err != nil {
			return err
		}
	}
	createElapsed := time.Since(start)
	r.Phases = append(r.Phases, metric("metadata_create", int64(count)*randomBlock, int64(count), createElapsed, latencyReport{}))
	cfg.Log.Info("sdbench phase started", "phase", "metadata_read_verify")
	start = time.Now()
	for i := 0; i < count; i++ {
		if err := checkContext(ctx); err != nil {
			return err
		}
		p := filepath.Join(meta, fmt.Sprintf("%06d", i))
		got, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err = checkPattern(got, int64(i)*randomBlock, seed); err != nil {
			return err
		}
	}
	r.Phases = append(r.Phases, metric("metadata_read_verify", int64(count)*randomBlock, int64(count), time.Since(start), latencyReport{}))
	cfg.Log.Info("sdbench phase started", "phase", "metadata_delete")
	start = time.Now()
	for i := 0; i < count; i++ {
		if err := os.Remove(filepath.Join(meta, fmt.Sprintf("%06d", i))); err != nil {
			return err
		}
	}
	r.Phases = append(r.Phases, metric("metadata_delete", 0, int64(count), time.Since(start), latencyReport{}))
	return nil
}

func metric(name string, bytes, ops int64, elapsed time.Duration, latency latencyReport) phaseReport {
	p := phaseReport{Name: name, Bytes: bytes, Operations: ops, DurationNS: elapsed.Nanoseconds(), LatencyNS: latency}
	if elapsed > 0 {
		p.ThroughputBytesPerSecond = float64(bytes) / elapsed.Seconds()
		p.IOPS = float64(ops) / elapsed.Seconds()
	}
	return p
}
func appendSample(samples []int64, value, index int64) []int64 {
	if len(samples) < maxLatencySamples {
		return append(samples, value)
	}
	slot := (index*1103515245 + 12345) % (index + 1)
	if slot < maxLatencySamples {
		samples[slot] = value
	}
	return samples
}
func latencyReportFor(values []int64) latencyReport {
	if len(values) == 0 {
		return latencyReport{}
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	pick := func(q float64) int64 { return values[int(float64(len(values)-1)*q)] }
	return latencyReport{P50: pick(.50), P95: pick(.95), P99: pick(.99), Max: values[len(values)-1]}
}
func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func printReport(w io.Writer, r report) {
	fmt.Fprintf(w, "SD benchmark: %s\nTarget: %s\nSize: %s\nCache control: %s\n", r.Status, r.Target.ResolvedPath, formatSize(r.Config.SizeBytes), r.CacheControl)
	for _, p := range r.Phases {
		fmt.Fprintf(w, "%-24s %8.2f MiB/s  %8.2f IOPS", p.Name, p.ThroughputBytesPerSecond/(1<<20), p.IOPS)
		if p.LatencyNS.Max > 0 {
			fmt.Fprintf(w, "  p50=%s p95=%s p99=%s max=%s", time.Duration(p.LatencyNS.P50), time.Duration(p.LatencyNS.P95), time.Duration(p.LatencyNS.P99), time.Duration(p.LatencyNS.Max))
		}
		if p.VerifiedBytes > 0 {
			fmt.Fprintf(w, "  verified=%s", formatSize(p.VerifiedBytes))
		}
		fmt.Fprintln(w)
	}
	for _, warning := range r.Warnings {
		fmt.Fprintln(w, "warning:", warning)
	}
	if r.Error != "" {
		fmt.Fprintln(w, "error:", r.Error)
	}
}

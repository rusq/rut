package sdbench

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseSizeSpecScalesSmallCard(t *testing.T) {
	got, err := parseSizeSpec("auto", 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	if got.value != 24<<20 {
		t.Fatalf("auto size = %d, want %d", got.value, 24<<20)
	}
	got, err = parseSizeSpec("all", 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	if got.value != ((32<<20)*9/10)&^(randomBlock-1) {
		t.Fatalf("all size = %d", got.value)
	}
}

func TestParseSizeRejectsReserveAndInvalidValues(t *testing.T) {
	for _, value := range []string{"0", "-1", "1MB", "999999999999999999999GiB"} {
		if _, err := parseSize(value); err == nil {
			t.Errorf("parseSize(%q) succeeded", value)
		}
	}
	if _, err := parseSizeSpec("2MiB", 2<<20); err == nil {
		t.Fatal("size without reserve was accepted")
	}
}

func TestExecuteSmallVerifiedSuiteCleansUp(t *testing.T) {
	dir := t.TempDir()
	r, err := execute(context.Background(), dir, "1MiB", time.Millisecond, 1, 2, 42)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "passed" || len(r.Phases) != 9 {
		t.Fatalf("status/phases = %q/%d", r.Status, len(r.Phases))
	}
	if r.Target.AvailableAfter == 0 {
		t.Fatal("missing post-test free-space measurement")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary entries remain: %v", entries)
	}
	encoded, err := json.Marshal(r)
	if err != nil || !strings.Contains(string(encoded), `"schema_version":1`) {
		t.Fatalf("invalid JSON report: %s (%v)", encoded, err)
	}
}

func TestExecuteCancellationCleansWorkspace(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, err := execute(ctx, dir, "1MiB", time.Second, 1, 1, 1)
	if err != context.Canceled {
		t.Fatalf("error = %v, want context canceled", err)
	}
	if r.Status != "failed" {
		t.Fatalf("status = %q", r.Status)
	}
	entries, readErr := os.ReadDir(filepath.Clean(dir))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary entries remain after cancellation: %v", entries)
	}
}

func TestModifiedBlockStateUsesOneBytePerBlock(t *testing.T) {
	const size = 4 << 20
	dir := t.TempDir()
	path := filepath.Join(dir, "data.bin")
	if err := os.WriteFile(path, make([]byte, size), 0600); err != nil {
		t.Fatal(err)
	}
	state, err := phaseRandomWrite(context.Background(), path, size, time.Millisecond, 7, &report{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(state), int(size/randomBlock); got != want {
		t.Fatalf("state length = %d, want one byte for each of %d blocks", got, want)
	}
}

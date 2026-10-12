package main

import (
	"bytes"
	"context"
	"errors"
	"github.com/rusq/rut/cmd/rut/internal/golang/base"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestCancellationSupervisorLogsDeadlineWithoutBlocking(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	interrupts := make(chan os.Signal, 1)
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go superviseCancellation(interrupts, done, cancel, logger, 10*time.Millisecond)

	interrupts <- os.Interrupt
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("supervisor did not cancel the command")
	}
	time.Sleep(30 * time.Millisecond)
	close(done)
	text := logs.String()
	for _, want := range []string{"termination started", "cleanup timed out"} {
		if !strings.Contains(text, want) {
			t.Errorf("log output missing %q: %s", want, text)
		}
	}
}

func TestCancellationSupervisorStopsWhenCommandReturns(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	interrupts := make(chan os.Signal)
	done := make(chan struct{})
	close(done)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go superviseCancellation(interrupts, done, cancel, logger, 10*time.Millisecond)
	time.Sleep(30 * time.Millisecond)
	if ctx.Err() != nil {
		t.Fatal("completed command was cancelled")
	}
	if logs.Len() != 0 {
		t.Fatalf("completed command logged cancellation: %s", logs.String())
	}
}

func TestHEICCommandRegistered(t *testing.T) {
	for _, cmd := range base.CmdRUT.Commands {
		if cmd.Name() == "2heic" {
			if !cmd.Runnable() || !strings.Contains(cmd.Long, "page-0001") {
				t.Fatal("incomplete 2heic registration")
			}
			for _, name := range []string{"quality", "recursive", "encoder"} {
				if cmd.Flag.Lookup(name) == nil {
					t.Errorf("missing flag %s", name)
				}
			}
			return
		}
	}
	t.Fatal("2heic command not registered")
}

func TestHEICCLIHelper(t *testing.T) {
	if os.Getenv("RUT_HEIC_CLI_HELPER") != "1" {
		return
	}
	os.Args = append([]string{"rut"}, strings.Split(os.Getenv("RUT_HEIC_CLI_ARGS"), "\n")...)
	main()
	os.Exit(0)
}

func TestHEICCLIValidationAndHelp(t *testing.T) {
	t.Setenv("RUT_HEIC_CLI_HELPER", "1")
	for _, key := range []string{"TRACE_FILE", "LOG_FILE", "DEBUG", "JSON_LOG"} {
		t.Setenv(key, "")
	}
	dir := t.TempDir()
	for _, tc := range []struct {
		name   string
		args   []string
		status int
		output string
	}{
		{"empty", []string{"2heic", dir}, 0, "0 converted, 0 skipped, 0 failed"},
		{"quality", []string{"2heic", "-quality", "101", dir}, 3, "quality must be"},
		{"malformedFlag", []string{"2heic", "-quality", "no", dir}, 2, "invalid value"},
		{"unknownFlag", []string{"2heic", "-unknown", dir}, 2, "flag provided but not defined"},
		{"encoder", []string{"2heic", "-encoder", "unknown", dir}, 3, "unknown encoder"},
		{"extra", []string{"2heic", dir, dir}, 3, "at most one directory"},
		{"help", []string{"help", "2heic"}, 0, "page-0001.heic"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("RUT_HEIC_CLI_ARGS", strings.Join(tc.args, "\n"))
			cmd := exec.Command(os.Args[0], "-test.run=^TestHEICCLIHelper$")
			out, err := cmd.CombinedOutput()
			status := 0
			if err != nil {
				var exit *exec.ExitError
				if !errors.As(err, &exit) {
					t.Fatal(err)
				}
				status = exit.ExitCode()
			}
			if status != tc.status || !strings.Contains(string(out), tc.output) {
				t.Fatalf("status=%d output=%s", status, out)
			}
		})
	}
}

package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
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

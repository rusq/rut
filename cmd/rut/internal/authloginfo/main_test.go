package authloginfo

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rusq/rut/cmd/rut/internal/golang/base"
)

func TestRunAuthLogInfoReportAndSkippedWarning(t *testing.T) {
	dir := t.TempDir()
	primary := filepath.Join(dir, "auth.log")
	line := "Jan  2 03:04:07 host sshd[10]: Failed password for root from 192.0.2.10 port 6000 ssh2\n"
	if err := os.WriteFile(primary, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(primary+".1.gz", []byte("not gzip"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	restoreCommandState(t, primary, "", &out, &errOut)
	if err := runAuthLogInfo(context.Background(), CmdAuthLogInfo, nil); err != nil {
		t.Fatalf("runAuthLogInfo() error = %v", err)
	}
	for _, want := range []string{"Failed Login Attempts", "192.0.2.10", "Processed Log Lines: 1"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report does not contain %q:\n%s", want, out.String())
		}
	}
	if got := errOut.String(); !strings.Contains(got, "warning: skipped "+primary+".1.gz") {
		t.Errorf("warning output = %q", got)
	}
}

func TestRunAuthLogInfoExitStatusClassification(t *testing.T) {
	// Generic errors have a lower status value than invalid parameters, so
	// these checks remain valid with base's process-wide monotonic status.
	restoreCommandState(t, filepath.Join(t.TempDir(), "missing", "auth.log"), "", &bytes.Buffer{}, &bytes.Buffer{})
	if err := runAuthLogInfo(context.Background(), CmdAuthLogInfo, nil); err == nil {
		t.Fatal("invalid log directory succeeded")
	}
	if got := base.ExitStatus(); got != base.SGenericError {
		t.Fatalf("discovery status = %v, want %v", got, base.SGenericError)
	}

	dir := t.TempDir()
	primary := filepath.Join(dir, "auth.log")
	if err := os.WriteFile(primary, []byte("noise\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	restoreCommandState(t, primary, filepath.Join(dir, "invalid.mmdb"), &bytes.Buffer{}, &bytes.Buffer{})
	if err := os.WriteFile(geoDBPath, []byte("invalid"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runAuthLogInfo(context.Background(), CmdAuthLogInfo, nil); err == nil {
		t.Fatal("invalid GeoIP database succeeded")
	}
	if got := base.ExitStatus(); got != base.SGenericError {
		t.Fatalf("GeoIP status = %v, want %v", got, base.SGenericError)
	}

	if err := runAuthLogInfo(context.Background(), CmdAuthLogInfo, []string{"extra"}); err == nil {
		t.Fatal("unexpected argument succeeded")
	}
	if got := base.ExitStatus(); got != base.SInvalidParameters {
		t.Fatalf("argument status = %v, want %v", got, base.SInvalidParameters)
	}
}

func restoreCommandState(t *testing.T, path, db string, out, errOut *bytes.Buffer) {
	t.Helper()
	oldPath, oldDB, oldOut, oldErr := logPath, geoDBPath, stdout, stderr
	logPath, geoDBPath, stdout, stderr = path, db, out, errOut
	t.Cleanup(func() { logPath, geoDBPath, stdout, stderr = oldPath, oldDB, oldOut, oldErr })
}

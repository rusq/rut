package authloginfo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverLogFilesOrdersRotationsOldestFirst(t *testing.T) {
	dir := t.TempDir()
	paths := []string{
		filepath.Join(dir, "auth.log"),
		filepath.Join(dir, "auth.log.1"),
		filepath.Join(dir, "auth.log.2.gz"),
		filepath.Join(dir, "auth.log.10.gz"),
		filepath.Join(dir, "auth.log.bad"),
		filepath.Join(dir, "auth.log.0"),
	}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	got, err := DiscoverLogFiles(filepath.Join(dir, "auth.log"))
	if err != nil {
		t.Fatalf("DiscoverLogFiles() error = %v", err)
	}

	want := []string{
		filepath.Join(dir, "auth.log.10.gz"),
		filepath.Join(dir, "auth.log.2.gz"),
		filepath.Join(dir, "auth.log.1"),
		filepath.Join(dir, "auth.log"),
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("DiscoverLogFiles() = %v, want %v", got, want)
	}
}

func TestParseFailedAttemptPatterns(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		username string
		ip       string
		port     string
		root     bool
		kind     FailureKind
	}{
		{
			name:     "invalid user line",
			line:     "Jan  2 03:04:05 host sshd[10]: Invalid user admin from 203.0.113.5 port 2222",
			username: "admin",
			ip:       "203.0.113.5",
			port:     "2222",
			root:     false,
			kind:     FailureInvalidUser,
		},
		{
			name:     "failed invalid user password",
			line:     "Jan  2 03:04:06 host sshd[10]: Failed password for invalid user guest from 198.51.100.12 port 22 ssh2",
			username: "guest",
			ip:       "198.51.100.12",
			port:     "22",
			root:     false,
			kind:     FailureInvalidUser,
		},
		{
			name:     "failed root password",
			line:     "Jan  2 03:04:07 host sshd[10]: Failed password for root from 192.0.2.10 port 6000 ssh2",
			username: "root",
			ip:       "192.0.2.10",
			port:     "6000",
			root:     true,
			kind:     FailureBadPassword,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseFailedAttempt(tc.line)
			if !ok {
				t.Fatalf("parseFailedAttempt() did not match")
			}
			if got.Username != tc.username || got.SourceIP != tc.ip || got.SourcePort != tc.port || got.IsRootTarget != tc.root || got.FailureKind != tc.kind {
				t.Fatalf("parseFailedAttempt() = %+v", got)
			}
			if got.Timestamp != tc.line[:15] {
				t.Fatalf("unexpected timestamp: %q", got.Timestamp)
			}
		})
	}
}

func TestParseSuccessfulSession(t *testing.T) {
	line := "Jan  2 04:05:06 host sudo: pam_unix(sudo:session): session opened for user deploy by root(uid=0)"
	got, ok := parseSuccessfulSession(line)
	if !ok {
		t.Fatalf("parseSuccessfulSession() did not match")
	}
	if got.Username != "deploy" || got.SessionType != "sudo" || got.Timestamp != "Jan  2 04:05:06" {
		t.Fatalf("parseSuccessfulSession() = %+v", got)
	}
}

func TestAnalyzeFilesAggregatesEvents(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "auth.log")
	content := strings.Join([]string{
		"Jan  2 03:04:05 host sshd[10]: Failed password for invalid user admin from 203.0.113.5 port 2222 ssh2",
		"Jan  2 03:04:06 host sshd[10]: Failed password for root from 192.0.2.10 port 6000 ssh2",
		"Jan  2 04:05:06 host sudo: pam_unix(sudo:session): session opened for user deploy by root(uid=0)",
		"noise",
	}, "\n") + "\n"
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	analyzer, err := NewAnalyzer("")
	if err != nil {
		t.Fatalf("NewAnalyzer() error = %v", err)
	}

	result, err := analyzer.AnalyzeFiles([]string{logPath})
	if err != nil {
		t.Fatalf("AnalyzeFiles() error = %v", err)
	}

	if result.ProcessedLines != 4 {
		t.Fatalf("ProcessedLines = %d, want 4", result.ProcessedLines)
	}
	if len(result.FailedAttempts) != 2 || len(result.RootFailures) != 1 || len(result.Successful) != 1 {
		t.Fatalf("unexpected counts: %+v", result)
	}
	if result.IPCounts["203.0.113.5"] != 1 || result.IPCounts["192.0.2.10"] != 1 {
		t.Fatalf("unexpected IP counts: %+v", result.IPCounts)
	}
	if result.UsernameCounts["admin"] != 1 || result.UsernameCounts["root"] != 1 {
		t.Fatalf("unexpected username counts: %+v", result.UsernameCounts)
	}
	if result.CountryCounts[UnknownCountry] != 2 {
		t.Fatalf("unexpected country counts: %+v", result.CountryCounts)
	}
}

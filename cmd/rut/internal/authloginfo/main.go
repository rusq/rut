package authloginfo

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/rodaine/table"
	"github.com/rusq/rut/cmd/rut/internal/cfg"
	"github.com/rusq/rut/cmd/rut/internal/golang/base"
)

var (
	logPath   = "/var/log/auth.log"
	geoDBPath string
	stdout    io.Writer = os.Stdout
	stderr    io.Writer = os.Stderr
)

// CmdAuthLogInfo analyzes Linux authentication logs.
var CmdAuthLogInfo = &base.Command{
	Run:        runAuthLogInfo,
	UsageLine:  "rut authloginfo [flags]",
	Short:      "analyzes Linux authentication logs",
	FlagMask:   cfg.OmitAll,
	PrintFlags: true,
	Long: `
Authloginfo analyzes a Linux authentication log and its numbered rotations,
including gzip-compressed rotations. It reports failed and successful login
activity and aggregates failed attempts by IP address, username, and country.

Use -geoip-db with a MaxMind GeoIP2 or GeoLite2 country database to enrich
country totals. Without a database, addresses are grouped under Unknown.

Examples:
  rut authloginfo
  rut authloginfo -log-path /var/log/auth.log
  rut authloginfo -geoip-db /path/to/GeoLite2-Country.mmdb
`,
}

func init() {
	CmdAuthLogInfo.Flag.StringVar(&logPath, "log-path", logPath, "path to the primary auth log")
	CmdAuthLogInfo.Flag.StringVar(&geoDBPath, "geoip-db", "", "optional path to a MaxMind GeoIP2/GeoLite2 .mmdb database")
}

func runAuthLogInfo(_ context.Context, _ *base.Command, args []string) error {
	if len(args) != 0 {
		base.SetExitStatus(base.SInvalidParameters)
		return fmt.Errorf("unexpected arguments: %v", args)
	}

	files, err := DiscoverLogFiles(logPath)
	if err != nil {
		return genericError(fmt.Errorf("discover log files: %w", err))
	}

	analyzer, err := NewAnalyzer(geoDBPath)
	if err != nil {
		return genericError(fmt.Errorf("initialize analyzer: %w", err))
	}
	defer analyzer.Close()

	result, err := analyzer.AnalyzeFiles(files)
	if err != nil {
		return genericError(err)
	}

	renderReport(stdout, result)
	for _, skipped := range result.SkippedFiles {
		fmt.Fprintf(stderr, "warning: skipped %s\n", skipped)
	}
	return nil
}

func genericError(err error) error {
	base.SetExitStatus(base.SGenericError)
	return err
}

func renderReport(w io.Writer, result AnalysisResult) {
	renderFailuresTable(w, "Failed Login Attempts", result.FailedAttempts)
	renderFailuresTable(w, "Failed Login Attempts For Root", result.RootFailures)
	renderSuccessTable(w, result.Successful)
	renderCountTable(w, "IP Address Attempt Counts", "IP Address", result.IPCounts)
	renderCountTable(w, "Username Attempt Counts", "Username", result.UsernameCounts)
	renderCountTable(w, "Country Attempt Counts", "Country", result.CountryCounts)

	fmt.Fprintf(w, "\nSummary Totals\n")
	fmt.Fprintf(w, "Processed Log Lines: %d\n", result.ProcessedLines)
	fmt.Fprintf(w, "Successful Login Attempts: %d\n", len(result.Successful))
	fmt.Fprintf(w, "Failed Login Attempts: %d\n", len(result.FailedAttempts))
	fmt.Fprintf(w, "Failed Login Attempts For Root: %d\n", len(result.RootFailures))
}

func renderFailuresTable(w io.Writer, title string, rows []FailedAttempt) {
	fmt.Fprintf(w, "\n%s\n", title)
	tbl := table.New("Time", "User", "IP Address", "Port").WithWriter(w)
	for _, row := range rows {
		tbl.AddRow(row.Timestamp, row.Username, row.SourceIP, row.SourcePort)
	}
	tbl.Print()
}

func renderSuccessTable(w io.Writer, rows []SuccessfulSession) {
	fmt.Fprintf(w, "\nSuccessful Login Attempts\n")
	tbl := table.New("Time", "User", "Session Type").WithWriter(w)
	for _, row := range rows {
		tbl.AddRow(row.Timestamp, row.Username, row.SessionType)
	}
	tbl.Print()
}

func renderCountTable(w io.Writer, title string, keyColumn string, counts map[string]int) {
	fmt.Fprintf(w, "\n%s\n", title)
	tbl := table.New(keyColumn, "Attempt Count").WithWriter(w)
	for _, row := range sortedCountRows(counts) {
		tbl.AddRow(row.Key, row.Count)
	}
	tbl.Print()
}

func init() {
	table.DefaultHeaderFormatter = func(format string, vals ...interface{}) string {
		return strings.ToUpper(fmt.Sprintf(format, vals...))
	}
}

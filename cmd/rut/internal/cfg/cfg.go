// Package cfg contains common configuration variables.
package cfg

import (
	"flag"
	"log/slog"
	"os"
)

var (
	TraceFile   string = os.Getenv("TRACE_FILE")
	LogFile     string = os.Getenv("LOG_FILE")
	JsonHandler bool   = os.Getenv("JSON_LOG") != ""
	Verbose     bool   = os.Getenv("DEBUG") != ""

	SomeFlag      string
	SomeOtherFlag string

	Log *slog.Logger = slog.Default()
)

type FlagMask uint16

const (
	DefaultFlags FlagMask = 0
	OmitSomeFlag FlagMask = 1 << (iota - 1)
	OmitSomeOtherFlag

	OmitAll = OmitSomeFlag | OmitSomeOtherFlag
)

// SetBaseFlags sets base flags.
func SetBaseFlags(fs *flag.FlagSet, mask FlagMask) {
	fs.StringVar(&TraceFile, "trace", TraceFile, "trace `filename`")
	fs.StringVar(&LogFile, "log", LogFile, "log `file`, if not specified, messages are printed to STDERR")
	fs.BoolVar(&JsonHandler, "log-json", JsonHandler, "log in JSON format")
	fs.BoolVar(&Verbose, "v", Verbose, "verbose messages")

	if mask&OmitSomeFlag == 0 {
		fs.StringVar(&SomeFlag, "some-flag", "", "some flag sets something")
	}
	if mask&OmitSomeOtherFlag == 0 {
		fs.StringVar(&SomeOtherFlag, "some-other-flag", "", "some other flag sets something else")
	}

	setDevFlags(fs, mask)
}

package authloginfo

import (
	"bufio"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/oschwald/geoip2-golang/v2"
)

var (
	timestampPattern      = regexp.MustCompile(`^([A-Z][a-z]{2}\s+\d{1,2}\s+\d{2}:\d{2}:\d{2})\s+`)
	invalidUserPattern    = regexp.MustCompile(`Invalid user (\S+) from (\S+) port (\d+)`)
	failedInvalidPattern  = regexp.MustCompile(`Failed password for invalid user (\S+) from (\S+) port (\d+)`)
	failedPasswordPattern = regexp.MustCompile(`Failed password for (\S+) from (\S+) port (\d+)`)
	sessionOpenedPattern  = regexp.MustCompile(`pam_unix\((sshd|sudo|su):session\): session opened for user (\S+) by`)
)

type FailureKind string

const (
	FailureInvalidUser FailureKind = "invalid_user"
	FailureBadPassword FailureKind = "bad_password"
	UnknownCountry                 = "Unknown"
)

type FailedAttempt struct {
	Timestamp    string
	Username     string
	SourceIP     string
	SourcePort   string
	IsRootTarget bool
	FailureKind  FailureKind
}

type SuccessfulSession struct {
	Timestamp   string
	Username    string
	SessionType string
}

type AnalysisResult struct {
	ProcessedLines int
	FailedAttempts []FailedAttempt
	RootFailures   []FailedAttempt
	Successful     []SuccessfulSession
	IPCounts       map[string]int
	UsernameCounts map[string]int
	CountryCounts  map[string]int
	SkippedFiles   []string
	ProcessedFiles []string
}

type Analyzer struct {
	geoDB        *geoip2.Reader
	countryCache map[string]string
}

func NewAnalyzer(geoDBPath string) (*Analyzer, error) {
	a := &Analyzer{
		countryCache: make(map[string]string),
	}
	if geoDBPath == "" {
		return a, nil
	}

	db, err := geoip2.Open(geoDBPath)
	if err != nil {
		return nil, fmt.Errorf("open GeoIP database %q: %w", geoDBPath, err)
	}
	a.geoDB = db
	return a, nil
}

func (a *Analyzer) Close() error {
	if a.geoDB == nil {
		return nil
	}
	return a.geoDB.Close()
}

func DiscoverLogFiles(primaryPath string) ([]string, error) {
	dir := filepath.Dir(primaryPath)
	base := filepath.Base(primaryPath)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read log directory %q: %w", dir, err)
	}

	type rotation struct {
		path   string
		number int
	}

	var rotations []rotation
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, base+".") {
			continue
		}

		suffix := strings.TrimPrefix(name, base+".")
		gz := false
		if strings.HasSuffix(suffix, ".gz") {
			gz = true
			suffix = strings.TrimSuffix(suffix, ".gz")
		}
		if suffix == "" {
			continue
		}
		n, err := strconv.Atoi(suffix)
		if err != nil || n <= 0 {
			continue
		}
		_ = gz
		rotations = append(rotations, rotation{
			path:   filepath.Join(dir, name),
			number: n,
		})
	}

	sort.Slice(rotations, func(i, j int) bool {
		if rotations[i].number != rotations[j].number {
			return rotations[i].number > rotations[j].number
		}
		return rotations[i].path < rotations[j].path
	})

	files := make([]string, 0, len(rotations)+1)
	for _, rotation := range rotations {
		files = append(files, rotation.path)
	}
	files = append(files, primaryPath)
	return files, nil
}

func (a *Analyzer) AnalyzeFiles(paths []string) (AnalysisResult, error) {
	result := AnalysisResult{
		IPCounts:       make(map[string]int),
		UsernameCounts: make(map[string]int),
		CountryCounts:  make(map[string]int),
	}

	var openedAny bool
	for _, path := range paths {
		lines, failures, successes, err := parseLogFile(path)
		if err != nil {
			result.SkippedFiles = append(result.SkippedFiles, fmt.Sprintf("%s: %v", path, err))
			continue
		}

		openedAny = true
		result.ProcessedFiles = append(result.ProcessedFiles, path)
		result.ProcessedLines += lines
		result.FailedAttempts = append(result.FailedAttempts, failures...)
		result.Successful = append(result.Successful, successes...)
		for _, failure := range failures {
			if failure.IsRootTarget {
				result.RootFailures = append(result.RootFailures, failure)
			}
			if failure.Username != "" {
				result.UsernameCounts[failure.Username]++
			}
			if failure.SourceIP != "" {
				result.IPCounts[failure.SourceIP]++
			}
		}
	}

	if !openedAny {
		return AnalysisResult{}, errors.New("could not read the auth log or any matching rotated logs")
	}

	a.populateCountryCounts(result.IPCounts, result.CountryCounts)
	return result, nil
}

func parseLogFile(path string) (int, []FailedAttempt, []SuccessfulSession, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, nil, nil, err
	}
	defer file.Close()

	var reader io.Reader = file
	if strings.HasSuffix(path, ".gz") {
		gzipReader, err := gzip.NewReader(file)
		if err != nil {
			return 0, nil, nil, fmt.Errorf("invalid gzip content: %w", err)
		}
		defer gzipReader.Close()
		reader = gzipReader
	}

	scanner := bufio.NewScanner(reader)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	var (
		lineCount int
		failures  []FailedAttempt
		successes []SuccessfulSession
	)
	for scanner.Scan() {
		lineCount++
		line := scanner.Text()
		if failure, ok := parseFailedAttempt(line); ok {
			failures = append(failures, failure)
		}
		if success, ok := parseSuccessfulSession(line); ok {
			successes = append(successes, success)
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, nil, nil, err
	}

	return lineCount, failures, successes, nil
}

func parseFailedAttempt(line string) (FailedAttempt, bool) {
	timestamp := extractTimestamp(line)

	if match := failedInvalidPattern.FindStringSubmatch(line); match != nil {
		ip := normalizeIP(match[2])
		if ip == "" {
			return FailedAttempt{}, false
		}
		return FailedAttempt{
			Timestamp:    timestamp,
			Username:     match[1],
			SourceIP:     ip,
			SourcePort:   match[3],
			IsRootTarget: strings.EqualFold(match[1], "root"),
			FailureKind:  FailureInvalidUser,
		}, true
	}

	if match := invalidUserPattern.FindStringSubmatch(line); match != nil {
		ip := normalizeIP(match[2])
		if ip == "" {
			return FailedAttempt{}, false
		}
		return FailedAttempt{
			Timestamp:    timestamp,
			Username:     match[1],
			SourceIP:     ip,
			SourcePort:   match[3],
			IsRootTarget: strings.EqualFold(match[1], "root"),
			FailureKind:  FailureInvalidUser,
		}, true
	}

	if match := failedPasswordPattern.FindStringSubmatch(line); match != nil {
		ip := normalizeIP(match[2])
		if ip == "" {
			return FailedAttempt{}, false
		}
		return FailedAttempt{
			Timestamp:    timestamp,
			Username:     match[1],
			SourceIP:     ip,
			SourcePort:   match[3],
			IsRootTarget: strings.EqualFold(match[1], "root"),
			FailureKind:  FailureBadPassword,
		}, true
	}

	return FailedAttempt{}, false
}

func parseSuccessfulSession(line string) (SuccessfulSession, bool) {
	match := sessionOpenedPattern.FindStringSubmatch(line)
	if match == nil {
		return SuccessfulSession{}, false
	}
	return SuccessfulSession{
		Timestamp:   extractTimestamp(line),
		SessionType: match[1],
		Username:    match[2],
	}, true
}

func extractTimestamp(line string) string {
	match := timestampPattern.FindStringSubmatch(line)
	if match == nil {
		return "N/A"
	}
	return match[1]
}

func normalizeIP(raw string) string {
	ip := net.ParseIP(raw)
	if ip == nil {
		return ""
	}
	return ip.String()
}

func (a *Analyzer) populateCountryCounts(ipCounts map[string]int, countryCounts map[string]int) {
	for ip, count := range ipCounts {
		country := a.lookupCountry(ip)
		countryCounts[country] += count
	}
}

func (a *Analyzer) lookupCountry(ip string) string {
	if cached, ok := a.countryCache[ip]; ok {
		return cached
	}

	country := UnknownCountry
	if a.geoDB != nil {
		addr, err := netip.ParseAddr(ip)
		if err == nil {
			record, err := a.geoDB.Country(addr)
			if err == nil && record.HasData() {
				switch {
				case record.Country.Names.English != "":
					country = record.Country.Names.English
				case record.Country.ISOCode != "":
					country = record.Country.ISOCode
				}
			}
		}
	}

	a.countryCache[ip] = country
	return country
}

type CountRow struct {
	Key   string
	Count int
}

func sortedCountRows(counts map[string]int) []CountRow {
	rows := make([]CountRow, 0, len(counts))
	for key, count := range counts {
		rows = append(rows, CountRow{Key: key, Count: count})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count != rows[j].Count {
			return rows[i].Count > rows[j].Count
		}
		return rows[i].Key < rows[j].Key
	})
	return rows
}

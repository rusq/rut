package toheic

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fixture struct {
	b                *batch
	out, diagnostics bytes.Buffer
	calls            []string
	pages            int
	failPage         string
	sipsFail, empty  bool
	missing          map[string]bool
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{pages: 1, missing: map[string]bool{}}
	f.b = &batch{opts: options{85, true, "auto"}, out: &f.out, errOut: &f.diagnostics, link: os.Link}
	f.b.lookup = func(name string) (string, error) {
		if f.missing[name] {
			return "", os.ErrNotExist
		}
		return name, nil
	}
	f.b.exec = func(ctx context.Context, name string, args []string, input io.Reader) ([]byte, error) {
		f.calls = append(f.calls, name+" "+strings.Join(args, " "))
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if reflect.DeepEqual(args, []string{"--formats"}) {
			return []byte("public.heic heic Writable\n"), nil
		}
		if args[0] == "identify" {
			var b strings.Builder
			for i := 0; i < f.pages; i++ {
				b.WriteString("RUTPAGE:")
				b.WriteByte(byte('0' + i))
				b.WriteByte('\n')
			}
			return []byte(b.String()), nil
		}
		dest := strings.TrimPrefix(args[len(args)-1], "heic:")
		content := []byte("encoded")
		if input != nil {
			data, err := io.ReadAll(input)
			if err != nil {
				return nil, err
			}
			content = append(content, data...)
		}
		if f.empty {
			content = nil
		}
		if err := os.WriteFile(dest, content, 0600); err != nil {
			return nil, err
		}
		if name == "sips" && f.sipsFail {
			return nil, errors.New("sips failure with partial output")
		}
		if f.failPage != "" && args[0] == f.failPage {
			return nil, errors.New("page failure with partial output")
		}
		return nil, nil
	}
	return f
}
func write(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("source bytes"), 0600); err != nil {
		t.Fatal(err)
	}
}

func writeRawWithoutDate(t *testing.T, path, format string) {
	t.Helper()
	if format == "crw" {
		data := make([]byte, 40)
		copy(data, "II\x1a\x00\x00\x00HEAPCCDR")
		binary.LittleEndian.PutUint32(data[2:], 26)
		// Root block starts at 26; its directory has zero records at offset 26.
		binary.LittleEndian.PutUint32(data[len(data)-4:], 0)
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	data := make([]byte, 14)
	copy(data, "II")
	binary.LittleEndian.PutUint16(data[2:], 42)
	binary.LittleEndian.PutUint32(data[4:], 8)
	// zero entries followed by the next-IFD offset
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}
func assertClean(t *testing.T, dir string) {
	t.Helper()
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			t.Error(err)
			return nil
		}
		if strings.HasPrefix(d.Name(), ".rut-2heic-") {
			t.Errorf("temporary directory remains: %s", path)
		}
		return nil
	})
}
func TestValidate(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name string
		o    options
		args []string
		bad  bool
	}{
		{"defaults", options{85, true, "auto"}, nil, false}, {"directory", options{0, false, "magick"}, []string{dir}, false},
		{"quality100", options{100, true, "sips"}, []string{dir}, false}, {"negative", options{-1, true, "auto"}, nil, true},
		{"tooHigh", options{101, true, "auto"}, nil, true}, {"encoder", options{85, true, "other"}, nil, true},
		{"extra", options{85, true, "auto"}, []string{dir, dir}, true}, {"missing", options{85, true, "auto"}, []string{filepath.Join(dir, "missing")}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validate(tc.o, tc.args)
			if (err != nil) != tc.bad {
				t.Fatalf("error=%v", err)
			}
		})
	}
	file := filepath.Join(dir, "file")
	write(t, file)
	if _, err := validate(options{85, true, "auto"}, []string{file}); err == nil {
		t.Fatal("accepted file as directory")
	}
}
func TestTraversalNamingAndSourcePreservation(t *testing.T) {
	for _, recursive := range []bool{false, true} {
		t.Run(map[bool]string{false: "top", true: "recursive"}[recursive], func(t *testing.T) {
			dir := t.TempDir()
			names := []string{"photo.PNG", "photo.jpg", "other.JPEG", "scan.TIF", "scan.tiff", "odd [name]\n$.png", "sub/nested.png", "ignored.gif"}
			for _, name := range names {
				write(t, filepath.Join(dir, name))
			}
			f := newFixture(t)
			f.b.opts.recursive = recursive
			if err := f.b.run(context.Background(), dir); err != nil {
				t.Fatal(err)
			}
			want := 6
			if recursive {
				want++
			}
			if f.b.converted != want {
				t.Fatalf("converted=%d want=%d", f.b.converted, want)
			}
			for _, name := range names {
				p := filepath.Join(dir, name)
				data, err := os.ReadFile(p)
				if err != nil || string(data) != "source bytes" {
					t.Fatalf("source changed %s", p)
				}
				if imageFormat(name) != "" && (recursive || !strings.Contains(name, "/")) {
					if _, err := os.Stat(p + ".heic"); err != nil {
						t.Error(err)
					}
				}
			}
			assertClean(t, dir)
		})
	}
}
func TestExistingOutputsAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	external := t.TempDir()
	write(t, filepath.Join(external, "outside.png"))
	for _, name := range []string{"existing.png", "dangling.jpg"} {
		write(t, filepath.Join(dir, name))
	}
	write(t, filepath.Join(dir, "existing.png.heic"))
	for _, pair := range [][2]string{{"dangling.jpg.heic", "missing"}, {"linked.png", filepath.Join(external, "outside.png")}, {"linked-dir", external}} {
		if err := os.Symlink(pair[1], filepath.Join(dir, pair[0])); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	f := newFixture(t)
	if err := f.b.run(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	if f.b.skipped != 2 || f.b.converted != 0 || len(f.calls) != 0 {
		t.Fatalf("unexpected processing: %+v %v", f.b, f.calls)
	}
}
func TestBackendSelectionAndFallback(t *testing.T) {
	for _, tc := range []struct {
		name, encoder, ext                   string
		sipsMissing, magickMissing, sipsFail bool
		wantBackend                          string
		bad                                  bool
	}{
		{name: "sips", encoder: "auto", ext: "png", wantBackend: "sips"},
		{name: "fallback", encoder: "auto", ext: "jpg", sipsFail: true, wantBackend: "magick"},
		{name: "magickOnly", encoder: "magick", ext: "png", wantBackend: "magick"},
		{name: "sipsUnavailable", encoder: "auto", ext: "png", sipsMissing: true, wantBackend: "magick"},
		{name: "forcedSipsFailure", encoder: "sips", ext: "png", sipsFail: true, bad: true},
		{name: "noEncoder", encoder: "auto", ext: "png", sipsMissing: true, magickMissing: true, bad: true},
		{name: "tiff", encoder: "auto", ext: "tiff", wantBackend: "magick"},
		{name: "tiffSips", encoder: "sips", ext: "tiff", bad: true},
		{name: "tiffNoMagick", encoder: "auto", ext: "tif", magickMissing: true, bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "source."+tc.ext)
			write(t, p)
			f := newFixture(t)
			f.b.opts.encoder = tc.encoder
			f.sipsFail = tc.sipsFail
			f.missing["sips"] = tc.sipsMissing
			f.missing["magick"] = tc.magickMissing
			err := f.b.run(context.Background(), dir)
			if (err != nil) != tc.bad {
				t.Fatalf("err=%v calls=%v", err, f.calls)
			}
			if tc.wantBackend != "" && !strings.HasPrefix(f.calls[len(f.calls)-1], tc.wantBackend+" ") {
				t.Fatalf("calls=%v", f.calls)
			}
			if tc.bad {
				if _, err := os.Lstat(p + ".heic"); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("failed output exists: %v", err)
				}
			}
			assertClean(t, dir)
		})
	}
}
func TestTIFFPagesFailureAndRetry(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "scan.tiff")
	write(t, p)
	f := newFixture(t)
	f.pages = 3
	f.failPage = "tiff:-[1]"
	if err := f.b.run(context.Background(), dir); err == nil {
		t.Fatal("expected failure")
	}
	if f.b.converted != 2 || f.b.failed != 1 {
		t.Fatalf("counts: %+v", f.b)
	}
	if _, err := os.Stat(p + ".page-0002.heic"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial page exists: %v", err)
	}
	retry := newFixture(t)
	retry.pages = 3
	if err := retry.b.run(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	if retry.b.converted != 1 || retry.b.skipped != 2 {
		t.Fatalf("retry counts: %+v", retry.b)
	}
	assertClean(t, dir)
}
func TestEmptyOutputAndPublishFailures(t *testing.T) {
	for _, mode := range []string{"empty", "race", "unsupported"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "one.png")
			write(t, p)
			f := newFixture(t)
			f.b.opts.encoder = "magick"
			f.empty = mode == "empty"
			if mode == "race" {
				f.b.link = func(_, dest string) error { write(t, dest); return os.ErrExist }
			}
			if mode == "unsupported" {
				f.b.link = func(_, _ string) error { return errors.New("hard links unsupported") }
			}
			err := f.b.run(context.Background(), dir)
			if (err == nil) != (mode == "race") {
				t.Fatalf("err=%v", err)
			}
			if mode == "race" {
				data, _ := os.ReadFile(p + ".heic")
				if string(data) != "source bytes" || f.b.skipped != 1 {
					t.Fatal("overwrote racing destination")
				}
			} else if _, err := os.Stat(p + ".heic"); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("published failed output")
			}
			assertClean(t, dir)
		})
	}
}
func TestEmptyDirectoryNeedsNoEncoder(t *testing.T) {
	f := newFixture(t)
	if err := f.b.run(context.Background(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 0 {
		t.Fatal(f.calls)
	}
}
func TestCancellationCleansAndStops(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.png"))
	write(t, filepath.Join(dir, "b.png"))
	f := newFixture(t)
	original := f.b.exec
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.b.exec = func(ctx context.Context, name string, args []string, in io.Reader) ([]byte, error) {
		data, err := original(ctx, name, args, in)
		if args[0] == "-s" {
			cancel()
			return nil, ctx.Err()
		}
		return data, err
	}
	if err := f.b.run(ctx, dir); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if f.b.converted != 0 || strings.Contains(strings.Join(f.calls, "\n"), "b.png") {
		t.Fatalf("continued after cancel: %v", f.calls)
	}
	assertClean(t, dir)
}
func TestExecuteHelper(t *testing.T) {
	if os.Getenv("RUT_HEIC_HELPER") != "1" {
		return
	}
	switch os.Getenv("RUT_HEIC_HELPER_MODE") {
	case "error":
		os.Stderr.WriteString("encoder diagnostic")
		os.Exit(7)
	case "wait":
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	os.Exit(0)
}
func TestExecuteDiagnosticsAndCancellation(t *testing.T) {
	t.Setenv("RUT_HEIC_HELPER", "1")
	t.Setenv("RUT_HEIC_HELPER_MODE", "error")
	_, err := execute(context.Background(), os.Args[0], []string{"-test.run=^TestExecuteHelper$"}, nil)
	if err == nil || !strings.Contains(err.Error(), "encoder diagnostic") {
		t.Fatalf("err=%v", err)
	}
	t.Setenv("RUT_HEIC_HELPER_MODE", "wait")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = execute(ctx, os.Args[0], []string{"-test.run=^TestExecuteHelper$"}, nil)
	if err == nil || ctx.Err() == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("cancellation err=%v elapsed=%v", err, time.Since(start))
	}
}

func TestCapabilityAndEnumerationFailures(t *testing.T) {
	for _, mode := range []string{"encoderError", "malformedPages", "identifyError"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			ext := "png"
			if strings.Contains(mode, "Pages") || mode == "identifyError" {
				ext = "tiff"
			}
			write(t, filepath.Join(dir, "source."+ext))
			f := newFixture(t)
			f.b.opts.encoder = "magick"
			original := f.b.exec
			f.b.exec = func(ctx context.Context, name string, args []string, in io.Reader) ([]byte, error) {
				if mode == "encoderError" {
					return nil, errors.New("HEIC encoder unavailable")
				}
				if args[0] == "identify" && mode == "malformedPages" {
					return []byte("RUTPAGE:0\nRUTPAGE:2\n"), nil
				}
				if args[0] == "identify" && mode == "identifyError" {
					return nil, errors.New("invalid TIFF")
				}
				return original(ctx, name, args, in)
			}
			if err := f.b.run(context.Background(), dir); err == nil || f.b.failed != 1 {
				t.Fatalf("err=%v failed=%d", err, f.b.failed)
			}
			assertClean(t, dir)
		})
	}
}

func TestTraversalErrorIsReported(t *testing.T) {
	f := newFixture(t)
	if err := f.b.run(context.Background(), filepath.Join(t.TempDir(), "missing")); err == nil || f.b.failed != 1 {
		t.Fatalf("err=%v failed=%d", err, f.b.failed)
	}
}

func TestRAWExtensions(t *testing.T) {
	for _, ext := range []string{"crw", "cr2", "nef", "nrw"} {
		for _, variant := range []string{ext, strings.ToUpper(ext)} {
			if got := imageFormat("photo." + variant); got != ext || !isRAW(got) {
				t.Fatalf("extension=%s format=%s", variant, got)
			}
		}
	}
}

func TestRAWConversionArgumentsAndInput(t *testing.T) {
	for _, format := range []string{"crw", "cr2", "nef", "nrw"} {
		t.Run(format, func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(dir, "photo."+strings.ToUpper(format))
			writeRawWithoutDate(t, source, format)
			f := newFixture(t)
			if err := f.b.run(context.Background(), dir); err != nil {
				t.Fatal(err)
			}
			calls := strings.Join(f.calls, "\n")
			want := "magick -define dng:use-camera-wb=true -define dng:read-thumbnails=false " + format + ":- -auto-orient -quality 85"
			if !strings.Contains(calls, want) || strings.Contains(calls, "sips -s") {
				t.Fatalf("calls=%s", calls)
			}
			data, err := os.ReadFile(source + ".heic")
			sourceData, sourceErr := os.ReadFile(source)
			if err != nil || sourceErr != nil || !bytes.Equal(data, append([]byte("encoded"), sourceData...)) {
				t.Fatalf("input consumed: %q err=%v", data, err)
			}
			assertClean(t, dir)
		})
	}
}

func TestJPEGInRAWFile(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "photo.NEF")
	data := []byte{0xff, 0xd8, 0xff, 0xe0, 0, 1, 2, 3}
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	f := newFixture(t)
	if err := f.b.run(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.diagnostics.String(), "RAW extension contains JPEG data") {
		t.Fatal(f.diagnostics.String())
	}
	calls := strings.Join(f.calls, "\n")
	if !strings.Contains(calls, "magick jpeg:- -auto-orient -quality 85") || strings.Contains(calls, "dng:") || strings.Contains(calls, "sips -s") {
		t.Fatal(calls)
	}
	got, err := os.ReadFile(source + ".heic")
	if err != nil || !bytes.Equal(got, append([]byte("encoded"), data...)) {
		t.Fatalf("input consumed: %x err=%v", got, err)
	}
}

func TestRAWFailuresContinue(t *testing.T) {
	for _, mode := range []string{"sips", "noMagick", "noDecoder", "truncated", "decodeFailure"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			raw := filepath.Join(dir, "a.NEF")
			write(t, raw)
			write(t, filepath.Join(dir, "b.png"))
			f := newFixture(t)
			switch mode {
			case "sips":
				f.b.opts.encoder = "sips"
			case "noMagick":
				f.missing["magick"] = true
			case "noDecoder":
				original := f.b.exec
				f.b.exec = func(ctx context.Context, name string, args []string, in io.Reader) ([]byte, error) {
					data, err := original(ctx, name, args, in)
					if args[0] == "-list" {
						data = bytes.ReplaceAll(data, []byte("NEF DNG r--"), []byte("NEF DNG ---"))
					}
					return data, err
				}
			case "truncated":
				if err := os.WriteFile(raw, []byte{0x49}, 0600); err != nil {
					t.Fatal(err)
				}
			case "decodeFailure":
				original := f.b.exec
				f.b.exec = func(ctx context.Context, name string, args []string, in io.Reader) ([]byte, error) {
					data, err := original(ctx, name, args, in)
					if strings.Contains(strings.Join(args, " "), "nef:-") {
						return nil, errors.New("unsupported camera")
					}
					return data, err
				}
			}
			if err := f.b.run(context.Background(), dir); err == nil || f.b.failed != 1 || f.b.converted != 1 {
				t.Fatalf("err=%v counts=%+v", err, f.b)
			}
			if _, err := os.Stat(raw + ".heic"); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed output exists: %v", err)
			}
			assertClean(t, dir)
		})
	}
}

// Run explicitly with RUT_HEIC_SAMPLE_TESTS=1 when local camera samples are available.
func TestCameraSamples(t *testing.T) {
	if os.Getenv("RUT_HEIC_SAMPLE_TESTS") != "1" {
		t.Skip("optional camera sample integration test")
	}
	sourceDir := filepath.Join("..", "..", "..", "..", "samples")
	entries, err := os.ReadDir(sourceDir)
	if os.IsNotExist(err) {
		t.Skip("camera samples unavailable")
	}
	if err != nil {
		t.Fatal(err)
	}
	var out, diagnostics bytes.Buffer
	b := batch{opts: options{85, true, "auto"}, out: &out, errOut: &diagnostics, exec: execute, lookup: exec.LookPath, link: os.Link}
	b.probe(context.Background())
	if b.magick == "" {
		t.Skip("ImageMagick HEIC encoder unavailable")
	}
	dir := t.TempDir()
	hashes := map[string][]byte{}
	for _, entry := range entries {
		if !isRAW(imageFormat(entry.Name())) {
			continue
		}
		path := filepath.Join(sourceDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		hashes[entry.Name()] = append([]byte(nil), data...)
		if err := os.WriteFile(filepath.Join(dir, entry.Name()), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if len(hashes) == 0 {
		t.Skip("no camera samples")
	}
	if err := b.run(context.Background(), dir); err != nil {
		t.Fatalf("%v\n%s", err, &diagnostics)
	}
	for name, hash := range hashes {
		source := filepath.Join(dir, name)
		format := imageFormat(name)
		decoder, err := rawDecoder(source, format)
		if err != nil {
			t.Fatal(err)
		}
		data, err := execute(context.Background(), b.magick, []string{"identify", "-ping", "-format", "%w %h %[orientation]", decoder + ":" + source}, nil)
		if err != nil {
			t.Fatal(err)
		}
		fields := strings.Fields(string(data))
		if len(fields) != 3 {
			t.Fatalf("source properties: %s", data)
		}
		w, h := fields[0], fields[1]
		switch fields[2] {
		case "LeftTop", "RightTop", "RightBottom", "LeftBottom":
			w, h = h, w
		}
		data, err = execute(context.Background(), b.magick, []string{"identify", "-format", "%m %w %h %[orientation]", source + ".heic"}, nil)
		if err != nil || strings.TrimSpace(string(data)) != "HEIC "+w+" "+h+" TopLeft" {
			t.Fatalf("%s output=%s err=%v", name, data, err)
		}
		t.Logf("%s: %s", name, data)
		for _, path := range []string{source, filepath.Join(sourceDir, name)} {
			data, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(data, hash) {
				t.Fatalf("source changed: %s", path)
			}
		}
	}
	converted := b.converted
	if err := b.run(context.Background(), dir); err != nil || b.converted != converted || b.skipped != len(hashes) {
		t.Fatalf("retry: %v counts=%+v", err, &b)
	}
	assertClean(t, dir)
}

func TestCameraSampleCaptureDates(t *testing.T) {
	if os.Getenv("RUT_HEIC_SAMPLE_TESTS") != "1" {
		t.Skip("optional camera sample metadata test")
	}
	for name, want := range map[string]captureDate{
		"IMG_20080408_133524_Canon EOS 10D.CRW": {original: "2008:04:08 13:35:24"},
		"IMG_20141231_183000_Canon EOS 5D.CR2":  {original: "2014:12:31 18:30:00"},
		"IMG_20150210_185656_NIKON D80.NEF":     {original: "2015:02:10 18:56:56", subsec: "10"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := readCaptureDate(filepath.Join("..", "..", "..", "..", "samples", name), imageFormat(name))
			if err != nil || !got.equal(want) {
				t.Fatalf("got=%+v err=%v", got, err)
			}
		})
	}
}

// A broken format listing must not prevent working encoders from converting.
func TestConversionDoesNotListImageMagickFormats(t *testing.T) {
	for _, encoder := range []string{"auto", "magick"} {
		t.Run(encoder, func(t *testing.T) {
			dir := t.TempDir()
			write(t, filepath.Join(dir, "source.jpg"))
			f := newFixture(t)
			f.b.opts.encoder = encoder
			f.sipsFail = true
			original := f.b.exec
			f.b.exec = func(ctx context.Context, name string, args []string, in io.Reader) ([]byte, error) {
				if name == "magick" && args[0] == "-list" {
					t.Fatal("format enumeration can hang while loading unrelated modules")
				}
				return original(ctx, name, args, in)
			}
			if err := f.b.run(context.Background(), dir); err != nil {
				t.Fatal(err)
			}
			if f.b.converted != 1 {
				t.Fatalf("converted=%d", f.b.converted)
			}
			assertClean(t, dir)
		})
	}
}

func TestSipsProbeTimeoutFallsBack(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "source.jpg"))
	f := newFixture(t)
	original := f.b.exec
	f.b.exec = func(ctx context.Context, name string, args []string, in io.Reader) ([]byte, error) {
		if name == "sips" && args[0] == "--formats" {
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("capability probe has no timeout")
			}
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return original(ctx, name, args, in)
	}
	if err := f.b.run(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	if f.b.converted != 1 || f.b.sips != "" {
		t.Fatalf("converted=%d sips=%q", f.b.converted, f.b.sips)
	}
}

// Package toheic converts directory images without modifying their sources.
package toheic

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rusq/rut/cmd/rut/internal/cfg"
	"github.com/rusq/rut/cmd/rut/internal/golang/base"
)

type options struct {
	quality   int
	recursive bool
	encoder   string
}

var flags = options{85, true, "auto"}
var CmdToHEIC = &base.Command{
	Run: run, UsageLine: "rut 2heic [flags] [directory]", Short: "converts images and Canon/Nikon RAW files to HEIC", FlagMask: cfg.OmitAll, PrintFlags: true,
	Long: `2heic recursively converts PNG, JPEG, TIFF, Canon CRW/CR2, and Nikon NEF/NRW
images, preserving originals.
The directory defaults to the current directory. Flags must precede it.
Outputs retain the source extension (photo.jpg.heic); existing outputs are skipped.
Multi-page TIFF produces scan.tiff.page-0001.heic, scan.tiff.page-0002.heic, etc.
TIFF and RAW always require ImageMagick. RAW uses camera white balance and
automatic orientation, decoding the full image rather than embedded thumbnails.
JPEG data in a RAW-named file is detected and converted with a warning.
For genuine RAW files, EXIF capture date, subseconds, and offset are retained
and verified before publication. A RAW file without a capture date is converted
with a warning; no date is invented. ExifTool and Perl are not required.
Auto prefers sips for PNG/JPEG and falls back
per image to ImageMagick. Encoder executables must be on PATH with HEIC support.
Symlinks are not followed. Output publication requires filesystem hard-link support.
Successful TIFF pages are retained if another page fails. Metadata preservation
and identical quality across encoders are not guaranteed.

Examples:
  rut 2heic /path/to/images
  rut 2heic -quality 90 -recursive=false .
  rut 2heic -encoder magick /path/to/images
`,
}

func init() {
	CmdToHEIC.Flag.IntVar(&flags.quality, "quality", 85, "encoder quality from 0 to 100")
	CmdToHEIC.Flag.BoolVar(&flags.recursive, "recursive", true, "include subdirectories")
	CmdToHEIC.Flag.StringVar(&flags.encoder, "encoder", "auto", "encoder: auto, sips, or magick (TIFF and RAW require magick)")
}
func validate(o options, args []string) (string, error) {
	if len(args) > 1 {
		return "", fmt.Errorf("expected at most one directory")
	}
	if o.quality < 0 || o.quality > 100 {
		return "", fmt.Errorf("quality must be from 0 to 100")
	}
	if o.encoder != "auto" && o.encoder != "sips" && o.encoder != "magick" {
		return "", fmt.Errorf("unknown encoder %q", o.encoder)
	}
	dir := "."
	if len(args) == 1 {
		dir = args[0]
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("not a directory: %s", dir)
	}
	return dir, nil
}
func run(ctx context.Context, _ *base.Command, args []string) error {
	dir, err := validate(flags, args)
	if err != nil {
		base.SetExitStatus(base.SInvalidParameters)
		return err
	}
	b := batch{opts: flags, out: os.Stdout, errOut: os.Stderr, exec: execute, lookup: exec.LookPath, link: os.Link}
	if err = b.run(ctx, dir); err != nil {
		base.SetExitStatus(base.SGenericError)
	}
	return err
}

type runner func(context.Context, string, []string, io.Reader) ([]byte, error)
type batch struct {
	opts                       options
	out, errOut                io.Writer
	exec                       runner
	lookup                     func(string) (string, error)
	link                       func(string, string) error
	sips, magick               string
	probed                     bool
	converted, skipped, failed int
}

func execute(ctx context.Context, name string, args []string, input io.Reader) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = input
	data, err := cmd.CombinedOutput()
	if err != nil {
		return data, fmt.Errorf("%s: %w: %s", filepath.Base(name), err, strings.TrimSpace(string(data)))
	}
	return data, nil
}
func imageFormat(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "png"
	case ".jpg", ".jpeg":
		return "jpeg"
	case ".tif", ".tiff":
		return "tiff"
	case ".crw", ".cr2", ".nef", ".nrw":
		return strings.ToLower(filepath.Ext(path)[1:])
	}
	return ""
}
func isRAW(format string) bool {
	return format == "crw" || format == "cr2" || format == "nef" || format == "nrw"
}

func rawDecoder(path, format string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	var header [3]byte
	_, err = io.ReadFull(f, header[:])
	if err != nil {
		return "", fmt.Errorf("read RAW signature: %w", err)
	}
	if header == [3]byte{0xff, 0xd8, 0xff} {
		return "jpeg", nil
	}
	return format, nil
}

func (b *batch) probe(ctx context.Context) {
	if b.probed {
		return
	}
	b.probed = true
	if b.opts.encoder != "magick" {
		if path, err := b.lookup("sips"); err == nil {
			probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			data, err := b.exec(probeCtx, path, []string{"--formats"}, nil)
			cancel()
			if err == nil {
				for _, line := range strings.Split(string(data), "\n") {
					f := strings.Fields(line)
					if len(f) >= 3 && f[0] == "public.heic" && f[2] == "Writable" {
						b.sips = path
					}
				}
			}
		}
	}
	if b.opts.encoder != "sips" {
		if path, err := b.lookup("magick"); err == nil {
			// Listing every format loads unrelated coder modules and can hang even
			// when HEIC conversion works. Let the conversion check codec support.
			b.magick = path
		}
	}
}
func (b *batch) failure(path string, err error) {
	b.failed++
	fmt.Fprintf(b.errOut, "Failed: %s: %v\n", path, err)
}
func (b *batch) run(ctx context.Context, dir string) error {
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if walkErr != nil {
			b.failure(path, walkErr)
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if path != dir && !b.opts.recursive {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		format := imageFormat(path)
		if format == "" {
			return nil
		}
		if format != "tiff" {
			b.convert(ctx, path, format, -1, path+".heic")
			return nil
		}
		b.probe(ctx)
		if b.magick == "" {
			b.failure(path, errors.New("TIFF requires ImageMagick with HEIC write support; use -encoder auto or magick"))
			return nil
		}
		pages, err := b.pages(ctx, path)
		if err != nil {
			b.failure(path, err)
			return nil
		}
		for page := 0; page < pages; page++ {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			output := path + ".heic"
			if pages > 1 {
				output = fmt.Sprintf("%s.page-%04d.heic", path, page+1)
			}
			b.convert(ctx, path, format, page, output)
		}
		return nil
	})
	fmt.Fprintf(b.out, "Done: %d converted, %d skipped, %d failed.\n", b.converted, b.skipped, b.failed)
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if b.failed > 0 {
		return fmt.Errorf("%d conversion or traversal failures", b.failed)
	}
	return nil
}
func (b *batch) pages(ctx context.Context, path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	// Markers distinguish TIFF page numbers from successful encoder warnings.
	data, err := b.exec(ctx, b.magick, []string{"identify", "-ping", "-format", "RUTPAGE:%p\\n", "tiff:-"}, f)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "RUTPAGE:") {
			continue
		}
		page, err := strconv.Atoi(strings.TrimPrefix(line, "RUTPAGE:"))
		if err != nil || page != count {
			return 0, errors.New("invalid TIFF page enumeration")
		}
		count++
	}
	if count == 0 {
		return 0, errors.New("no TIFF pages found")
	}
	return count, nil
}
func (b *batch) convert(ctx context.Context, source, format string, page int, output string) {
	if _, err := os.Lstat(output); err == nil {
		b.skipped++
		fmt.Fprintf(b.out, "Skipping existing: %s\n", output)
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		b.failure(output, err)
		return
	}
	rawInput := isRAW(format)
	var capture captureDate
	attachCapture := false
	b.probe(ctx)
	if ctx.Err() != nil {
		return
	}
	if rawInput {
		if b.magick == "" {
			b.failure(source, errors.New("RAW requires ImageMagick with HEIC write support; use -encoder auto or magick"))
			return
		}
		decoder, err := rawDecoder(source, format)
		if err != nil {
			b.failure(source, err)
			return
		}
		if decoder != format {
			fmt.Fprintf(b.errOut, "warning: %s: RAW extension contains JPEG data; decoding as JPEG\n", source)
		} else {
			capture, err = readCaptureDate(source, format)
			if err != nil {
				b.failure(source, fmt.Errorf("read RAW capture metadata: %w", err))
				return
			}
			if capture.empty() {
				fmt.Fprintf(b.errOut, "warning: %s: RAW source has no capture date; converting without one\n", source)
			} else {
				attachCapture = true
			}
		}
		format = decoder
	}
	if b.sips == "" && b.magick == "" {
		b.failure(source, errors.New("no HEIC encoder found: use macOS sips or install ImageMagick with HEIC support"))
		return
	}
	fmt.Fprintf(b.out, "Converting: %s -> %s\n", source, output)
	temp, err := os.MkdirTemp(filepath.Dir(output), ".rut-2heic-")
	if err != nil {
		b.failure(output, err)
		return
	}
	defer func() {
		if err := os.RemoveAll(temp); err != nil {
			b.failure(temp, fmt.Errorf("clean temporary files: %w", err))
		}
	}()
	staged := filepath.Join(temp, "output.heic")
	profile := ""
	if attachCapture {
		data, err := exifProfile(capture)
		if err != nil {
			b.failure(source, err)
			return
		}
		profile = filepath.Join(temp, "capture.exif")
		if err := os.WriteFile(profile, data, 0600); err != nil {
			b.failure(output, err)
			return
		}
	}
	var conversionErr error
	if !rawInput && format != "tiff" && b.sips != "" {
		_, conversionErr = b.exec(ctx, b.sips, []string{"-s", "format", "heic", "-s", "formatOptions", strconv.Itoa(b.opts.quality), source, "--out", staged}, nil)
		if conversionErr == nil {
			conversionErr = checkOutput(staged)
		}
	}
	if (rawInput || format == "tiff" || b.sips == "" || conversionErr != nil) && b.magick != "" && ctx.Err() == nil {
		if err := os.Remove(staged); err != nil && !errors.Is(err, os.ErrNotExist) {
			b.failure(output, err)
			return
		}
		f, err := os.Open(source)
		if err == nil {
			input := format + ":-"
			if page >= 0 {
				input += fmt.Sprintf("[%d]", page)
			}
			var args []string
			if isRAW(format) {
				args = append(args, "-define", "dng:use-camera-wb=true", "-define", "dng:read-thumbnails=false")
			}
			args = append(args, input)
			if rawInput {
				args = append(args, "-auto-orient")
			}
			if profile != "" {
				args = append(args, "-profile", "APP1:"+profile)
			}
			args = append(args, "-quality", strconv.Itoa(b.opts.quality), "heic:"+staged)
			_, err = b.exec(ctx, b.magick, args, f)
			f.Close()
			if err == nil {
				err = checkOutput(staged)
			}
		}
		if err != nil && conversionErr != nil {
			conversionErr = fmt.Errorf("sips failed: %v; ImageMagick failed: %w", conversionErr, err)
		} else {
			conversionErr = err
		}
	}
	if conversionErr != nil {
		b.failure(output, conversionErr)
		return
	}
	if attachCapture {
		data, err := b.exec(ctx, b.magick, []string{"identify", "-ping", "-format", "%[EXIF:*]", staged}, nil)
		if err != nil {
			b.failure(output, fmt.Errorf("verify capture metadata: %w", err))
			return
		}
		got, err := parseIdentifyDates(string(data))
		if err != nil {
			b.failure(output, fmt.Errorf("verify capture metadata: %w (%q)", err, string(data)))
			return
		}
		if !capture.equal(got) {
			b.failure(output, fmt.Errorf("capture metadata mismatch: source %s, output %s", capture, got))
			return
		}
	}
	if ctx.Err() != nil {
		return
	}
	if err := b.link(staged, output); err != nil {
		if errors.Is(err, os.ErrExist) {
			b.skipped++
			fmt.Fprintf(b.out, "Skipping existing: %s\n", output)
			return
		}
		b.failure(output, fmt.Errorf("publish HEIC (filesystem must support hard links): %w", err))
		return
	}
	b.converted++
}
func checkOutput(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return errors.New("encoder produced no nonempty regular HEIC output")
	}
	return nil
}

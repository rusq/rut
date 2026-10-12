package toheic

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// captureDate is deliberately text rather than time.Time: EXIF dates are local
// camera-calendar values and converting them through a host timezone corrupts them.
type captureDate struct {
	original string
	subsec   string
	offset   string
}

func (d captureDate) empty() bool { return d.original == "" }

func validDate(s string) bool {
	if len(s) != 19 {
		return false
	}
	_, err := time.Parse("2006:01:02 15:04:05", s)
	return err == nil
}
func validOffset(s string) bool {
	if len(s) != 6 || (s[0] != '+' && s[0] != '-') || s[3] != ':' {
		return false
	}
	h, e1 := strconv.Atoi(s[1:3])
	m, e2 := strconv.Atoi(s[4:6])
	return e1 == nil && e2 == nil && h <= 14 && m < 60 && !(h == 14 && m != 0)
}
func (d captureDate) valid() error {
	if !validDate(d.original) {
		return errors.New("invalid DateTimeOriginal")
	}
	if d.subsec != "" {
		for _, c := range d.subsec {
			if c < '0' || c > '9' {
				return errors.New("invalid SubSecTimeOriginal")
			}
		}
	}
	if d.offset != "" && !validOffset(d.offset) {
		return errors.New("invalid OffsetTimeOriginal")
	}
	return nil
}

const (
	tagExifIFD  = 0x8769
	tagOriginal = 0x9003
	tagSubsec   = 0x9291
	tagOffset   = 0x9011
)

func readCaptureDate(path, format string) (captureDate, error) {
	f, err := os.Open(path)
	if err != nil {
		return captureDate{}, err
	}
	defer f.Close()
	if format == "crw" {
		return readCIFFDate(f)
	}
	var h [3]byte
	if _, err := f.ReadAt(h[:], 0); err != nil {
		return captureDate{}, err
	}
	if h == [3]byte{0xff, 0xd8, 0xff} {
		return readJPEGDate(f)
	}
	return readTIFFDate(f, 0)
}

func fileSize(r io.ReaderAt) (int64, error) {
	if s, ok := r.(interface{ Stat() (os.FileInfo, error) }); ok {
		i, e := s.Stat()
		return i.Size(), e
	}
	return 0, errors.New("metadata reader has no size")
}
func readAt(r io.ReaderAt, off, n int64, size int64) ([]byte, error) {
	if off < 0 || n < 0 || n > 1024 || off > size || n > size-off {
		return nil, errors.New("invalid metadata offset or length")
	}
	b := make([]byte, n)
	_, err := r.ReadAt(b, off)
	if err != nil {
		return nil, err
	}
	return b, nil
}
func orderAt(r io.ReaderAt, base, size int64) (binary.ByteOrder, error) {
	b, err := readAt(r, base, 8, size)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(b[:2], []byte("II")) {
		return binary.LittleEndian, nil
	}
	if bytes.Equal(b[:2], []byte("MM")) {
		return binary.BigEndian, nil
	}
	return nil, errors.New("not a TIFF header")
}
func readTIFFDate(r io.ReaderAt, base int64) (captureDate, error) {
	size, err := fileSize(r)
	if err != nil {
		return captureDate{}, err
	}
	o, err := orderAt(r, base, size)
	if err != nil {
		return captureDate{}, err
	}
	h, _ := readAt(r, base, 8, size)
	if o.Uint16(h[2:4]) != 42 {
		return captureDate{}, errors.New("invalid TIFF magic")
	}
	root := int64(o.Uint32(h[4:8]))
	if root == 0 {
		return captureDate{}, errors.New("missing TIFF IFD")
	}
	var d captureDate
	seen := map[int64]bool{}
	var dirs, entries int
	var walk func(int64, int) error
	walk = func(off int64, depth int) error {
		if depth > 16 || dirs >= 64 {
			return errors.New("TIFF traversal limit")
		}
		dirs++
		if seen[off] {
			return errors.New("cyclic TIFF IFD")
		}
		seen[off] = true
		p := base + off
		b, e := readAt(r, p, 2, size)
		if e != nil {
			return e
		}
		n := int(o.Uint16(b))
		if entries+n > 4096 {
			return errors.New("TIFF entry limit")
		}
		entries += n
		// The entry limit bounds the allocation, but each read must also
		// stay within readAt's metadata read limit.
		data := make([]byte, n*12+4)
		for start := 0; start < len(data); start += 1024 {
			length := min(1024, len(data)-start)
			chunk, err := readAt(r, p+2+int64(start), int64(length), size)
			if err != nil {
				return err
			}
			copy(data[start:], chunk)
		}
		for i := 0; i < n; i++ {
			x := data[i*12 : i*12+12]
			tag, typ := o.Uint16(x), o.Uint16(x[2:])
			count := uint64(o.Uint32(x[4:]))
			width := uint64(0)
			switch typ {
			case 1, 2, 7:
				width = 1
			case 3:
				width = 2
			case 4:
				width = 4
			}
			if count == 0 || width == 0 || count > 1024/width {
				continue
			}
			total := int64(count * width)
			v := x[8:12]
			if total > 4 {
				q := int64(o.Uint32(v))
				v, e = readAt(r, base+q, total, size)
				if e != nil {
					return e
				}
			} else {
				v = v[:total]
			}
			if tag == tagExifIFD && typ == 4 && count == 1 {
				if e := walk(int64(o.Uint32(v)), depth+1); e != nil {
					return e
				}
				continue
			}
			if typ != 2 {
				continue
			}
			s := strings.TrimRight(string(v), "\x00")
			switch tag {
			case tagOriginal:
				if d.original != "" && d.original != s {
					return errors.New("conflicting DateTimeOriginal")
				}
				d.original = s
			case tagSubsec:
				if d.subsec != "" && d.subsec != s {
					return errors.New("conflicting SubSecTimeOriginal")
				}
				d.subsec = s
			case tagOffset:
				if d.offset != "" && d.offset != s {
					return errors.New("conflicting OffsetTimeOriginal")
				}
				d.offset = s
			}
		}
		return nil
	}
	if err := walk(root, 0); err != nil {
		return captureDate{}, err
	}
	if d.empty() {
		return d, nil
	}
	return d, d.valid()
}
func readJPEGDate(r io.ReaderAt) (captureDate, error) {
	size, err := fileSize(r)
	if err != nil {
		return captureDate{}, err
	}
	off := int64(2)
	for off+4 <= size {
		h, e := readAt(r, off, 4, size)
		if e != nil {
			return captureDate{}, e
		}
		if h[0] != 0xff {
			return captureDate{}, errors.New("invalid JPEG marker")
		}
		marker := h[1]
		if marker == 0xda || marker == 0xd9 {
			break
		}
		n := int64(binary.BigEndian.Uint16(h[2:]))
		if n < 2 {
			return captureDate{}, errors.New("invalid JPEG segment")
		}
		if marker == 0xe1 && n >= 8 {
			p, e := readAt(r, off+4, 6, size)
			if e != nil {
				return captureDate{}, e
			}
			if string(p) == "Exif\x00\x00" {
				return readTIFFDate(r, off+10)
			}
		}
		off += 2 + n
	}
	return captureDate{}, nil
}

// CIFF stores its directory offset in the final four bytes. Canon's timestamp
// record is a Unix timestamp; the low 0x180e tag identifies the record. Some
// cameras store it inline, others point at the four-byte value.
func readCIFFDate(r io.ReaderAt) (captureDate, error) {
	size, err := fileSize(r)
	if err != nil {
		return captureDate{}, err
	}
	if size < 30 {
		return captureDate{}, errors.New("truncated CIFF")
	}
	h, e := readAt(r, 0, 14, size)
	if e != nil {
		return captureDate{}, e
	}
	if !bytes.Equal(h[:2], []byte("II")) || !bytes.Equal(h[6:14], []byte("HEAPCCDR")) {
		return captureDate{}, errors.New("not Canon CIFF")
	}
	block := int64(binary.LittleEndian.Uint32(h[2:6]))
	if block < 0 || block >= size {
		return captureDate{}, errors.New("invalid CIFF header length")
	}
	var found captureDate
	var dirs, entries int
	var walk func(int64, int64, int) error
	walk = func(offset, length int64, depth int) error {
		if depth > 16 || dirs >= 64 {
			return errors.New("CIFF traversal limit")
		}
		dirs++
		if length < 4 {
			return errors.New("truncated CIFF directory")
		}
		tail, e := readAt(r, offset+length-4, 4, size)
		if e != nil {
			return e
		}
		dir := offset + int64(binary.LittleEndian.Uint32(tail))
		countBytes, e := readAt(r, dir, 2, size)
		if e != nil {
			return e
		}
		count := int(binary.LittleEndian.Uint16(countBytes))
		if entries+count > 4096 {
			return errors.New("CIFF entry limit")
		}
		entries += count
		items, e := readAt(r, dir+2, int64(count)*10, size)
		if e != nil {
			return e
		}
		for i := 0; i < count; i++ {
			x := items[i*10 : i*10+10]
			typ := binary.LittleEndian.Uint16(x)
			n := int64(binary.LittleEndian.Uint32(x[2:]))
			rel := int64(binary.LittleEndian.Uint32(x[6:]))
			inline := typ&0x4000 != 0
			typ &= 0x3fff
			value := offset + rel
			if inline {
				n = 8
				value = dir + 2 + int64(i)*10 + 2
			}
			if value < 0 || value > size {
				return errors.New("invalid CIFF entry")
			}
			switch typ >> 8 {
			case 0x28, 0x30:
				if n > size-value {
					return errors.New("invalid CIFF directory entry")
				}
				if err := walk(value, n, depth+1); err != nil {
					return err
				}
			case 0x18:
				if typ != 0x180e {
					continue
				}
				if n < 4 || n > 1024 || n > size-value {
					return errors.New("invalid CIFF timestamp")
				}
				v, e := readAt(r, value, 4, size)
				if e != nil {
					return e
				}
				t := time.Unix(int64(binary.LittleEndian.Uint32(v)), 0).UTC()
				d := captureDate{original: t.Format("2006:01:02 15:04:05")}
				if n >= 8 {
					z, e := readAt(r, value+4, 4, size)
					if e != nil {
						return e
					}
					sec := int64(int32(binary.LittleEndian.Uint32(z)))
					if sec != 0 && sec >= -14*3600 && sec <= 14*3600 {
						sign := "+"
						if sec < 0 {
							sign = "-"
							sec = -sec
						}
						d.offset = fmt.Sprintf("%s%02d:%02d", sign, sec/3600, (sec/60)%60)
					}
				}
				if !found.empty() && !found.equal(d) {
					return errors.New("conflicting CIFF timestamps")
				}
				found = d
			}
		}
		return nil
	}
	if err := walk(block, size-block, 0); err != nil {
		return captureDate{}, err
	}
	if found.empty() {
		return found, nil
	}
	return found, found.valid()
}

func exifProfile(d captureDate) ([]byte, error) {
	if err := d.valid(); err != nil {
		return nil, err
	}
	tags := []struct {
		tag   uint16
		value string
	}{{tagOriginal, d.original}}
	if d.subsec != "" {
		tags = append(tags, struct {
			tag   uint16
			value string
		}{tagSubsec, d.subsec})
	}
	if d.offset != "" {
		tags = append(tags, struct {
			tag   uint16
			value string
		}{tagOffset, d.offset})
	}
	const base = 8
	root := base
	ifd := root + 2 + 12 + 4
	data := ifd + 2 + len(tags)*12 + 4
	out := make([]byte, data)
	copy(out, "II")
	binary.LittleEndian.PutUint16(out[2:], 42)
	binary.LittleEndian.PutUint32(out[4:], uint32(root))
	binary.LittleEndian.PutUint16(out[root:], 1)
	binary.LittleEndian.PutUint16(out[root+2:], tagExifIFD)
	binary.LittleEndian.PutUint16(out[root+4:], 4)
	binary.LittleEndian.PutUint32(out[root+6:], 1)
	binary.LittleEndian.PutUint32(out[root+10:], uint32(ifd))
	binary.LittleEndian.PutUint16(out[ifd:], uint16(len(tags)))
	for i, t := range tags {
		p := ifd + 2 + i*12
		binary.LittleEndian.PutUint16(out[p:], t.tag)
		binary.LittleEndian.PutUint16(out[p+2:], 2)
		binary.LittleEndian.PutUint32(out[p+4:], uint32(len(t.value)+1))
		value := []byte(t.value + "\x00")
		if len(value) <= 4 {
			copy(out[p+8:p+12], value)
		} else {
			binary.LittleEndian.PutUint32(out[p+8:], uint32(data))
			out = append(out, value...)
			data += len(value)
		}
	}
	return out, nil
}
func parseIdentifyDates(s string) (captureDate, error) {
	var d captureDate
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "exif:DateTimeOriginal", "DateTimeOriginal":
			d.original = strings.TrimSpace(v)
		case "exif:SubSecTimeOriginal", "SubSecTimeOriginal":
			d.subsec = strings.TrimSpace(v)
		case "exif:OffsetTimeOriginal", "OffsetTimeOriginal":
			d.offset = strings.TrimSpace(v)
		}
	}
	if d.empty() {
		return d, errors.New("staged HEIC has no DateTimeOriginal")
	}
	return d, d.valid()
}
func (d captureDate) equal(other captureDate) bool {
	return d.original == other.original && d.subsec == other.subsec && d.offset == other.offset
}
func (d captureDate) String() string {
	return fmt.Sprintf("DateTimeOriginal=%q SubSecTimeOriginal=%q OffsetTimeOriginal=%q", d.original, d.subsec, d.offset)
}

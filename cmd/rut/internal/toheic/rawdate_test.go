package toheic

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTIFFDateLargeDirectories(t *testing.T) {
	for _, order := range []struct {
		name string
		binary.ByteOrder
	}{{"II", binary.LittleEndian}, {"MM", binary.BigEndian}} {
		for _, count := range []int{85, 86, 4096, 4097} {
			t.Run(fmt.Sprintf("%s/%d", order.name, count), func(t *testing.T) {
				const date = "2026:10:12 12:34:56"
				valueOffset := 8 + 2 + count*12 + 4
				data := make([]byte, valueOffset+len(date)+1)
				copy(data, order.name)
				order.PutUint16(data[2:], 42)
				order.PutUint32(data[4:], 8)
				order.PutUint16(data[8:], uint16(count))
				// Place the date at the end so extraction requires the full directory.
				entry := data[10+(count-1)*12:]
				order.PutUint16(entry, tagOriginal)
				order.PutUint16(entry[2:], 2)
				order.PutUint32(entry[4:], uint32(len(date)+1))
				order.PutUint32(entry[8:], uint32(valueOffset))
				copy(data[valueOffset:], date)
				path := filepath.Join(t.TempDir(), "photo.nef")
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				got, err := readCaptureDate(path, "nef")
				if count > 4096 {
					if err == nil || !strings.Contains(err.Error(), "TIFF entry limit") {
						t.Fatalf("got %+v, error %v; want entry limit", got, err)
					}
					return
				}
				if err != nil || got.original != date {
					t.Fatalf("got %+v, error %v; want %s", got, err, date)
				}
				// A missing next-IFD pointer must still reject a truncated directory.
				if err := os.WriteFile(path, data[:valueOffset-1], 0600); err != nil {
					t.Fatal(err)
				}
				if _, err := readCaptureDate(path, "nef"); err == nil {
					t.Fatal("accepted truncated directory")
				}
			})
		}
	}
}

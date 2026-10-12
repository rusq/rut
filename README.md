RUSQ Utilites
-------------

Repository that combines scattered utilities.

SD card benchmark
-----------------

Run a verified filesystem benchmark against a mounted card:

    rut sdbench /Volumes/CARD

The command writes only inside a private temporary directory, verifies
sequential and random data, measures sync and small-file operations, and
removes its temporary files when it exits. Existing files are not modified,
but the benchmark consumes free space temporarily and causes flash writes.
The default test uses up to 4 GiB or 75% of available space, whichever is
smaller. Use `-size all` to exercise 90% of available space, or `-json` for a
versioned machine-readable report.

HEIC conversion
---------------

Convert images recursively while preserving originals:

    rut 2heic /path/to/images
    rut 2heic -quality 90 -recursive=false .
    rut 2heic -encoder magick /path/to/images

The directory defaults to `.`. Supported extensions are PNG, JPG/JPEG,
TIF/TIFF, Canon CRW/CR2, and Nikon NEF/NRW, matched case-insensitively. Flags must precede the directory.
Quality defaults to 85 and accepts 0–100. Outputs are written beside sources
as `photo.jpg.heic`; existing destinations are skipped, including symlinks.
Directories and files reached through symlinks are not processed.

Auto mode prefers macOS `sips` for PNG/JPEG and falls back per image to
ImageMagick. TIFF and RAW always require `magick` with HEIC write support;
explicit `-encoder sips` reports TIFF and RAW files as failures. Encoders must be on PATH.
Single-page TIFF uses `scan.tiff.heic`; multi-page TIFF produces
`scan.tiff.page-0001.heic`, `scan.tiff.page-0002.heic`, etc. Successful pages
are retained after a page failure, and retries skip completed outputs.

Conversion uses temporary files and atomic hard-link publication, requiring
a filesystem with hard-link support. Failed conversions do not leave partial
HEIC outputs. The command continues after individual failures, prints a
converted/skipped/failed summary, and returns a nonzero status on failure.
Metadata preservation and identical quality across encoders are not guaranteed.

RAW conversion requires read support for the selected format in ImageMagick.
It develops the full image using camera white balance and decoder defaults,
then applies its orientation before HEIC encoding. Embedded thumbnails are
not used. Rendering may differ from camera JPEGs. JPEG data inside a file with
a RAW extension is decoded as JPEG with a warning; output names still retain
the original extension, for example `photo.NEF.heic`.

For genuine CRW, CR2, NEF, and NRW inputs, `2heic` preserves the EXIF capture
date (`DateTimeOriginal`) and any available subseconds or offset, and verifies
those fields before publishing the HEIC. A structurally valid RAW file with no
capture date is converted with a warning; the command never invents one. This
uses only the existing ImageMagick dependency—ExifTool and Perl are not
required.

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

The directory defaults to `.`. Supported extensions are PNG, JPG/JPEG, and
TIF/TIFF, matched case-insensitively. Flags must precede the directory.
Quality defaults to 85 and accepts 0–100. Outputs are written beside sources
as `photo.jpg.heic`; existing destinations are skipped, including symlinks.
Directories and files reached through symlinks are not processed.

Auto mode prefers macOS `sips` for PNG/JPEG and falls back per image to
ImageMagick. TIFF always requires `magick` with HEIC write support; explicit
`-encoder sips` reports TIFF files as failures. Encoders must be on PATH.
Single-page TIFF uses `scan.tiff.heic`; multi-page TIFF produces
`scan.tiff.page-0001.heic`, `scan.tiff.page-0002.heic`, etc. Successful pages
are retained after a page failure, and retries skip completed outputs.

Conversion uses temporary files and atomic hard-link publication, requiring
a filesystem with hard-link support. Failed conversions do not leave partial
HEIC outputs. The command continues after individual failures, prints a
converted/skipped/failed summary, and returns a nonzero status on failure.
Metadata preservation and identical quality across encoders are not guaranteed.

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

# Crude local patch: modernc.org/libc v1.72.3

This is the complete pinned Go module cache source, including its licenses and
all platform sources. The original module checksum is
`h1:ZnDF4tXn4NBXFutMMQC4vtbTFSXhhKzR73fv0beZEAU=`.
The baseline has 4,161 files and 86,622,673 bytes. Its SHA-256 manifest digest
is `d79a453cb6a72f40e09bf8fccd2703cf7d9a09b49864544bab056ce90e1c4d18`:
sort relative slash-separated paths in case-sensitive UTF-8 byte order and hash
each UTF-8 path, a NUL byte,
and the raw SHA-256 digest of that file.

Only `libc_windows.go` differs: eight Windows calls use `syscall.SyscallN`
with the resolved procedure address and the original arguments, and their
fixed-arity entry points carry `//go:uintptrescapes`. Original file SHA-256:
`be1a22aa491527cd1469137ffa710c9b447e086cb403db5a926034de65614cf5`.

`SyscallN` is the same standard-library entry that `windows.Proc.Call` uses.
The fixed-arity adapters retain the original argument lists, procedure lookup,
success tests and thread-local error handling, without an escaping argument slice.
The compiler directive protects direct calls with immediate pointer-to-`uintptr`
conversions. It cannot recover a pointer's lifetime after conversion or through
an indirect function pointer. Those callers must own their C/TLS/native memory
through the call. Pending overlapped buffers and structures remain caller-owned
and pinned until completion, not merely until the adapter returns.

Crude's production importers of this module are the pinned `modernc.org/sqlite`
driver and its `lib` package. In SQLite v1.50.1's Windows source, `_winRead` and
`_winWrite` retain count and `OVERLAPPED` storage in explicit `tls.Alloc(48)`
frames until their syscall loops return. The database and shared-memory open
paths use synchronous handles (`flag_overlapped` is zero). This patch does not
change those ownership or completion paths. Re-review them if the SQLite pin or
its Windows build options change.

Root `sessiondb/native_adapters_windows_test.go` checks actual bytes/counts,
Unicode paths, error codes, locks, GC pressure, explicitly completed pending I/O,
and the allocation regression. Race success alone is not a native-lifetime proof.
This is a local patch, not an upstream release. When an independently reviewed
upstream fix is available, validate its lifetime, error and performance
behavior, remove this copy and its root module replacement together.

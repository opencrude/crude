# Crude local patch: modernc.org/memory v1.11.0

This is the complete pinned Go module cache source, including licenses and
all platform sources. The original module checksum is
`h1:o4QC8aMQzmcwCK3t3Ux/ZHmwFPzE6hf2Y5LbkRs+hbI=`.
The baseline has 23 files and 57,056 bytes. Its SHA-256 manifest digest is
`d712ddb03fe17175209fb36c392aef731824e41b286d00790435471cd7ad13b1`:
sort relative slash-separated paths in case-sensitive UTF-8 byte order and hash
each UTF-8 path, a NUL byte,
and the raw SHA-256 digest of that file.

Only `mmap_windows.go` differs: VirtualAlloc and VirtualFree use
`syscall.SyscallN` with the original arguments, and the concrete errno is
compared directly. Original file SHA-256:
`4416d536852f09bc19b75103768a4c2a5b382cde752dbabd2d1c4f2c2e0cd453`.

The addresses are OS-owned mappings, not pointers into a Go stack or heap.
The procedure addresses, native arguments and return/error tests are unchanged;
the returned concrete `syscall.Errno` has the same dynamic type as the original
`windows.Errno` error value. Allocation/free ownership remains with this module's
existing allocator. The patch adds no cache, pool or alternative allocator.
This is a local patch, not an upstream release. When an independently reviewed
upstream fix is available, validate its error and performance behavior, remove
this copy and its root module replacement together.

// Package pathwalk is Crude's vendored, bounded filesystem path walker.
//
// The directory enumeration implementation is derived from Charlie Vieth's
// github.com/charlievieth/fastwalk at commit
// 1d58b22e6ed98b13d701f9a77871e5215d23cc97. It is kept in third_party so Crude
// has no module dependency on Fastwalk. Crude's completed extensions add
// context cancellation, picker-specific bounded metadata catalogs, a hard
// worker ceiling, entry and path-byte limits, hidden-directory filtering,
// directory-only scans, deterministic catalog sorting, and a workspace/path
// search adapter that never reads file contents.
//
// Upstream licensing is preserved in LICENSE and GO-LICENSE.
package pathwalk

// Package jsonrepair repairs malformed JSON strings returned by LLMs,
// JavaScript snippets, and other JSON-like sources.
//
// Public APIs return errors instead of panicking, and the package does not expose
// Must* helpers. The only panic-capable paths are package initialization of
// internal regexp.MustCompile literals, which can fail only if the package source
// contains an invalid regular expression.
package jsonrepair

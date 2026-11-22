// Package safety judges a shell command or tool operation: it reports a risk,
// a reason and the permission the operation needs. The caller decides whether
// to ask the user and runs the command itself.
package safety

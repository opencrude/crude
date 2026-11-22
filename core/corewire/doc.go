// Package corewire is the carrier boundary between a user-owned core and its
// clients: multiplexed, credit-controlled streams over QUIC or over any
// reliable byte stream (local socket, named pipe, SSH stdio, TCP). It does not
// encode application values, dispatch methods or own runtime state.
package corewire

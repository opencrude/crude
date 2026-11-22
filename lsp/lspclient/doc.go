// Package lspclient runs one language server for one project root over a
// subprocess pipe, and normalizes its diagnostics, navigation, completion,
// action and hint answers. It never edits files; reviewed edits belong to
// workspace.
package lspclient

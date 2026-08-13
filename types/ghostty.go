package types

// PinnedGhosttyVersion is the Ghostty release Crude targets for host
// terminal features and in-app PTY environment (TERM=xterm-ghostty).
// Ghostty 1.3.0 is the current stable pin (released 2026-03-09).
const PinnedGhosttyVersion = "1.3.0"

// GhosttyTermName is the terminfo name Ghostty 1.3 advertises and that
// in-app terminals set so programs inside a Crude PTY see Ghostty.
const GhosttyTermName = "xterm-ghostty"

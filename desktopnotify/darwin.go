//go:build darwin

package desktopnotify

// macOS is not implemented yet. Every Show reports ErrUnsupported, and the
// caller presents that as "not available", so nothing else in Crude needs a
// macOS branch.
//
// Plan for this file: post through UNUserNotificationCenter. That API needs
// a signed application bundle with a bundle identifier, and Crude ships a
// plain executable that runs in a terminal. So the backend must run inside a
// small signed helper bundle that Crude installs beside the executable and
// talks to, the way the Windows backend owns an AppUserModelID. The helper
// must support the same three operations as the other backends: show with
// replace by key, withdraw by key, and report a click by key. Do not use
// osascript "display notification": it shows Script Editor as the sender and
// its click opens Script Editor.
func openBackend(Identity, func(string)) (backend, error) { return nil, ErrUnsupported }

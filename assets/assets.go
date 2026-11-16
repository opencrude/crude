// Package assets holds the brand images that the program itself needs.
package assets

import _ "embed"

// IconPNG is the 512 by 512 application icon. Desktop notifications show it
// as the sender's picture.
//
//go:embed crude-icon-512.png
var IconPNG []byte

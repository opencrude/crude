package tools

// PermTagSuffix returns the visible permission suffix for a tool title line.
func PermTagSuffix(tag string) string {
	if tag == "" {
		return ""
	}
	switch tag {
	case "allowed":
		return "  ✓"
	case "always":
		return "  ✓ always"
	case "auto":
		return "  ✓ auto"
	case "denied":
		return "  ✗"
	case "reviewer-allowed":
		return "  ✓ reviewer"
	case "reviewer-denied":
		return "  ✗ reviewer"
	default:
		return "  " + tag
	}
}

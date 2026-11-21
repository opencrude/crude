package rendering

import (
	"sort"
	"strings"

	"crude/tui/bidi"
)

// strongDirection supplies the same isolate boundaries to the table's
// first-strong decision as to paragraph layout. P2 skips text inside an
// isolate; an initial code identifier cannot set its containing cell's base.
// The semantic text and its byte offsets remain unchanged.
func (t *CopyText) strongDirection(text string, start, end int) (rtl, strong bool) {
	i := sort.Search(len(t.inlineCode), func(i int) bool { return t.inlineCode[i][1] > start })
	if i == len(t.inlineCode) || t.inlineCode[i][0] >= end {
		return bidi.StrongDirection(text[start:end])
	}
	var paragraph strings.Builder
	for _, span := range t.inlineCode[i:] {
		if span[0] >= end {
			break
		}
		paragraph.WriteString(text[start:max(start, span[0])])
		paragraph.WriteString(bidi.LRI)
		stop := min(end, span[1])
		paragraph.WriteString(text[max(start, span[0]):stop])
		paragraph.WriteString(bidi.PDI)
		start = stop
	}
	paragraph.WriteString(text[start:end])
	return bidi.StrongDirection(paragraph.String())
}

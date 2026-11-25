package providersim

import "strings"

// Prefix reports exact shared rendered blocks and synthetic input token counts.
type Prefix struct {
	// PreviousIndex is the earliest best match in the journal, or zero if none.
	PreviousIndex int
	// SharedBlocks is the number of matching blocks from the start.
	SharedBlocks int
	// SharedTokens is the synthetic token count in those blocks.
	SharedTokens int
	// InputTokens is the synthetic token count in the entire effective input.
	InputTokens int
	// CachedTokens is the synthetic cache read attributed to this request.
	CachedTokens int
	// CacheWriteTokens counts newly eligible input not already read from cache.
	CacheWriteTokens int
	// FirstDifference is the JSON Pointer of the first changed or missing
	// block, or empty when there is no earlier request or all blocks match.
	FirstDifference string
}

// CacheProfile defines the synthetic implicit cache thresholds for one model.
// A positive granularity specifies the interval above Minimum. Zero disables
// synthetic cache reads; prefix and input measurement still run.
type CacheProfile struct {
	// Minimum is the minimum eligible prefix size in synthetic tokens.
	Minimum int
	// Granularity is the size of each increment above Minimum, or zero to disable.
	Granularity int
}

type block struct {
	path string
	text string
}

type prefixKey struct {
	wire  Wire
	model string
}

// Each node keeps the earliest request through this prefix and that request's
// next path. Lookup and insert require one traversal, independent of history.
type prefixNode struct {
	index    int
	nextPath string
	children map[string]*prefixNode
}

func (n *prefixNode) record(index int, blocks []block, profile CacheProfile) Prefix {
	p := Prefix{PreviousIndex: n.index}
	matched := n.index != 0
	for i, b := range blocks {
		tokens := estimateTokens(b.text)
		p.InputTokens += tokens
		if n.index == 0 {
			n.index = index
			n.nextPath = b.path
		}
		child := n.children[b.text]
		if child == nil {
			child = &prefixNode{}
			if n.children == nil {
				n.children = make(map[string]*prefixNode)
			}
			n.children[b.text] = child
		}
		if matched {
			if child.index == 0 {
				p.FirstDifference = b.path
				matched = false
			} else {
				p.PreviousIndex = child.index
				p.SharedBlocks++
				p.SharedTokens += tokens
				if i == len(blocks)-1 {
					p.FirstDifference = child.nextPath
				}
			}
		}
		n = child
	}
	if len(blocks) == 0 && matched {
		p.FirstDifference = n.nextPath
	}
	if n.index == 0 {
		n.index = index
	}
	if p.PreviousIndex != 0 {
		p.CachedTokens = profile.eligible(p.SharedTokens)
	}
	p.CacheWriteTokens = max(0, profile.eligible(p.InputTokens)-p.CachedTokens)
	return p
}

// Synthetic implicit-cache model. One eligibility rule governs both the
// readable prefix and the newly written tail, including a first submission.
func (p CacheProfile) eligible(tokens int) int {
	if p.Granularity == 0 || tokens < p.Minimum {
		return 0
	}
	return p.Minimum + (tokens-p.Minimum)/p.Granularity*p.Granularity
}

// Synthetic estimate: round UTF-8 byte length up to one token per four bytes
// per rendered block. This is not a tokenizer and is not provider billing.
func estimateTokens(text string) int {
	n := len(text) / 4
	if len(text)%4 != 0 {
		n++
	}
	return n
}

var pointerEscaper = strings.NewReplacer("~", "~0", "/", "~1")

func jsonPointer(path, segment string) string {
	return path + "/" + pointerEscaper.Replace(segment)
}

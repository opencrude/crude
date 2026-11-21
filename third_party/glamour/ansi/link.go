package ansi

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"io"
	"net/url"

	"github.com/charmbracelet/x/ansi"
)

// A LinkElement is used to render hyperlinks.
type LinkElement struct {
	BaseURL  string
	URL      string
	Children []ElementRenderer
	SkipText bool
	SkipHref bool
	// LiteralURL preserves the source spelling of an inferred file reference.
	LiteralURL bool

	hyperlink, resetHyperlink string
	validURL                  bool
}

// Render renders a LinkElement.
func (e *LinkElement) Render(w io.Writer, ctx RenderContext) error {
	// Make OSC 8 hyperlink token.
	if e.LiteralURL {
		// The AST transformer already validated the source reference. A native
		// path (notably a backslash path with a source suffix) is not a URI.
		e.hyperlink, e.resetHyperlink = hyperlinkTokens(e.URL)
		e.validURL = true
	} else {
		e.hyperlink, e.resetHyperlink, e.validURL = makeHyperlink(e.URL)
	}

	if !e.SkipText {
		if err := e.renderTextPart(w, ctx); err != nil {
			return err
		}
	}
	if !e.SkipHref {
		if err := e.renderHrefPart(w, ctx); err != nil {
			return err
		}
	}
	return nil
}

func (e *LinkElement) renderTextPart(w io.Writer, ctx RenderContext) error {
	for _, child := range e.Children {
		if r, ok := child.(StyleOverriderElementRenderer); ok { //nolint:nestif
			var b bytes.Buffer
			st := ctx.options.Styles.LinkText
			if err := r.StyleOverrideRender(&b, ctx, st); err != nil {
				return fmt.Errorf("glamour: error rendering with style: %w", err)
			}

			token := e.hyperlink + b.String() + e.resetHyperlink
			if _, err := io.WriteString(w, token); err != nil {
				return fmt.Errorf("glamour: error writing hyperlink: %w", err)
			}
		} else {
			var b bytes.Buffer
			if err := child.Render(&b, ctx); err != nil {
				return fmt.Errorf("glamour: error rendering: %w", err)
			}
			token := e.hyperlink + b.String() + e.resetHyperlink
			el := &BaseElement{
				Token:     token,
				Style:     ctx.options.Styles.LinkText,
				Presented: true,
			}
			if err := el.Render(w, ctx); err != nil {
				return fmt.Errorf("glamour: error rendering: %w", err)
			}
		}
	}
	return nil
}

func (e *LinkElement) renderHrefPart(w io.Writer, ctx RenderContext) error {
	prefix := ""
	if !e.SkipText {
		prefix = " "
	}

	if e.validURL {
		href := e.URL
		if !e.LiteralURL {
			href = resolveRelativeURL(e.BaseURL, href)
		}
		token := e.hyperlink + href + e.resetHyperlink
		el := &BaseElement{
			Token:  token,
			Prefix: prefix,
			Style:  ctx.options.Styles.Link,
		}
		if ctx.options.Source != nil {
			// OSC 8 belongs to layout, not to the literal file/URL
			// that the source map will put on the clipboard.
			el.Token = href
			var styled bytes.Buffer
			if err := el.Render(&styled, ctx); err != nil {
				return err
			}
			_, err := io.WriteString(w, e.hyperlink+styled.String()+e.resetHyperlink)
			return err
		}
		if err := el.Render(w, ctx); err != nil {
			return err
		}
	}
	return nil
}

// makeHyperlink takes a URL and returns an OSC 8 hyperlink token.
func makeHyperlink(link string) (string, string, bool) {
	// Make OSC 8 hyperlink token.
	u, err := url.Parse(link)
	validURL := err == nil && "#"+u.Fragment != link // if the URL only consists of an anchor, ignore it
	if !validURL {
		return "", "", false
	}
	hyperlink, resetHyperlink := hyperlinkTokens(link)
	return hyperlink, resetHyperlink, true
}

func hyperlinkTokens(link string) (string, string) {
	h := fnv.New32a()
	_, _ = h.Write([]byte(link)) // hash.Hash never returns a write error.
	return ansi.SetHyperlink(link, fmt.Sprintf("id=%d", h.Sum32())), ansi.ResetHyperlink()
}

package providersim

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
)

// Model describes one provider inventory entry.
type Model struct {
	// ID is the model ID, or the slug on Codex.
	ID string
	// DisplayName is the optional provider-facing display name.
	DisplayName string
	// ContextWindow is the context size on Codex or the maximum input tokens
	// on Messages and Gemini. Zero leaves the wire's default unchanged.
	ContextWindow int
}

// WithModelCatalog sets a wire's public model inventory. Only Codex, Messages
// and Gemini list display names and token limits in their pinned schemas.
func WithModelCatalog(wire Wire, models ...Model) Option {
	return configureCatalog(wire, "", false, models)
}

// WithCredentialModels sets the model inventory visible to one test credential.
// Use the token itself, without "Bearer ". Requests without a matching token
// get the wire's auth error. Do not mix public and keyed catalogs on a wire.
func WithCredentialModels(wire Wire, credential string, models ...Model) Option {
	return configureCatalog(wire, credential, true, models)
}

func configureCatalog(wire Wire, key string, keyed bool, models []Model) Option {
	rows := slices.Clone(models)
	return func(s *Server) {
		if err := s.setCatalog(wire, key, keyed, rows); err != nil {
			s.t.Fatalf("simulate %s model inventory: %v", wire, err)
		}
	}
}

func (s *Server) setCatalog(wire Wire, key string, keyed bool, rows []Model) error {
	if _, ok := wireRoutes[wire]; !ok || wire == Interactions {
		return fmt.Errorf("%s has no model inventory in its pinned wire", wire)
	}
	if keyed && strings.TrimSpace(key) == "" {
		return fmt.Errorf("use a nonempty test credential")
	}
	for _, m := range rows {
		if m.ID == "" || m.ContextWindow < 0 {
			return fmt.Errorf("supply a model ID and a non-negative context window")
		}
		if wire != Codex && wire != Messages && wire != Gemini && (m.DisplayName != "" || m.ContextWindow != 0) {
			return fmt.Errorf("%s does not list display names or context windows; supply only the ID", wire)
		}
	}
	if s.catalogs == nil {
		s.catalogs = make(map[Wire]map[string][]Model)
	}
	catalog := s.catalogs[wire]
	if catalog == nil {
		catalog = make(map[string][]Model)
		s.catalogs[wire] = catalog
	} else if _, public := catalog[""]; public == keyed {
		return fmt.Errorf("use either a public inventory or credential-specific inventories")
	}
	catalog[key] = rows
	return nil
}

// inventory returns the immutable entries for the model operation, or writes
// the same authentication response as a model request with an Auth fault.
func (s *Server) inventory(w http.ResponseWriter, r *http.Request, wire Wire, fallback string) ([]Model, bool) {
	catalog := s.catalogs[wire]
	if catalog == nil {
		return []Model{{ID: fallback}}, true
	}
	if public, ok := catalog[""]; ok {
		return public, true
	}
	if models, ok := catalog[catalogCredential(r, wire)]; ok {
		return models, true
	}
	f := fault{kind: Auth}
	var status int
	var body any
	switch wire {
	case Chat, Responses, Codex:
		status, body = chatFault(f)
	case Messages:
		status, body = messageFault(w, f)
	case Gemini:
		status, body = geminiFault(f)
	case Ollama:
		n, message := ollamaFault(f)
		status, body = n, ollamaError(message)
	}
	writeJSON(w, status, body)
	return nil, false
}

func catalogCredential(r *http.Request, wire Wire) string {
	// Do not guess between multiple credential carriers.
	var values []string
	for _, v := range r.Header.Values("Authorization") {
		scheme, token, ok := strings.Cut(v, " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") {
			return ""
		}
		values = append(values, token)
	}
	switch wire {
	case Messages:
		values = append(values, r.Header.Values("x-api-key")...)
	case Gemini:
		values = append(values, r.Header.Values("x-goog-api-key")...)
		values = append(values, r.URL.Query()["key"]...)
	}
	if len(values) != 1 {
		return ""
	}
	return values[0]
}

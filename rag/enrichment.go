package rag

import (
	"fmt"
	"strings"

	"crude/xmlcodec"
)

// Enricher iteratively enriches context by discovering related terms.
// Port of Router.EnrichContextAsync + DiscoverNewTermsAsync from C#.
type Enricher struct {
	engine  *Engine
	llmCall func(prompt string) (string, error)
}

// NewEnricher creates an Enricher. llmCall should call the LLM with no tools.
func NewEnricher(engine *Engine, llmCall func(string) (string, error)) *Enricher {
	return &Enricher{engine: engine, llmCall: llmCall}
}

// Enrich runs up to maxIter rounds of search → discover new terms → search again.
// Returns the combined context string and a list of source chunk titles.
func (e *Enricher) Enrich(query string, maxIter int) (context string, sources []string, err error) {
	knownTerms := wordsOf(query)
	currentQuery := query
	var allChunks []Chunk
	seenTitles := make(map[string]bool)

	for iter := 0; iter < maxIter; iter++ {
		results, searchErr := e.engine.Search(currentQuery, 3, 0.3)
		if searchErr != nil {
			return "", nil, fmt.Errorf("enrich: search: %w", searchErr)
		}
		if len(results) == 0 {
			break
		}

		// Filter to new chunks only.
		var newChunks []Chunk
		for _, r := range results {
			key := r.Title + "::" + r.Text
			if !seenTitles[key] {
				seenTitles[key] = true
				newChunks = append(newChunks, r)
				if !seenTitles["src:"+r.Title] {
					sources = append(sources, r.Title)
					seenTitles["src:"+r.Title] = true
				}
			}
		}
		if len(newChunks) == 0 {
			break
		}
		allChunks = append(allChunks, newChunks...)

		// Format accumulated context.
		var contextParts []string
		for _, c := range allChunks {
			contextParts = append(contextParts, "Title: "+c.Title+"\nContent: "+c.Text)
		}
		allContext := strings.Join(contextParts, "\n\n---\n\n")

		// Discover new terms via LLM.
		newTerms, discoverErr := e.discoverNewTerms(query, allContext, knownTerms)
		if discoverErr != nil || len(newTerms) == 0 {
			break
		}

		// Update known terms and set next query.
		for _, t := range newTerms {
			knownTerms[strings.ToLower(t)] = true
		}
		currentQuery = strings.Join(newTerms, " ")
	}

	// Build final context string.
	var contextParts []string
	for _, c := range allChunks {
		contextParts = append(contextParts, "Title: "+c.Title+"\nContent: "+c.Text)
	}
	context = strings.Join(contextParts, "\n\n---\n\n")
	return context, sources, nil
}

// discoverNewTerms calls the LLM to identify new technical terms from accumulated context.
func (e *Enricher) discoverNewTerms(query, context string, knownTerms map[string]bool) ([]string, error) {
	knownList := make([]string, 0, len(knownTerms))
	for t := range knownTerms {
		knownList = append(knownList, t)
	}

	prompt := fmt.Sprintf(`You are a technical term analyzer. Given the user's goal and accumulated context, identify any technical terms or system names that require further investigation.
Return XML only: <terms><term>term1</term><term>term2</term></terms>
If no new terms are needed, return: <terms/>

User goal: %s
Accumulated context: %s
Already known terms (do not repeat): %s`,
		query,
		truncateStr(context, 16000),
		strings.Join(knownList, ", "),
	)

	raw, err := e.llmCall(prompt)
	if err != nil {
		return nil, err
	}

	terms, parseErr := xmlcodec.ParseTerms(raw)
	if parseErr != nil {
		return nil, nil
	}

	// Filter out already-known terms.
	var newTerms []string
	for _, t := range terms {
		lower := strings.ToLower(strings.TrimSpace(t))
		if lower != "" && !knownTerms[lower] {
			newTerms = append(newTerms, t)
		}
	}
	return newTerms, nil
}

// wordsOf returns a set of lowercase words from a string.
func wordsOf(s string) map[string]bool {
	words := make(map[string]bool)
	for _, w := range strings.Fields(s) {
		w = strings.ToLower(strings.Trim(w, ".,;:!?\"'"))
		if len(w) > 2 {
			words[w] = true
		}
	}
	return words
}

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

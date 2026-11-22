// Package gitdiff presents many Git comparisons as one continuous, hunk-focused
// document: every changed file stacked under a sticky header, unchanged runs
// folded into expandable gaps, unified or split. It owns no repository state;
// it paints immutable prepared files and emits typed file intents.
package gitdiff

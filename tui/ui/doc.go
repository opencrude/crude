// Package ui is the Crude TUI component host and design-system foundation.
//
// Product screens compose this package directly. Current authoring and surface
// contracts live in ../../docs/guides/component-authoring.md and
// ../../docs/reference/surface-catalog.md.
//
// Invariants:
//   - Paint is side-effect free with respect to domain state.
//   - Handle does not paint.
//   - Component IDs are stable and unique among mounted components.
//   - Geometry for paint and hit-testing comes from the same layout pass.
package ui

// Package inspect supplies bounded retrieval of immutable inspection evidence.
// It owns no product state, authorization, sampling or background work. Each
// service owns its Store and must authenticate the owner before calling Read.
package inspect

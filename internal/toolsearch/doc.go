// Package toolsearch provides deterministic, in-memory search for the small
// per-client tool sets exposed by Smart mode. It deliberately has no external
// dependencies: visibility is decided before documents reach this package and
// the same query always produces the same page and ordering.
package toolsearch

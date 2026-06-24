// Copyright 2026 Query Farm LLC - https://query.farm

package odataworker

// Shared helpers for the per-object discovery/description metadata that the
// vgi-lint strict profile expects on EVERY function (and on the catalog and
// schema). Each function surfaces these in its FunctionMetadata.Tags:
//
//   - vgi.title           (VGI124) — human-friendly display name
//   - vgi.description_llm (VGI112) — concise prose aimed at LLMs
//   - vgi.description_md  (VGI113) — short Markdown description
//   - vgi.keywords        (VGI126) — comma-separated search terms/synonyms
//   - vgi.source_url      (VGI128) — link to the implementing source file
//
// sourceURL(file) builds the canonical GitHub blob URL for a source file so
// every object points at exactly where it is implemented.

// sourceBase is the GitHub blob URL prefix for source files in this repo
// (pinned to main).
const sourceBase = "https://github.com/Query-farm/vgi-odata/blob/main"

// sourceURL builds the implementation vgi.source_url for a repo-relative path,
// e.g. sourceURL("internal/odataworker/functions.go").
func sourceURL(relativePath string) string {
	return sourceBase + "/" + relativePath
}

// objectTags returns the five standard per-object discovery/description tags.
// relativePath is the implementing file relative to the repo root.
func objectTags(title, descriptionLLM, descriptionMD, keywords, relativePath string) map[string]string {
	return map[string]string{
		"vgi.title":           title,
		"vgi.description_llm": descriptionLLM,
		"vgi.description_md":  descriptionMD,
		"vgi.keywords":        keywords,
		"vgi.source_url":      sourceURL(relativePath),
	}
}

// withTags merges the standard object tags with any extra function-specific
// tags (e.g. vgi.columns_md), returning a fresh map.
func withTags(base map[string]string, extra map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

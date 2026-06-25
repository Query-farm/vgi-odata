// Copyright 2026 Query Farm LLC - https://query.farm

package odataworker

// Shared helpers for the per-object discovery/description metadata that the
// vgi-lint strict profile expects on EVERY function (and on the catalog and
// schema). Each function surfaces these in its FunctionMetadata.Tags:
//
//   - vgi.title     (VGI124) — human-friendly display name
//   - vgi.doc_llm   (VGI112) — Markdown narrative aimed at LLMs/agents
//   - vgi.doc_md    (VGI113) — Markdown narrative aimed at human docs
//   - vgi.keywords  (VGI126/VGI138) — JSON array of search terms/synonyms
//
// vgi.source_url (VGI139) is advertised ONLY on the catalog object, never
// repeated per-object, so objectTags no longer emits it.

import (
	"encoding/json"
	"strings"
)

// keywordsJSON turns a comma-separated keyword list into the JSON-array string
// vgi.keywords requires (VGI138), e.g. "a, b" -> `["a","b"]`. Whitespace is
// trimmed and empty/duplicate entries are dropped (VGI127).
func keywordsJSON(commaSeparated string) string {
	seen := make(map[string]bool)
	var kws []string
	for _, raw := range strings.Split(commaSeparated, ",") {
		kw := strings.TrimSpace(raw)
		if kw == "" || seen[kw] {
			continue
		}
		seen[kw] = true
		kws = append(kws, kw)
	}
	b, err := json.Marshal(kws)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// objectTags returns the standard per-object discovery/description tags.
// keywords is supplied as a comma-separated list and serialized as a JSON array.
func objectTags(title, docLLM, docMD, keywords string) map[string]string {
	return map[string]string{
		"vgi.title":    title,
		"vgi.doc_llm":  docLLM,
		"vgi.doc_md":   docMD,
		"vgi.keywords": keywordsJSON(keywords),
	}
}

// withTags merges the standard object tags with any extra function-specific
// tags (e.g. vgi.result_columns_md), returning a fresh map.
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

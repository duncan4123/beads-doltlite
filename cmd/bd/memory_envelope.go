package main

import "encoding/json"

// memoryEnvelopeMarker tags a stored memory value as a structured envelope.
// Its presence distinguishes a timestamped envelope from a legacy bare-string
// memory written before timestamps existed.
const memoryEnvelopeMarker = 1

// memoryEnvelope is the on-disk representation of a persistent memory value.
//
// Memories live in the shared config key/value table (kv.memory.* rows), which
// has only (key, value) columns and no timestamps. Rather than widen that
// shared table across every storage backend, each memory carries its own
// created/updated timestamps inside the value. This is backend-agnostic (it is
// just string content, identical under Dolt and DoltLite) and sidesteps the
// DoltLite CURRENT_TIMESTAMP gap entirely.
type memoryEnvelope struct {
	Marker    int    `json:"_bdmem"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// wrapMemory serializes a memory's content and timestamps into the stored
// envelope form. Timestamps are expected to be RFC3339 UTC strings. On the
// (practically impossible) marshalling failure it degrades to the bare content
// so a memory is never lost.
func wrapMemory(content, createdAt, updatedAt string) string {
	b, err := json.Marshal(memoryEnvelope{
		Marker:    memoryEnvelopeMarker,
		Content:   content,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	})
	if err != nil {
		return content
	}
	return string(b)
}

// unwrapMemory parses a stored memory value. If raw is a well-formed envelope
// (carrying the _bdmem marker) it returns the content and its timestamps with
// dated=true. Otherwise raw is a legacy bare string: it is returned verbatim as
// content with empty timestamps and dated=false. The leading-brace fast path
// avoids a JSON parse for the common case of prose memories.
func unwrapMemory(raw string) (content, createdAt, updatedAt string, dated bool) {
	if len(raw) == 0 || raw[0] != '{' {
		return raw, "", "", false
	}
	var env memoryEnvelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil || env.Marker != memoryEnvelopeMarker {
		return raw, "", "", false
	}
	return env.Content, env.CreatedAt, env.UpdatedAt, true
}

// memoryMetaSuffix renders a compact provenance suffix for the memories
// listing so a reader can tell a fresh memory from a stale one at a glance.
// Legacy memories with no stored timestamp render as "undated".
func memoryMetaSuffix(updatedAt string, dated bool) string {
	if !dated || updatedAt == "" {
		return "(undated)"
	}
	// updatedAt is RFC3339 (e.g. 2026-07-04T19:00:00Z); the date alone is
	// enough to distinguish current from stale in a compact list.
	d := updatedAt
	if len(d) >= 10 {
		d = d[:10]
	}
	return "(updated " + d + ")"
}

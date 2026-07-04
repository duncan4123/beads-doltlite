package main

import (
	"strings"
	"testing"
)

func TestMemoryEnvelopeRoundTrip(t *testing.T) {
	raw := wrapMemory("always use -race", "2026-07-04T10:00:00Z", "2026-07-04T19:00:00Z")
	content, created, updated, dated := unwrapMemory(raw)
	if !dated {
		t.Fatalf("expected a dated envelope, got legacy for %q", raw)
	}
	if content != "always use -race" {
		t.Errorf("content = %q, want %q", content, "always use -race")
	}
	if created != "2026-07-04T10:00:00Z" {
		t.Errorf("created_at = %q, want %q", created, "2026-07-04T10:00:00Z")
	}
	if updated != "2026-07-04T19:00:00Z" {
		t.Errorf("updated_at = %q, want %q", updated, "2026-07-04T19:00:00Z")
	}
}

func TestMemoryEnvelopeLegacyBareString(t *testing.T) {
	// A memory stored before timestamps existed is a plain string; it must be
	// returned verbatim as content, with no timestamps and dated=false.
	content, created, updated, dated := unwrapMemory("plain old memory, no envelope")
	if dated {
		t.Fatal("legacy bare string must be reported undated")
	}
	if content != "plain old memory, no envelope" {
		t.Errorf("content = %q, want the raw string", content)
	}
	if created != "" || updated != "" {
		t.Errorf("legacy timestamps must be empty, got %q / %q", created, updated)
	}
}

func TestMemoryEnvelopeLegacyJSONWithoutMarker(t *testing.T) {
	// A legacy memory whose content merely happens to be JSON but lacks our
	// _bdmem sentinel must be treated as legacy content, not misparsed.
	raw := `{"content":"trap","created_at":"nope"}`
	content, _, _, dated := unwrapMemory(raw)
	if dated {
		t.Fatal("JSON without the _bdmem marker must be treated as legacy")
	}
	if content != raw {
		t.Errorf("content = %q, want the raw string %q", content, raw)
	}
}

func TestMemoryEnvelopeEmpty(t *testing.T) {
	content, _, _, dated := unwrapMemory("")
	if dated {
		t.Fatal("empty value must be reported undated")
	}
	if content != "" {
		t.Errorf("content = %q, want empty", content)
	}
}

func TestWrapMemoryProducesParseableEnvelope(t *testing.T) {
	raw := wrapMemory("hi", "2026-01-01T00:00:00Z", "2026-01-02T00:00:00Z")
	if !strings.Contains(raw, "_bdmem") {
		t.Errorf("envelope is missing its marker: %q", raw)
	}
	if raw == "hi" {
		t.Error("wrapMemory must not return the bare content")
	}
}

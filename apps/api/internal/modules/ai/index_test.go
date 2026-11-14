package ai

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Port of cnote-ai chunks_overlap_and_cover.
func TestChunkText(t *testing.T) {
	chunks := ChunkText(strings.Repeat("alpha beta gamma delta ", 200), 800, 100)
	if len(chunks) < 5 {
		t.Fatal(len(chunks))
	}
	for _, c := range chunks {
		if utf8.RuneCountInString(c) > 800 {
			t.Fatal("chunk too long")
		}
	}
	f := strings.Fields(chunks[0])
	if !strings.Contains(chunks[1], f[len(f)-1]) {
		t.Fatal("no overlap")
	}
	if len(ChunkText("   \n ", 800, 100)) != 0 {
		t.Fatal("blank")
	}
	if got := ChunkText("short", 800, 100); len(got) != 1 || got[0] != "short" {
		t.Fatal(got)
	}
	if len(ChunkText(strings.Repeat("नमस्ते ", 300), 800, 100)) == 0 {
		t.Fatal("multibyte")
	}
}

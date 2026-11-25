package notes

import (
	"os"
	"regexp"
	"strconv"
	"testing"
)

func TestDocSchemaMatchesCollab(t *testing.T) {
	b, err := os.ReadFile("../../../../collab/src/doc-schema.ts")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`DOC_SCHEMA = (\d+)`).FindSubmatch(b)
	if m == nil {
		t.Fatal("DOC_SCHEMA not found")
	}
	if n, _ := strconv.Atoi(string(m[1])); n != DocSchema {
		t.Fatalf("collab DOC_SCHEMA %d != Go DocSchema %d", n, DocSchema)
	}
}

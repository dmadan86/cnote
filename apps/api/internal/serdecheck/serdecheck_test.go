package serdecheck

import (
	"encoding/json"
	"testing"
)

const schema = `{"$defs":{"Source":{"enum":["jira","csv"],"type":"string"}},"properties":{"batch":{"format":"uint32","type":["integer","null"]},
"day":{"format":"date","type":["string","null"]},"id":{"format":"uuid","type":"string"},"source":{"$ref":"#/$defs/Source"},"tags":{"items":{"type":"string"},"type":"array"}},
"required":["id","source"],"type":"object"}`

// The expected texts are what the Rust server answers for the same inputs (TestSerdeInputErrorsMatchRust).
func TestDescribe(t *testing.T) {
	id := `"00000000-0000-0000-0000-000000000000"`
	for in, want := range map[string]string{
		`{}`:                                "missing field `id`",
		`{"id":` + id + `}`:                 "missing field `source`",
		`{"id":5,"source":"x"}`:             "invalid type: integer `5`, expected a formatted UUID string",
		`{"id":"zz","source":"jira"}`:       "UUID parsing failed: invalid character: found `z` at 0",
		`{"id":` + id + `,"source":"nope"}`: "unknown variant `nope`, expected `jira` or `csv`",
		`{"id":` + id + `,"source":"csv","batch":-1}`:         "invalid value: integer `-1`, expected u32",
		`{"id":` + id + `,"source":"csv","batch":1.5}`:        "invalid type: floating point `1.5`, expected u32",
		`{"id":` + id + `,"source":"csv","batch":1e20}`:       "invalid type: floating point `1e+20`, expected u32",
		`{"id":` + id + `,"source":"csv","tags":["a",1]}`:     "invalid type: integer `1`, expected a string",
		`{"id":` + id + `,"source":"csv","day":"2026-02-30"}`: "no possible date and time matching input",
		`{"id":` + id + `,"source":"csv","day":"x"}`:          "input contains invalid characters",
		`{"id":` + id + `,"source":"csv","day":"2026-01"}`:    "premature end of input",
		`{"id":` + id + `,"source":"csv","day":7}`:            "invalid type: integer `7`, expected a formatted date string",
		`{"id":` + id + `,"source":"csv","tags":"x"}`:         `invalid type: string "x", expected a sequence`,
	} {
		if got := Describe("zzz", json.RawMessage(schema), json.RawMessage(in)); got != want {
			t.Errorf("%s\n got  %q\n want %q", in, got, want)
		}
	}
}

func TestNormalizeUUIDs(t *testing.T) {
	in := json.RawMessage(`{"id":"00000000000000000000000000ABCDEF","source":"csv","tags":["x"]}`)
	got := NormalizeUUIDs(json.RawMessage(schema), in)
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil || m["id"] != "00000000-0000-0000-0000-000000abcdef" {
		t.Fatalf("%s %v", got, err)
	}
	same := json.RawMessage(`{"id":"00000000-0000-0000-0000-000000000000"}`)
	if string(NormalizeUUIDs(json.RawMessage(schema), same)) != string(same) {
		t.Fatal("canonical input must come back untouched")
	}
}

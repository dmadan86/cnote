package serdejson

import "testing"

// Texts read from the Rust server; TestSerdeJSONErrorsMatchRust compares 5000 mutated documents live.
func TestCheck(t *testing.T) {
	for in, want := range map[string]string{
		`{bad`: "key must be a string at line 1 column 2", ``: "EOF while parsing a value at line 1 column 0", `[1,]`: "trailing comma at line 1 column 4",
		`"\ud800"`: "unexpected end of hex escape at line 1 column 8", `nul`: "EOF while parsing a value at line 1 column 3", `nope`: "expected ident at line 1 column 2",
		`{"a": `: "a: EOF while parsing a value at line 1 column 6", `{"a":[1,x]}`: "a[1]: expected value at line 1 column 9", `1 2`: "trailing characters at line 1 column 3",
		`1e999`: "number out of range at line 1 column 5", `01`: "invalid number at line 1 column 2",
	} {
		if e := Check([]byte(in), true); e == nil || e.Error() != want {
			t.Errorf("%q: %v want %q", in, e, want)
		}
	}
	if Check([]byte(` {"a":[1,2.5e3,"é",null,{}]} `), true) != nil {
		t.Error("valid document refused")
	}
}

func TestStructError(t *testing.T) {
	spec := Spec{Name: "Login", Fields: []Field{{Name: "email"}, {Name: "password"}}}
	for in, want := range map[string]string{
		`{}`: "missing field `email` at line 1 column 2", `{"email":"a"}`: "missing field `password` at line 1 column 13",
		`{"email":5,"password":"x"}`:               "email: invalid type: integer `5`, expected a string at line 1 column 10",
		`{"email":"a","password":"x","email":"b"}`: "duplicate field `email` at line 1 column 35",
		`[]`: "invalid length 0, expected struct Login with 2 elements at line 1 column 2", `null`: "invalid type: null, expected struct Login at line 1 column 4",
	} {
		if status, got := StructError([]byte(in), spec); status != 422 || got != "Failed to deserialize the JSON body into the target type: "+want {
			t.Errorf("%s: %d %q", in, status, got)
		}
	}
	if status, _ := StructError([]byte(`{"email":"a","password":"b"}`), spec); status != 0 {
		t.Error("valid body refused")
	}
	if got := RustFloat(1e20); got != "1e+20" {
		t.Error(got)
	}
	if got := RustFloat(1000); got != "1000.0" {
		t.Error(got)
	}
}

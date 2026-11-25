package tables

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Template file shape (loader format, see the report): record keys are field names, select values are option ids,
// currency is integer paise, link values are lists of the target table's primary-field values, and a link field's
// config names its target table ("table") instead of an id.
type tplField struct {
	Name   string          `json:"name"`
	Type   FieldType       `json:"type"`
	Config json.RawMessage `json:"config"`
}
type tplView struct {
	Name   string          `json:"name"`
	Type   string          `json:"type"`
	Config json.RawMessage `json:"config"`
}
type tplTable struct {
	Name    string           `json:"name"`
	Fields  []tplField       `json:"fields"`
	Views   []tplView        `json:"views"`
	Records []map[string]any `json:"records"`
}
type tplFile struct {
	Key, Name, Description string
	Tables                 []tplTable
}

func TestTemplatesData(t *testing.T) {
	files, _ := filepath.Glob("templates/*.json")
	if len(files) != 10 {
		t.Fatalf("want 10 templates, got %d", len(files))
	}
	valid := map[FieldType]bool{}
	for _, ft := range AllFieldTypes {
		valid[ft] = true
	}
	for _, path := range files {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var tp tplFile
		if err := json.Unmarshal(b, &tp); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if tp.Key+".json" != filepath.Base(path) || tp.Name == "" || tp.Description == "" || len(tp.Tables) < 1 || len(tp.Tables) > 3 {
			t.Errorf("%s: bad key, name, description or table count", path)
		}
		byName := map[string]*tplTable{}
		for i := range tp.Tables {
			byName[tp.Tables[i].Name] = &tp.Tables[i]
		}
		for _, tb := range tp.Tables {
			where := tp.Key + "/" + tb.Name
			fields := map[string]Field{}
			if len(tb.Fields) == 0 {
				t.Fatalf("%s: no fields", where)
			}
			switch tb.Fields[0].Type {
			case Text, Number, Phone, Email, Autonumber:
			default:
				t.Errorf("%s: primary field type %s not allowed", where, tb.Fields[0].Type)
			}
			if len(tb.Records) < 3 || len(tb.Records) > 15 {
				t.Errorf("%s: %d records, want 3-15", where, len(tb.Records))
			}
			for _, f := range tb.Fields {
				if !valid[f.Type] || f.Type == Attachment {
					t.Errorf("%s.%s: type %q not allowed in 1a", where, f.Name, f.Type)
				}
				if _, dup := fields[f.Name]; dup {
					t.Errorf("%s: duplicate field %s", where, f.Name)
				}
				fields[f.Name] = Field{ID: f.Name, Name: f.Name, Type: f.Type, Config: f.Config}
				if c := ConfigFor(f.Type); c != nil {
					if f.Type == Link {
						var lc struct {
							Table string `json:"table"`
						}
						if json.Unmarshal(f.Config, &lc) != nil || byName[lc.Table] == nil || lc.Table == tb.Name {
							t.Errorf("%s.%s: bad link target %q", where, f.Name, lc.Table)
						}
					} else if err := json.Unmarshal(f.Config, c); err != nil {
						t.Errorf("%s.%s: config: %v", where, f.Name, err)
					}
				}
			}
			kinds := map[string]int{}
			for _, v := range tb.Views {
				kinds[v.Type]++
				var vc struct {
					Kanban *struct {
						Stack string `json:"stack"`
					} `json:"kanban"`
				}
				_ = json.Unmarshal(v.Config, &vc)
				if v.Type == "kanban" && (vc.Kanban == nil || fields[vc.Kanban.Stack].Type != SingleSelect) {
					t.Errorf("%s: kanban stack must be a single_select field", where)
				}
				if v.Type != "grid" && v.Type != "kanban" {
					t.Errorf("%s: view type %s", where, v.Type)
				}
			}
			if kinds["grid"] < 1 {
				t.Errorf("%s: needs a grid view", where)
			}
			for i, rec := range tb.Records {
				for k, v := range rec {
					f, ok := fields[k]
					if !ok {
						t.Errorf("%s #%d: unknown field %s", where, i+1, k)
						continue
					}
					if f.Type == Link {
						var lc struct{ Table string }
						_ = json.Unmarshal(f.Config, &lc)
						target := byName[lc.Table]
						list, _ := v.([]any)
						if len(list) == 0 {
							t.Errorf("%s #%d.%s: link needs a list", where, i+1, k)
						}
						for _, x := range list {
							found := false
							for _, tr := range target.Records {
								if tr[target.Fields[0].Name] == x {
									found = true
								}
							}
							if !found {
								t.Errorf("%s #%d.%s: no %q in %s", where, i+1, k, x, lc.Table)
							}
						}
						continue
					}
					if f.Type.Computed() {
						t.Errorf("%s #%d.%s: computed field has a value", where, i+1, k)
						continue
					}
					if _, err := Validate(f, v); err != nil && err != errNotImplemented {
						t.Errorf("%s #%d.%s = %v: %v", where, i+1, k, v, err)
					}
				}
			}
		}
	}
}

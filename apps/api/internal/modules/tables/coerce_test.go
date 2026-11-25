package tables

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestCoerceAndValidate(t *testing.T) {
	sel := Field{Name: "S", Type: SingleSelect, Config: json.RawMessage(`{"options":[{"id":"o1","name":"New"},{"id":"o2","name":"Paid"}]}`)}
	multi := sel
	multi.Type = MultiSelect
	user := Field{Name: "U", Type: User, Config: json.RawMessage(`{"multiple":false}`)}
	uid := "11111111-2222-4333-8444-555555555555"
	cases := []struct {
		f    Field
		raw  string
		want any
		fail bool
	}{
		{Field{Type: Text}, "  ", nil, false},
		{Field{Type: Text}, " hi ", " hi ", false},
		{Field{Type: Number}, "1,18,000.50", 118000.5, false},
		{Field{Type: Number}, "₹ 500", 500.0, false},
		{Field{Type: Percent}, "12%", 12.0, false},
		{Field{Type: Number}, "abc", nil, true},
		{Field{Type: Number}, "0x10", nil, true},
		{Field{Type: Currency}, "₹ 1,180.50", int64(118050), false},
		{Field{Type: Date}, "2026-11-25", "2026-11-25", false},
		{Field{Type: Date}, "25-11-2026", "2026-11-25", false},
		{Field{Type: Date}, "2026-02-30", nil, true},
		{Field{Type: Checkbox}, "Yes", true, false},
		{Field{Type: Checkbox}, "no", nil, false},
		{Field{Type: Checkbox}, "maybe", nil, true},
		{sel, "paid", "o2", false},
		{sel, "o1", "o1", false},
		{sel, "Other", nil, true},
		{multi, "new, PAID,new", []string{"o1", "o2"}, false},
		{Field{Type: Phone}, "+91 98123-00041", "+919812300041", false},
		{Field{Type: Phone}, "12", nil, true},
		{Field{Type: Email}, "a@b.in", "a@b.in", false},
		{Field{Type: Email}, "a@b", nil, true},
		{Field{Type: URL}, "example.com/x", "https://example.com/x", false},
		{Field{Type: URL}, "ftp://example.com", nil, true},
		{user, uid, []string{uid}, false},
		{user, uid + "," + uid, []string{uid}, false},
		{Field{Type: Autonumber}, "5", nil, true},
		{Field{Type: Link}, "x", nil, true},
	}
	for _, tc := range cases {
		got, err := Coerce(tc.f, tc.raw)
		if (err != nil) != tc.fail || (!tc.fail && !reflect.DeepEqual(got, tc.want)) {
			t.Errorf("Coerce(%s, %q) = %v, %v; want %v fail=%v", tc.f.Type, tc.raw, got, err, tc.want, tc.fail)
		}
	}
	v := []struct {
		f    Field
		in   any
		want any
		fail bool
	}{
		{Field{Type: Currency}, 118050.0, int64(118050), false},
		{Field{Type: Currency}, 1.5, nil, true},
		{Field{Type: Number}, "12", nil, true},
		{Field{Type: Checkbox}, false, nil, false},
		{Field{Type: Date}, "25-11-2026", nil, true},
		{multi, []any{"o1", "o1"}, []string{"o1"}, false},
		{user, []any{uid, uid}, []string{uid}, false},
		{Field{Type: CreatedTime}, "x", nil, true},
		{Field{Type: Text}, nil, nil, false},
	}
	for _, tc := range v {
		got, err := Validate(tc.f, tc.in)
		if (err != nil) != tc.fail || (!tc.fail && !reflect.DeepEqual(got, tc.want)) {
			t.Errorf("Validate(%s, %v) = %v, %v; want %v fail=%v", tc.f.Type, tc.in, got, err, tc.want, tc.fail)
		}
	}
}

func TestRupees(t *testing.T) {
	for in, want := range map[int64]string{11800050: "₹1,18,000.50", 50000: "₹500.00", 123456789: "₹12,34,567.89", -250: "-₹2.50"} {
		if got := rupees(in, 2); got != want {
			t.Errorf("rupees(%d) = %s, want %s", in, got, want)
		}
	}
}

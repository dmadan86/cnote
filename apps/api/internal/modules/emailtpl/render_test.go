package emailtpl

import (
	"strings"
	"testing"
)

func inv(t *testing.T) EventInfo {
	ev, ok := eventOf("invoice.sent")
	if !ok {
		t.Fatal("no invoice.sent")
	}
	return ev
}

func testBrand() brand {
	return brand{FromName: "Acme", Accent: defaultAccent, Common: map[string]string{"company.legal_name": "Acme Works Pvt Ltd", "company.gstin": "29ABCDE1234F1Z5", "_address": "12 MG Road\nBengaluru"}}
}

func TestValuesAreEscapedAndNeverRescanned(t *testing.T) {
	r := renderWith(inv(t), "Hi {{contact.name}}", "Dear {{contact.name}}, **{{invoice.number}}** {{invoice.total}}",
		map[string]string{"contact.name": `<script>alert(1)</script> {{invoice.total}} **x**`, "invoice.number": "A&B", "invoice.total": "₹1"}, testBrand())
	if strings.Contains(r.HTML, "<script>") || !strings.Contains(r.HTML, "&lt;script&gt;") {
		t.Fatalf("not escaped: %s", r.HTML)
	}
	// the value's own "{{invoice.total}}" and "**x**" stay literal
	if !strings.Contains(r.HTML, "{{invoice.total}} **x**") || !strings.Contains(r.HTML, "<strong>A&amp;B</strong>") {
		t.Fatalf("rescanned: %s", r.HTML)
	}
	if !strings.Contains(r.Text, "{{invoice.total}} **x**") || strings.Contains(r.Text, "&amp;") {
		t.Fatalf("text %q", r.Text)
	}
}

func TestAdminTypedURLIsNeverAnAnchor(t *testing.T) {
	ev := inv(t)
	r := renderWith(ev, "s", "See https://evil.example/x and <a href=\"https://evil.example\">x</a>\n\n{{invoice.link}}",
		map[string]string{"invoice.link": "https://app.example.com/p/tok"}, testBrand())
	if strings.Contains(r.HTML, `href="https://evil`) || strings.Count(r.HTML, "<a ") != 2 { // link anchor + button only
		t.Fatalf("anchors: %s", r.HTML)
	}
	if !strings.Contains(r.HTML, `href="https://app.example.com/p/tok"`) || !strings.Contains(r.HTML, "View and pay") {
		t.Fatalf("link: %s", r.HTML)
	}
	// a non-https or hostile "link" is text only, no button
	for _, bad := range []string{"javascript:alert(1)", "http://app.example.com/p/x", `https://a.test/" onclick="x`} {
		r = renderWith(ev, "s", "{{invoice.link}}", map[string]string{"invoice.link": bad}, testBrand())
		if strings.Contains(r.HTML, "<a ") || strings.Contains(r.HTML, `"x"`) {
			t.Fatalf("%q became an anchor: %s", bad, r.HTML)
		}
	}
}

func TestLinkAppendedWhenMissing(t *testing.T) {
	r := renderWith(inv(t), "s", "Hello", map[string]string{"invoice.link": "https://app.example.com/p/t"}, testBrand())
	if !strings.Contains(r.Text, "Hello\n\nhttps://app.example.com/p/t") {
		t.Fatalf("%q", r.Text)
	}
}

func TestRupeeHindiAndSubjectCRLF(t *testing.T) {
	r := renderWith(inv(t), "बिल {{invoice.total}}\r\nBcc: x@y.test", "नमस्ते {{contact.name}}",
		map[string]string{"invoice.total": "₹1,18,000.00", "contact.name": "मीरा"}, testBrand())
	if r.Subject != "बिल ₹1,18,000.00 Bcc: x@y.test" || strings.ContainsAny(r.Subject, "\r\n") {
		t.Fatalf("subject %q", r.Subject)
	}
	if !strings.Contains(r.HTML, "नमस्ते मीरा") || !strings.Contains(r.Text, "नमस्ते मीरा") {
		t.Fatal("hindi lost")
	}
}

func TestMissingValuesAndFooter(t *testing.T) {
	r := renderWith(inv(t), "s", "Hi {{contact.name}} due {{invoice.due_date}}.", map[string]string{}, testBrand())
	if !strings.Contains(r.Text, "Hi there due .") || !strings.Contains(r.Text, "Acme Works Pvt Ltd · GSTIN 29ABCDE1234F1Z5 · 12 MG Road, Bengaluru") || !strings.Contains(r.HTML, "Sent via C Note") {
		t.Fatalf("%q", r.Text)
	}
	if !strings.Contains(r.HTML, "Content-Security-Policy") || strings.Contains(r.HTML, "src=\"http") {
		t.Fatal("csp/external")
	}
}

func TestAccentIsValidated(t *testing.T) {
	for _, a := range []string{"#12ab34", "#ABCDEF"} {
		if !accentRe.MatchString(a) {
			t.Fatal(a)
		}
	}
	for _, a := range []string{"red", "#12ab3", "#12ab34;background:url(x)", "#12ab34\"", "javascript:x"} {
		if accentRe.MatchString(a) {
			t.Fatal(a)
		}
	}
}

func TestCheckTemplateRefusesUnknownAndMalformed(t *testing.T) {
	ev := inv(t)
	for name, c := range map[string][2]string{
		"unknown in body":    {"ok", "Hi {{customer.secret}}"},
		"unknown in subject": {"{{quote.number}}", "ok"},
		"half typed":         {"ok", "Hi {{ Customer name }}"},
		"unclosed":           {"ok", "Hi {{invoice.number"},
		"empty subject":      {"", "ok"},
		"long subject":       {strings.Repeat("a", 201), "ok"},
		"empty body":         {"ok", ""},
		"long body":          {"ok", strings.Repeat("a", 10001)},
	} {
		if checkTemplate(ev, c[0], c[1]) == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if err := checkTemplate(ev, "Invoice {{ invoice.number }}", "Hi {{contact.name}}"); err != nil {
		t.Fatal(err)
	}
}

func TestEveryDefaultPassesItsOwnCheck(t *testing.T) {
	if len(Events()) != 7 {
		t.Fatal("seven events")
	}
	for _, ev := range Events() {
		if err := checkTemplate(ev, ev.DefaultSubject, ev.DefaultBody); err != nil {
			t.Errorf("%s: %v", ev.Event, err)
		}
		if ev.LinkKey != "" && !ev.has(ev.LinkKey) {
			t.Errorf("%s link key", ev.Event)
		}
	}
}

package docs

import "testing"

func TestCommandNames(t *testing.T) {
	want := "doc.verify doc.set_owner doc.flags doc.proposals doc.proposal_decide doc.archived doc.restore doc.freshness_score doc.ask doc.capture capture.list capture.answer template.save template.list template.delete template.fields template.render template.refresh"
	got := ""
	for i, c := range Commands() {
		if i > 0 {
			got += " "
		}
		got += c.Name
	}
	if got != want {
		t.Fatal(got)
	}
}

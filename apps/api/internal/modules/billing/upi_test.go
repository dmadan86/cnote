package billing

import "testing"

func TestUPIURI(t *testing.T) {
	got, err := UPIURI("acme.shop@okbank", "Acme & Sons", 123_405, "INV/2026-27/0001")
	want := "upi://pay?pa=acme.shop%40okbank&pn=Acme%20%26%20Sons&am=1234.05&cu=INR&tn=INV%2F2026-27%2F0001"
	if err != nil || got != want {
		t.Fatalf("got %q %v\nwant %q", got, err, want)
	}
	if g, _ := UPIURI("a@b", "x", 5, "n"); g != "" {
		t.Fatalf("one-letter vpa accepted: %s", g)
	}
	for _, c := range []struct {
		vpa, payee string
		amt        int64
	}{{"bad vpa@x", "P", 100}, {"ok@bank", "", 100}, {"ok@bank", "P", 0}, {"ok@bank", "P", -5}} {
		if g, err := UPIURI(c.vpa, c.payee, c.amt, "n"); err == nil {
			t.Fatalf("%v accepted: %s", c, g)
		}
	}
	if g, _ := UPIURI("ok@bank", "P", 5, "n"); g != "upi://pay?pa=ok%40bank&pn=P&am=0.05&cu=INR&tn=n" {
		t.Fatal(g)
	}
}

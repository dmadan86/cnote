package sites

import "testing"

func TestShownPriceMatchesBillingTaxTable(t *testing.T) {
	// billing.taxPaise: half-up to the paisa; the incl price is the price plus that tax.
	for _, c := range []struct {
		price, rate, want int64
	}{{10_000, 1800, 11_800}, {50, 500, 53}, {49, 500, 51}, {1, 1800, 1}, {3, 1800, 4}} {
		got, ok := shownPrice(priceIncl, c.price, c.rate)
		if !ok || got != c.want {
			t.Errorf("incl %d@%d = %d %v, want %d", c.price, c.rate, got, ok, c.want)
		}
	}
	if got, ok := shownPrice(pricePlus, 10_000, 1800); !ok || got != 10_000 {
		t.Errorf("plus: %d", got)
	}
	for _, m := range []string{priceHide, priceIncl, pricePlus} {
		if _, ok := shownPrice(m, 0, 1800); ok {
			t.Errorf("%s: price 0 must ask for price", m)
		}
	}
	if _, ok := shownPrice(priceHide, 10_000, 1800); ok {
		t.Error("hide shows a price")
	}
}

func TestFormatINRAndDiff(t *testing.T) {
	for in, want := range map[int64]string{100000: "₹1,000", 12345600: "₹1,23,456", 50: "₹0.50", 99900: "₹999", 1234550: "₹12,345.50"} {
		if got := FormatINR(in); got != want {
			t.Errorf("%d: %s want %s", in, got, want)
		}
	}
	p := func(n int64) *int64 { return &n }
	old := []snapItem{{ItemID: "a", Name: "A", PriceMinor: p(100), Mode: priceIncl}, {ItemID: "b", Name: "B", Mode: priceHide}, {ItemID: "c", Name: "C", Mode: priceHide}}
	next := []snapItem{{ItemID: "a", Name: "A", PriceMinor: p(200), Mode: priceIncl}, {ItemID: "b", Name: "B2", Mode: priceHide}, {ItemID: "d", Name: "D", Mode: priceHide}}
	var kinds string
	for _, c := range diffSnapshots(old, next) {
		kinds += c.Kind + ","
	}
	if kinds != "price,renamed,added,removed," {
		t.Errorf("diff: %s", kinds)
	}
	if got := aboutLine(mustJSON(old), "A"); got != "About: A (₹1)" {
		t.Errorf("about: %q", got)
	}
	if aboutLine(mustJSON(old), "zzz") != "" || aboutLine(mustJSON(old), "c") != "About: C" {
		t.Error("about unknown or hidden")
	}
}

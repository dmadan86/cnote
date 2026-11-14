package bank

import "testing"

func TestParseMinor(t *testing.T) {
	for in, want := range map[string]int64{"1,23,456.78": 12345678, "Rs. 500": 50000, "₹1,00,000.5": 10000050, "(250.00)": -25000, "-90": -9000,
		"1,000.00 Dr": -100000, "1,000.00 Cr": 100000, "": 0, "0.00": 0, "12.500": 1250, "+7": 700, ".5": 50} {
		got, _, err := parseMinor(in)
		if err != nil || got != want {
			t.Errorf("%q: got %d err %v, want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"12.345", "abc", "1.2.3", "99999999999999999"} {
		if _, _, err := parseMinor(bad); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
}

func TestParseDateIsDayFirst(t *testing.T) {
	for in, want := range map[string]string{"03/04/2024": "2024-04-03", "3/4/24": "2024-04-03", "03-04-2024": "2024-04-03", "03 Apr 2024": "2024-04-03",
		"03-Apr-24": "2024-04-03", "2024-04-03": "2024-04-03", "03/04/2024 10:15:09": "2024-04-03", "31/12/2023": "2023-12-31"} {
		d, ok := parseDate(in)
		if !ok || d.Format("2006-01-02") != want {
			t.Errorf("%q: got %v %v, want %s", in, d, ok, want)
		}
	}
	if _, ok := parseDate("31/13/2024"); ok {
		t.Error("month 13 must not parse")
	}
}

func TestHDFCStyleWithPreambleDebitCreditColumns(t *testing.T) {
	csv := bom + "Account Statement\nCustomer Name: ACME\n\nDate,Narration,Chq./Ref.No.,Value Dt,Withdrawal Amt.,Deposit Amt.,Closing Balance\n" +
		"********,********,********,********,********,********,********\n" +
		"01/04/24,UPI-ACME CUSTOMER-9876@okhdfc-HDFC0001-440123456789-PAYMENT,0000440123456789,01/04/24,,\"1,18,000.00\",\"5,18,000.00\"\n" +
		"02/04/24,NEFT DR-OFFICE RENT,N123,02/04/24,\"25,000.00\",,\"4,93,000.00\"\n" +
		"02/04/24,NEFT DR-OFFICE RENT,N123,02/04/24,\"25,000.00\",,\"4,68,000.00\"\n" +
		"STATEMENT SUMMARY,,,,,,\n"
	r, err := parseStatement(csv, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Lines) != 3 || r.Lines[0].Amount != 11800000 || r.Lines[1].Amount != -2500000 {
		t.Fatalf("%+v", r.Lines)
	}
	if r.Lines[0].Date.Format("2006-01-02") != "2024-04-01" || *r.Lines[0].Balance != 51800000 {
		t.Fatalf("%+v", r.Lines[0])
	}
	if r.Lines[1].Hash == r.Lines[2].Hash { // different balances
		t.Fatal("distinct lines share a hash")
	}
	if r.Used.Debit != "Withdrawal Amt." || r.Used.Credit != "Deposit Amt." || r.Used.Date != "Date" || r.Used.Reference != "Chq./Ref.No." {
		t.Fatalf("%+v", r.Used)
	}
	if len(r.Errors) != 0 {
		t.Fatal(r.Errors)
	}
}

func TestSignedAmountAndDrCrColumn(t *testing.T) {
	signed := "Txn Date;Description;Amount;Balance\n05-Jun-2024;Salary credit;\"1,50,000.00\";\"1,60,000.00\"\n06-Jun-2024;ATM;-2000.00;158000.00\n"
	r, err := parseStatement(signed, nil) // semicolon delimited
	if err != nil || len(r.Lines) != 2 || r.Lines[0].Amount != 15000000 || r.Lines[1].Amount != -200000 {
		t.Fatalf("%v %+v", err, r.Lines)
	}
	drcr := "Date,Particulars,Dr/Cr,Amount\n07/06/2024,Rent,DR,\"12,000.00\"\n08/06/2024,Customer,CR,500.00\n"
	r, err = parseStatement(drcr, nil)
	if err != nil || len(r.Lines) != 2 || r.Lines[0].Amount != -1200000 || r.Lines[1].Amount != 50000 {
		t.Fatalf("%v %+v", err, r.Lines)
	}
	suffix := "Date,Narration,Amount\n09/06/2024,Fee,10.00 Dr\n"
	if r, err = parseStatement(suffix, nil); err != nil || r.Lines[0].Amount != -1000 {
		t.Fatalf("%v %+v", err, r.Lines)
	}
}

func TestNewestFirstIsStoredChronologicallyAndOccurrencesDedupe(t *testing.T) {
	csv := "Date,Narration,Debit,Credit\n10/06/2024,B,100.00,\n09/06/2024,A,100.00,\n09/06/2024,A,100.00,\n"
	r, err := parseStatement(csv, nil)
	if err != nil || len(r.Lines) != 3 {
		t.Fatal(err, r.Lines)
	}
	if r.Lines[0].Date.Day() != 9 || r.Lines[2].Date.Day() != 10 {
		t.Fatalf("not chronological: %+v", r.Lines)
	}
	if r.Lines[0].Hash == r.Lines[1].Hash { // two identical genuine lines stay two lines
		t.Fatal("identical same-day lines collapsed")
	}
	again, _ := parseStatement(csv, nil)
	for i := range r.Lines {
		if r.Lines[i].Hash != again.Lines[i].Hash {
			t.Fatal("hash is not stable across imports")
		}
	}
}

func TestBadRowsAndHeaders(t *testing.T) {
	r, err := parseStatement("Date,Narration,Debit,Credit\n31/13/2024,Bad date,5.00,\n01/01/2024,Bad amount,abc,\n01/01/2024,Both,1.00,2.00\n01/01/2024,Zero,0.00,\n02/01/2024,Ok,,3.00\n", nil)
	if err != nil || len(r.Lines) != 1 || len(r.Errors) != 3 || r.Skipped != 1 {
		t.Fatalf("%v %+v", err, r)
	}
	if _, err := parseStatement("foo,bar\n1,2\n", nil); err == nil {
		t.Error("no usable header must fail")
	}
	// a saved mapping that no longer matches the file falls back to detection
	r, err = parseStatement("Date,Narration,Debit,Credit\n02/01/2024,Ok,,3.00\n", &mapping{Date: "Posted", Debit: "Out", Credit: "In"})
	if err != nil || len(r.Lines) != 1 {
		t.Fatal(err)
	}
	// an explicit mapping wins over detection
	r, err = parseStatement("When,Memo,Out,In\n02/01/2024,Ok,,3.00\n", &mapping{Date: "When", Narration: "Memo", Debit: "Out", Credit: "In"})
	if err != nil || len(r.Lines) != 1 || r.Lines[0].Narration != "Ok" {
		t.Fatalf("%v %+v", err, r)
	}
}

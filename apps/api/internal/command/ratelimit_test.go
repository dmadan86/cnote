package command

import "testing"

func TestCheckRateNIsAllOrNothing(t *testing.T) {
	const key = "test:ratelimit-n"
	if err := CheckRateN(key, 5, 0, 3); err != nil {
		t.Fatal(err)
	}
	if err := CheckRateN(key, 5, 0, 3); err == nil {
		t.Fatal("3 of 2 remaining tokens must be refused")
	}
	if err := CheckRateN(key, 5, 0, 2); err != nil { // the refusal took nothing
		t.Fatalf("2 tokens were left: %v", err)
	}
	if err := CheckRateWith(key, 5, 0); err == nil {
		t.Fatal("bucket should be empty")
	}
}

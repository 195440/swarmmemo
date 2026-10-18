package useragent

import "testing"

func TestFor(t *testing.T) {
	if got := For("cards"); got != "SwarmMemo/cards (+https://swarmmemo.com)" {
		t.Fatalf("For: %q", got)
	}
}

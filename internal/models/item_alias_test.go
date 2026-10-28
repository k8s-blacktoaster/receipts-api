package models

import "testing"

// ItemAliasCases is shared with the SQL parity test.
var ItemAliasCases = map[string]string{
	"MILK 1/2 GAL": "milk 1/2 gal", "  Soda\t 1.5L  ": "soda 1.5l", "SODA 1,5L": "soda 1,5l",
	"COKE-2L": "coke-2l", "a\u00a0\u00a0b": "a b", "X__Y": "x__y", "Ben & Jerry's": "ben & jerry's",
	"ÉCLAIR 2CT": "éclair 2ct", "": "", "   ": "", "!!": "!!",
}

func TestItemAliasKey(t *testing.T) {
	for in, want := range ItemAliasCases {
		if got := ItemAliasKey(in); got != want {
			t.Errorf("%q: %q want %q", in, got, want)
		}
	}
	for _, pair := range [][2]string{{"MILK 12 GAL", "MILK 1/2 GAL"}, {"SODA 15L", "SODA 1.5L"}, {"SODA 1,5L", "SODA 1.5L"}, {"COKE-2L", "COKE 2L"}} {
		if ItemAliasKey(pair[0]) == ItemAliasKey(pair[1]) {
			t.Errorf("%q and %q must differ", pair[0], pair[1])
		}
	}
}

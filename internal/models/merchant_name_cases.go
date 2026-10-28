package models

// MerchantNameCases are the documented normalization examples; shared by the Go unit test and
// the SQL/Go parity integration test.
var MerchantNameCases = map[string]string{
	"Trader Joe's":          "trader_joes",
	"trader  joes":          "trader_joes",
	"TRADER   JOE'S":        "trader_joes",
	"Trader Joe’s":          "trader_joes",
	"Home-Depot ":           "home_depot",
	"The Home Depot":        "the_home_depot",
	"COSTCO":                "costco",
	"Costco Wholesale":      "costco_wholesale",
	"7-11":                  "7_11",
	"7-Eleven":              "7_eleven",
	"Barnes & Noble":        "barnes_and_noble",
	"H&M":                   "h_and_m",
	"AT&T":                  "at_and_t",
	"Bed Bath & Beyond":     "bed_bath_and_beyond",
	"  __sprouts--market_ ": "sprouts_market",
	"Macy's, Inc.":          "macys_inc",
	"Toys\"R\"Us":           "toysrus",
	"Ace\tHardware\n":       "ace_hardware",
	"Café Rio":              "café_rio",
	"nbsp\u00a0store":       "nbsp_store",
	"Lowe’s – Irvine":       "lowes_irvine",
	"!!!":                   "",
	"":                      "",
}

package models

import "strings"

// Merchant name normalization: the ONE stored merchant name (match key and display).
// Must stay byte-for-byte identical to SQL merchant_name_key() in migration 006
// (enforced by TestMerchantNameSQLGoParity).
//
//  1. lowercase (strings.ToLower; SQL lower())
//  2. '&' -> ' and ' (padded, so "H&M" -> "h_and_m" and "Barnes & Noble" -> "barnes_and_noble")
//  3. drop MerchantNamePunct (ASCII punctuation except '-' and '_', plus common typographic marks)
//  4. each run of MerchantNameSeparators ('-', '_', ASCII whitespace, NBSP) -> one '_'
//  5. strip leading/trailing '_'
//
// The character sets are explicit lists (not Unicode/locale classes) so Go and Postgres agree
// regardless of DB locale. Other characters (letters/digits of any script) are kept.
const (
	MerchantNamePunct      = "!\"#$%'()*+,./:;<=>?@[\\]^`{|}~‘’“”–—…·•®™©"
	MerchantNameSeparators = " \t\n\v\f\r\u00a0-_"
)

func NormalizeMerchantName(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "&", " and ")
	var b strings.Builder
	sep := false
	for _, r := range s {
		switch {
		case strings.ContainsRune(MerchantNamePunct, r):
			continue
		case strings.ContainsRune(MerchantNameSeparators, r):
			sep = true
		default:
			if sep && b.Len() > 0 {
				b.WriteByte('_')
			}
			sep = false
			b.WriteRune(r)
		}
	}
	return b.String()
}

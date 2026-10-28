package models

import "strings"

// ItemAliasSpace is the whitespace set collapsed by ItemAliasKey (ASCII whitespace + NBSP).
const ItemAliasSpace = " \t\n\v\f\r\u00a0"

// ItemAliasKey is the conservative key for item-alias text (exact alias lookup, learning and
// applying corrections): lowercase, trim, collapse whitespace runs to one space. ALL
// punctuation is kept ("1/2" != "12", "1.5L" != "1,5L" != "15L", "COKE-2L" != "COKE 2L").
// Must stay identical to SQL item_alias_key() in migration 012 (TestItemAliasKeySQLGoParity).
// Merchant names use NormalizeMerchantName instead.
func ItemAliasKey(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	space := false
	for _, r := range s {
		if strings.ContainsRune(ItemAliasSpace, r) {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}

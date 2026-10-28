package extraction

import "sort"

// SystemPrompt instructs the model; it is merchant-agnostic on purpose.
const SystemPrompt = `You extract data from photos of retail receipts from any merchant.
Return only JSON matching the schema. Rules:
- All money is integer cents (e.g. $12.34 -> 1234). Never use decimals for money.
- Use null for anything not printed or unreadable; do not guess.
- merchant_name is the chain/brand as printed (e.g. "Trader Joe's"), not the legal entity.
- Store location, each copied as printed and null if absent: store_number (store/location id,
  e.g. "#0412" -> "0412"), store_address (street line only), store_city, store_state (2-letter
  US state or region code), store_zip, store_phone.
- purchase_date is YYYY-MM-DD.
- One items[] entry per printed line in order; quantity may be fractional for weighed goods.
- discount_cents on an item is a POSITIVE amount taken off that line.
- savings_cents is the receipt-level "you saved" total if printed, as a POSITIVE amount.
- adjustments[]: receipt-level coupons/instant savings/discounts as NEGATIVE amount_cents;
  deposits/fees/tips as POSITIVE amount_cents.
- sku / item_number / upc: copy any printed identifiers exactly; null if absent.
- confidence: your overall confidence 0..1 that totals and items are read correctly.`

func nullable(t string) map[string]any { return map[string]any{"type": []string{t, "null"}} }

func obj(props map[string]any) map[string]any {
	req := make([]string, 0, len(props))
	for k := range props {
		req = append(req, k)
	}
	sort.Strings(req)
	return map[string]any{"type": "object", "properties": props, "required": req, "additionalProperties": false}
}

// ReceiptJSONSchema is the strict JSON schema sent as response_format.
func ReceiptJSONSchema() map[string]any {
	item := obj(map[string]any{
		"line_no": nullable("integer"), "raw_text": nullable("string"), "name": map[string]any{"type": "string"},
		"sku": nullable("string"), "item_number": nullable("string"), "upc": nullable("string"),
		"quantity": nullable("number"), "unit": nullable("string"),
		"unit_price_cents": nullable("integer"), "line_total_cents": nullable("integer"),
		"discount_cents": nullable("integer"), "tax_flag": nullable("string"), "is_return": nullable("boolean"),
	})
	adj := obj(map[string]any{
		"kind":         map[string]any{"type": "string", "enum": []string{"instant_savings", "coupon", "discount", "deposit", "fee", "tip", "other"}},
		"raw_text":     nullable("string"),
		"amount_cents": map[string]any{"type": "integer"},
	})
	return obj(map[string]any{
		"merchant_name": nullable("string"), "store_number": nullable("string"), "store_address": nullable("string"),
		"store_phone": nullable("string"), "store_city": nullable("string"), "store_state": nullable("string"),
		"store_zip": nullable("string"), "purchase_date": nullable("string"), "purchase_time": nullable("string"),
		"currency":       nullable("string"),
		"subtotal_cents": nullable("integer"), "tax_cents": nullable("integer"), "total_cents": nullable("integer"),
		"savings_cents": nullable("integer"), "payment_method": nullable("string"), "payment_last4": nullable("string"),
		"confidence":  nullable("number"),
		"items":       map[string]any{"type": "array", "items": item},
		"adjustments": map[string]any{"type": "array", "items": adj},
	})
}

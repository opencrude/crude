package provider

import "testing"

// TestLookupPriceFreeSuffixBeatsFuzzyMatch pins the resolution order in
// LookupPrice: a free variant like "meituan/LongCat-2.0:free" must resolve to
// zero pricing even though the fuzzy-substring fallback would otherwise match
// the priced "longcat-2.0" entry.
func TestLookupPriceFreeSuffixBeatsFuzzyMatch(t *testing.T) {
	SetModelPrice("longcat-2.0", ModelPrice{
		InputPricePerToken:  0.0000003,
		OutputPricePerToken: 0.000001,
	})
	defer func() {
		dynamicPricesMu.Lock()
		delete(dynamicPrices, "longcat-2.0")
		dynamicPricesMu.Unlock()
	}()

	price := LookupPrice("meituan/LongCat-2.0:free")
	if price.InputPricePerToken != 0 || price.OutputPricePerToken != 0 || price.InputCachedPricePerToken != 0 {
		t.Fatalf("free-suffix model resolved to paid pricing: %+v", price)
	}
}

// TestLookupPriceFuzzyFallbackStillResolves keeps the pre-existing fuzzy
// contract: a model absent from the exact/stripped keys can still match a
// dynamicPrices key by substring, as long as no free suffix is involved.
func TestLookupPriceFuzzyFallbackStillResolves(t *testing.T) {
	SetModelPrice("tencent/hy3", ModelPrice{
		InputPricePerToken:  0.000066,
		OutputPricePerToken: 0.00058,
	})
	defer func() {
		dynamicPricesMu.Lock()
		delete(dynamicPrices, "tencent/hy3")
		dynamicPricesMu.Unlock()
	}()

	price := LookupPrice("tencent/hy3-paid")
	if price.InputPricePerToken != 0.000066 {
		t.Fatalf("fuzzy fallback input price = %v, want 0.000066", price.InputPricePerToken)
	}
}

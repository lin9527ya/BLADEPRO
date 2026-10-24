package db

import "testing"

// TestProfileTierAndPricesRoundTrip pins the storage contract for the cost-tier
// fields: unknown tiers collapse to standard, negative prices are clamped to 0,
// and everything survives a save/load round trip.
func TestProfileTierAndPricesRoundTrip(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()

	id, err := d.SaveProfile(&LLMProfile{
		Name: "t-tier-cost", Format: "openai", Model: "m", APIKey: "sk-tier-test",
		Tier: "STRONG", InputPricePerMTok: 3, OutputPricePerMTok: -1,
		CacheReadPricePerMTok: 0.3, CacheWritePricePerMTok: -0.5,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Exec(`DELETE FROM llm_profiles WHERE id=$1`, id)

	got, err := d.ProfileByID(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Tier != ProfileTierStrong {
		t.Fatalf("tier=%q want strong", got.Tier)
	}
	if got.InputPricePerMTok != 3 || got.OutputPricePerMTok != 0 ||
		got.CacheReadPricePerMTok != 0.3 || got.CacheWritePricePerMTok != 0 {
		t.Fatalf("prices not clamped/round-tripped: %+v", got)
	}

	// 拼错/空值回落 standard，不能让配置存不进去。
	id2, err := d.SaveProfile(&LLMProfile{Name: "t-tier-unknown", Format: "openai", Model: "m", APIKey: "k", Tier: "turbo"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Exec(`DELETE FROM llm_profiles WHERE id=$1`, id2)
	got2, err := d.ProfileByID(id2)
	if err != nil {
		t.Fatal(err)
	}
	if got2.Tier != ProfileTierStandard {
		t.Fatalf("unknown tier=%q want standard", got2.Tier)
	}
}

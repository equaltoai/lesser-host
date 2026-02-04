package trust

func pricedCredits(base int64, multiplierBps int64) int64 {
	if base <= 0 {
		return 0
	}
	if multiplierBps <= 0 {
		return base
	}
	if multiplierBps >= 10000 {
		return base
	}
	// Ceil(base * multiplierBps / 10000) to avoid systematic undercharging.
	return (base*multiplierBps + 9999) / 10000
}

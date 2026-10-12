package cost

import (
	"fmt"
	"math"
)

// PercentageText preserves positive measurements below display precision.
func PercentageText(percent float64) string {
	if math.IsNaN(percent) || math.IsInf(percent, 0) || percent < 0 {
		return "미확인"
	}
	if percent == 0 {
		return "0%"
	}
	if percent < 0.1 {
		return "<0.1%"
	}
	if percent == math.Trunc(percent) {
		return fmt.Sprintf("%.0f%%", percent)
	}
	return fmt.Sprintf("%.1f%%", percent)
}

func RatioText(numerator, denominator int64) string {
	if denominator <= 0 || numerator < 0 {
		return "미확인"
	}
	return PercentageText(100 * float64(numerator) / float64(denominator))
}

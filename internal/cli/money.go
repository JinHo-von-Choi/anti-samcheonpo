package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lang"
)

var wonAmountRe = regexp.MustCompile(`(\d[\d,]*) KRW`)

// dollars rewrites won amounts in English output. A line stating the
// exchange rate keeps its won figure.
func dollars(text string, rate float64) string {
	lines := strings.SplitAfter(text, "\n")
	for i, line := range lines {
		if strings.Contains(line, "1 USD") {
			continue
		}
		lines[i] = wonAmountRe.ReplaceAllStringFunc(line, func(m string) string {
			n, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSuffix(m, " KRW"), ",", ""), 64)
			if err != nil {
				return m
			}
			return fmt.Sprintf("$%.2f", n/rate)
		})
	}
	return strings.Join(lines, "")
}

type moneyWriter struct {
	w    io.Writer
	rate float64
}

func (m *moneyWriter) Write(p []byte) (int, error) {
	if _, err := io.WriteString(m.w, dollars(string(p), m.rate)); err != nil {
		return 0, err
	}
	return len(p), nil
}

// usdRate returns the rate when output should be in dollars, or 0.
func usdRate() float64 {
	cfg := config.Default()
	if loaded, _, err := config.Load(""); err == nil {
		cfg = loaded
	}
	currency := strings.ToUpper(cfg.Currency)
	if currency == "KRW" || (currency != "USD" && lang.Current() != lang.English) {
		return 0
	}
	prices, err := cost.Load(filepath.Join(config.Home(), "prices.yml"))
	if err != nil {
		return 0
	}
	return prices.Rate(time.Now())
}

// moneyOutput is the command output writer that shows dollars, or nil.
func moneyOutput() io.Writer {
	if rate := usdRate(); rate > 0 {
		return &moneyWriter{w: os.Stdout, rate: rate}
	}
	return nil
}

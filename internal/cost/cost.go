// Package cost converts token usage to micro-won using a versioned price table.
package cost

import (
	_ "embed"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
)

//go:embed prices.yml
var builtin []byte

// Price is one price row in USD per 1M tokens.
type Price struct {
	Model      string  `yaml:"model" json:"model"`
	Input      float64 `yaml:"input" json:"input"`
	Output     float64 `yaml:"output" json:"output"`
	CacheWrite float64 `yaml:"cache_write" json:"cache_write"`
	CacheRead  float64 `yaml:"cache_read" json:"cache_read"`
	// CacheWrite1h is the 1-hour TTL write price; 0 means 2x input.
	CacheWrite1h float64 `yaml:"cache_write_1h" json:"cache_write_1h"`
	ValidFrom    Date    `yaml:"valid_from" json:"valid_from"`
	Source       string  `yaml:"source" json:"source"`
}

// FX is a USD/KRW rate valid from a date.
type FX struct {
	USDKRW    float64 `yaml:"usd_krw" json:"usd_krw"`
	ValidFrom Date    `yaml:"valid_from" json:"valid_from"`
}

// Date is a YAML date.
type Date struct{ time.Time }

// UnmarshalYAML parses YYYY-MM-DD.
func (d *Date) UnmarshalYAML(n *yaml.Node) error {
	t, err := time.Parse("2006-01-02", n.Value)
	if err != nil {
		return fmt.Errorf("invalid date %q: %w", n.Value, err)
	}
	d.Time = t
	return nil
}

// MarshalYAML writes YYYY-MM-DD.
func (d Date) MarshalYAML() (any, error) { return d.Format("2006-01-02"), nil }

// Table is the full price table.
type Table struct {
	FX     []FX    `yaml:"fx"`
	Models []Price `yaml:"models"`
	// Version is a hash of the table content, recorded in receipt seals.
	Version string `yaml:"-"`
}

// Load returns the built-in table merged with the user's override file
// (~/.samcheonpo/prices.yml) when present.
func Load(userFile string) (*Table, error) {
	var t Table
	if err := yaml.Unmarshal(builtin, &t); err != nil {
		return nil, fmt.Errorf("built-in prices: %w", err)
	}
	raw := string(builtin)
	if userFile != "" {
		if b, err := os.ReadFile(userFile); err == nil {
			var u Table
			if err := yaml.Unmarshal(b, &u); err != nil {
				return nil, fmt.Errorf("%s: %w", userFile, err)
			}
			t.FX = append(t.FX, u.FX...)
			t.Models = append(t.Models, u.Models...)
			raw += "\n" + string(b)
		}
	}
	if err := t.validate(); err != nil {
		return nil, err
	}
	sort.SliceStable(t.FX, func(i, j int) bool { return t.FX[i].ValidFrom.Before(t.FX[j].ValidFrom.Time) })
	sort.SliceStable(t.Models, func(i, j int) bool { return t.Models[i].ValidFrom.Before(t.Models[j].ValidFrom.Time) })
	t.Version = fp.Hash("prices", raw)
	return &t, nil
}

func (t *Table) validate() error {
	if len(t.FX) == 0 {
		return fmt.Errorf("price table has no fx rate")
	}
	for _, m := range t.Models {
		if m.Source == "" {
			return fmt.Errorf("price row %s has no source url", m.Model)
		}
	}
	return nil
}

// Lookup returns the price row for a model at time ts.
func (t *Table) Lookup(model string, ts time.Time) (Price, bool) {
	if model == "" || model == "<synthetic>" {
		return Price{}, false
	}
	var best Price
	bestLen := -1
	for _, p := range t.Models {
		if !ts.IsZero() && p.ValidFrom.After(ts) {
			continue
		}
		l := -1
		if p.Model == model {
			l = 1 << 20
		} else if strings.HasPrefix(model, p.Model+"-") || strings.HasPrefix(model, p.Model+"@") {
			l = len(p.Model)
		}
		// rows are sorted by valid_from, so later rows of equal length win
		if l >= 0 && l >= bestLen {
			best, bestLen = p, l
		}
	}
	return best, bestLen >= 0
}

// Rate returns the USD/KRW rate at ts.
func (t *Table) Rate(ts time.Time) float64 {
	r := t.FX[0].USDKRW
	for _, f := range t.FX {
		if ts.IsZero() || !f.ValidFrom.After(ts) {
			r = f.USDKRW
		}
	}
	return r
}

// MicroKRW returns the cost in micro-won and whether the model was priced.
// microKRW = tokens * usdPerMillion * fx (the 1e6 factors cancel).
func (t *Table) MicroKRW(u event.Usage, ts time.Time) (int64, bool) {
	p, ok := t.Lookup(u.Model, ts)
	if !ok {
		return 0, false
	}
	fx := t.Rate(ts)
	cw1h := p.CacheWrite1h
	if cw1h == 0 {
		cw1h = 2 * p.Input
	}
	v := float64(u.In)*p.Input + float64(u.Out)*p.Output + float64(u.CacheRead)*p.CacheRead +
		float64(u.CacheWrite-u.CacheWrite1h)*p.CacheWrite + float64(u.CacheWrite1h)*cw1h
	return int64(math.Round(v * fx)), true
}

// Apply prices every event of a session in place.
func (t *Table) Apply(evs []*event.Event) {
	for _, ev := range evs {
		if ev.Usage.Total() == 0 {
			ev.CostMicroKRW = 0
			ev.Priced = true
			continue
		}
		ev.CostMicroKRW, ev.Priced = t.MicroKRW(ev.Usage, ev.TS)
	}
}

// RoundLines converts micro-won amounts to whole won so that the sum of the
// rounded lines equals the rounded total; the residue goes to the largest line.
func RoundLines(micro []int64) []int64 {
	out := make([]int64, len(micro))
	var total int64
	largest := -1
	for i, m := range micro {
		total += m
		out[i] = roundWon(m)
		if largest < 0 || m > micro[largest] {
			largest = i
		}
	}
	var sum int64
	for _, v := range out {
		sum += v
	}
	if largest >= 0 {
		out[largest] += roundWon(total) - sum
	}
	return out
}

// RoundTo rounds micro-won lines to won so that they sum exactly to target;
// the difference goes to the largest line.
func RoundTo(micro []int64, target int64) []int64 {
	out := make([]int64, len(micro))
	largest := -1
	var sum int64
	for i, m := range micro {
		out[i] = roundWon(m)
		sum += out[i]
		if largest < 0 || m > micro[largest] {
			largest = i
		}
	}
	if largest >= 0 {
		out[largest] += target - sum
	}
	return out
}

func roundWon(micro int64) int64 {
	if micro >= 0 {
		return (micro + 500_000) / 1_000_000
	}
	return -((-micro + 500_000) / 1_000_000)
}

// Won rounds micro-won to whole won.
func Won(micro int64) int64 { return roundWon(micro) }

// SetFXLine renders a YAML snippet for `price set-fx`.
func SetFXLine(rate float64, from time.Time) string {
	return fmt.Sprintf("  - usd_krw: %g\n    valid_from: %s\n", rate, from.Format("2006-01-02"))
}

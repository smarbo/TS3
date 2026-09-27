package book

import (
	"hash/crc32"
	"sort"
	"strconv"
	"strings"

	"ts3/internal/domain"
)

type rawLevel struct{ price, size string }

func rawLevels(levels []domain.Level) map[string]rawLevel {
	out := make(map[string]rawLevel, len(levels))
	for _, l := range levels {
		p, q := l.SourcePrice, l.SourceSize
		if p == "" {
			p = l.Price
		}
		if q == "" {
			q = l.Size
		}
		out[l.Price] = rawLevel{p, q}
	}
	return out
}
func cloneRaw(in map[string]rawLevel) map[string]rawLevel {
	out := make(map[string]rawLevel, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
func sortedPrices(in map[string]rawLevel, high bool) []string {
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		c := domain.CompareDecimal(keys[i], keys[j])
		if high {
			return c > 0
		}
		return c < 0
	})
	return keys
}
func truncate(book map[string]string, raw map[string]rawLevel, depth int, high bool) {
	keys := sortedPrices(raw, high)
	if len(keys) <= depth {
		return
	}
	for _, p := range keys[depth:] {
		delete(book, p)
		delete(raw, p)
	}
}
func verifyKrakenChecksum(bids, asks map[string]rawLevel, want string) bool {
	n, err := strconv.ParseUint(want, 10, 32)
	if err != nil {
		return false
	}
	return krakenChecksum(bids, asks) == uint32(n)
}

func krakenChecksum(bids, asks map[string]rawLevel) uint32 {
	var b strings.Builder
	for _, part := range []struct {
		levels map[string]rawLevel
		high   bool
	}{{asks, false}, {bids, true}} {
		keys := sortedPrices(part.levels, part.high)
		if len(keys) > 10 {
			keys = keys[:10]
		}
		for _, p := range keys {
			v := part.levels[p]
			for _, s := range []string{v.price, v.size} {
				s = strings.ReplaceAll(s, ".", "")
				s = strings.TrimLeft(s, "0")
				if s == "" {
					s = "0"
				}
				b.WriteString(s)
			}
		}
	}
	return crc32.ChecksumIEEE([]byte(b.String()))
}

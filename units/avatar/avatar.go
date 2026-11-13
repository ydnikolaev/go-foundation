// Package avatar is a fixed set of generated avatar pictures: each key names one deterministic SVG, a mirrored 5×5
// mosaic drawn from a hash of the key on a fixed palette. A user picks one instead of uploading a picture.
//
// The set is part of the stored data: a key once handed out must keep its picture, so the keys, the palette and the
// drawing are frozen (avatar_test pins a digest of every picture). Adding keys at the end is safe; editing or
// reordering is not.
//
// Standard library only.
package avatar

import (
	"hash/fnv"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
)

// keys is the set, in display order.
var keys = []string{
	"k-01", "k-02", "k-03", "k-04", "k-05", "k-06", "k-07", "k-08",
	"k-09", "k-10", "k-11", "k-12", "k-13", "k-14", "k-15", "k-16",
}

// The palette: three paper grounds and eight accents.
var (
	grounds = []string{"#FBF9F4", "#F2EFE8", "#ECE7DD"}
	accents = []string{"#EF709D", "#1A936F", "#F7D046", "#805D93", "#FF9DBF", "#4FCFA3", "#F9DB66", "#D4B8E4"}
)

// Keys returns every key of the set, in display order. The slice is the caller's.
func Keys() []string { return slices.Clone(keys) }

// IsValid reports whether key names a picture of the set.
func IsValid(key string) bool { return slices.Contains(keys, key) }

// Random returns a key of the set drawn from r. The caller owns the source, so a test can fix it.
func Random(r *rand.Rand) string { return keys[r.IntN(len(keys))] }

// SVG returns the picture of key: the same bytes for the same key, always. An unknown key returns nil.
func SVG(key string) []byte {
	if !IsValid(key) {
		return nil
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	next := splitmix(h.Sum64())

	ground := grounds[next()%uint64(len(grounds))]
	fill := accents[next()%uint64(len(accents))]
	var cells strings.Builder
	on := 0
	for y := range 5 {
		for x := range 3 {
			if next()%100 < 55 {
				continue
			}
			on++
			cell(&cells, x, y, fill)
			if x < 2 {
				cell(&cells, 4-x, y, fill)
			}
		}
	}
	if on == 0 {
		cell(&cells, 2, 2, fill) // never an empty picture
	}

	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 96 96" width="96" height="96">`)
	b.WriteString(`<rect width="96" height="96" fill="` + ground + `"/>`)
	// Scaled about the centre, so the corners stay inside a round crop.
	b.WriteString(`<g transform="translate(48 48) scale(0.8) translate(-48 -48)">`)
	b.WriteString(cells.String())
	b.WriteString(`</g></svg>`)
	return []byte(b.String())
}

// cell draws the mosaic cell at column x, row y.
func cell(b *strings.Builder, x, y int, fill string) {
	b.WriteString(`<rect x="` + strconv.Itoa(8+x*16) + `" y="` + strconv.Itoa(8+y*16) +
		`" width="14" height="14" rx="3" fill="` + fill + `"/>`)
}

// splitmix is the SplitMix64 sequence from seed: written out here, so the pictures never depend on a library's
// generator.
func splitmix(seed uint64) func() uint64 {
	s := seed
	return func() uint64 {
		s += 0x9e3779b97f4a7c15
		z := s
		z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
		z = (z ^ (z >> 27)) * 0x94d049bb133111eb
		return z ^ (z >> 31)
	}
}

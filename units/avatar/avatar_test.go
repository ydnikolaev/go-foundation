package avatar

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"io"
	"math/rand/v2"
	"testing"
)

// pinned freezes every picture: a key handed out keeps its picture, so a change to the keys, the palette or the
// drawing fails here (a new key is added to the map, an old digest is never edited).
var pinned = map[string]string{
	"k-01": "ec50a3c389689c2d2ba42f54910ec109e0dd0d07e1dda5d1b33f228897d5976e",
	"k-02": "3a9f60f2742e9b6530bdafdd2cef06a12081795040f307c65769a271a743aedf",
	"k-03": "ffe8bbe95d0e9304363055c0dc23f0d0e174386f44499d981c9c3d2288454f32",
	"k-04": "ad6163daa0f87cfe054874ce71f1460b3ea5a700c5c4607e9d7a87d24c5f5e16",
	"k-05": "1b72e820a047abc9254f4445758094936082bbd38ed1107f4833d5e111796380",
	"k-06": "ae7beefd426ce48ad1c5ca760d26c9586b48f14ecd9e8f323ef8cf5fb17bb3de",
	"k-07": "b92fe5c1e93e935d929cdfc77ffab249e84836c37cb60febe44788d47b014fe7",
	"k-08": "71740d2e1aee34b36d493aac9cda08502aa9d4af2430813ba2da1116bea9fc12",
	"k-09": "57baedbef90b4bd70a3b1ffae83f5037c8178d6376f2e6d2d55b2239a0ce3a70",
	"k-10": "2bb37f9210831b9db9942695827f0ec05c04ef9b41c8596de92bc62e3df3c80a",
	"k-11": "aa1510c1b41aa35707a965c49a3e8c2baf3e17b09f5482d60fed6569dfff9d2a",
	"k-12": "301062bdd8efeb98a4564e75f2535828380f22d0fda306c02fd27f93b71cd608",
	"k-13": "c82f1d19db86a7a58f5199640a086219c985deea50f68c602638fe376123e75a",
	"k-14": "9b587adb5ae0465f05b22030a641873a05a3339e0f3f23eecd463f280bbeb925",
	"k-15": "280b417bd58612a7c809e6564d3d892a748a3e52048039e8821f830201d5a577",
	"k-16": "ea2a3a3a203217cac5c644ea115f761738175e74339a4f962c2cfdfc676436ce",
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// TestSVGByteStable: every key draws the same bytes on every call and the pinned bytes across releases.
func TestSVGByteStable(t *testing.T) {
	t.Parallel()
	for _, k := range Keys() {
		a, b := SVG(k), SVG(k)
		if len(a) == 0 || !bytes.Equal(a, b) {
			t.Fatalf("SVG(%q) is empty or differs between calls", k)
		}
		if got, want := digest(a), pinned[k]; got != want {
			t.Errorf("SVG(%q) digest %s, pinned %q: the picture changed", k, got, want)
		}
	}
	if len(pinned) != len(Keys()) {
		t.Errorf("%d pinned digests for %d keys", len(pinned), len(Keys()))
	}
}

// TestSVGWellFormed: every picture is one well-formed <svg> element.
func TestSVGWellFormed(t *testing.T) {
	t.Parallel()
	for _, k := range Keys() {
		dec := xml.NewDecoder(bytes.NewReader(SVG(k)))
		var root string
		for {
			tok, err := dec.Token()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatalf("SVG(%q): %v", k, err)
			}
			if s, ok := tok.(xml.StartElement); ok && root == "" {
				root = s.Name.Local
			}
		}
		if root != "svg" {
			t.Errorf("SVG(%q): root element %q, want svg", k, root)
		}
	}
}

// TestSetDistinct: at least 12 keys, no key twice, no two keys with the same picture.
func TestSetDistinct(t *testing.T) {
	t.Parallel()
	keys := Keys()
	if len(keys) < 12 {
		t.Fatalf("%d keys, want at least 12", len(keys))
	}
	seenKey, seenSVG := map[string]bool{}, map[string]string{}
	for _, k := range keys {
		if seenKey[k] {
			t.Errorf("key %q twice", k)
		}
		seenKey[k] = true
		d := digest(SVG(k))
		if other, ok := seenSVG[d]; ok {
			t.Errorf("keys %q and %q draw the same picture", other, k)
		}
		seenSVG[d] = k
	}
}

// TestKeysIsACopy: the caller cannot change the set through the slice it got.
func TestKeysIsACopy(t *testing.T) {
	t.Parallel()
	k := Keys()
	first := k[0]
	k[0] = "changed"
	if Keys()[0] != first {
		t.Fatal("Keys returned the set itself")
	}
}

// TestIsValid: every key of the set is valid; anything else is not, and draws nothing.
func TestIsValid(t *testing.T) {
	t.Parallel()
	for _, k := range Keys() {
		if !IsValid(k) {
			t.Errorf("IsValid(%q) = false", k)
		}
	}
	for _, k := range []string{"", "k-00", "k-1", "K-01", "k-01 ", "fox", "../k-01", "k-99"} {
		if IsValid(k) {
			t.Errorf("IsValid(%q) = true", k)
		}
		if SVG(k) != nil {
			t.Errorf("SVG(%q) drew a picture for an unknown key", k)
		}
	}
}

// TestRandom: every draw is a valid key, a fixed source gives a fixed sequence, and draws cover the set.
func TestRandom(t *testing.T) {
	t.Parallel()
	r1, r2 := rand.New(rand.NewPCG(1, 2)), rand.New(rand.NewPCG(1, 2))
	seen := map[string]bool{}
	for range 1000 {
		k := Random(r1)
		if !IsValid(k) {
			t.Fatalf("Random returned %q", k)
		}
		if k2 := Random(r2); k2 != k {
			t.Fatalf("the same source gave %q and %q", k, k2)
		}
		seen[k] = true
	}
	if len(seen) != len(Keys()) {
		t.Errorf("1000 draws covered %d of %d keys", len(seen), len(Keys()))
	}
}

# avatar

A fixed set of generated avatar pictures. Each of 16 keys (`k-01` to `k-16`) names one deterministic SVG: a mirrored
5x5 mosaic drawn from an FNV hash of the key on a fixed palette. A user picks a key instead of uploading a picture, and
you store only the key. The set is part of your stored data, so the keys, the palette and the drawing are frozen: the
tests pin a digest of every picture. Standard library only.

## Install

```sh
go get github.com/ydnikolaev/go-foundation/units/avatar@v0.1.0
```

go-foundation is public: no GOPRIVATE, no token, no vendoring.

## Wire

1. Offer the set: `avatar.Keys()` returns the keys in display order (a copy, yours to keep).
2. Accept a choice: store the key the user picked after `avatar.IsValid(key)`; refuse anything else.
3. Give a new user a picture: `avatar.Random(r)` draws a key from your `*rand.Rand`, so a test can fix the source.
4. Serve the picture: `avatar.SVG(key)` returns the same bytes for the same key, always (serve them as
   `image/svg+xml`); an unknown key returns nil.

## Configure

None: the set, the palette and the drawing are fixed. Adding a key at the end of the set is a new release of this unit
with its digest pinned; editing or reordering a key changes a picture someone already chose, and is refused.

## API

<!-- api: devkit foundation api; do not edit -->
```go
FUNCTIONS
func IsValid(key string) bool
func Keys() []string
func Random(r *rand.Rand) string
func SVG(key string) []byte
```

## Example

Draw a key, check it and render its picture (this is `Example`, compiled and run by the unit's tests):

```go
	r := rand.New(rand.NewPCG(1, 2)) // the caller owns the source; a test fixes it
	key := avatar.Random(r)

	fmt.Println(avatar.IsValid(key), len(avatar.Keys()))
	svg := avatar.SVG(key) // same bytes for the same key, always
	fmt.Println(len(svg) > 0, string(svg[:4]))
	fmt.Println(avatar.SVG("no-such-key") == nil)
	// prints "true 16", "true <svg", "true"
```

## Extension points

- `Random` takes a `*rand.Rand` you own: seed it for a repeatable choice.
- The pictures are SVG text with a 96x96 viewBox; scale them in CSS, or crop them round, as you like.

## Do not

- Do not edit or reorder the keys, the palette or the drawing: a key already stored must keep its picture.
- Do not derive a key from a user id and expect a distinct picture per user: there are 16 pictures in all.
- Do not call `SVG` with an unchecked key and write the result: an unknown key returns nil.
- Do not copy this package. Extend it here; a copy carries `go-foundation-copy: avatar@<sha>` and a removal row.

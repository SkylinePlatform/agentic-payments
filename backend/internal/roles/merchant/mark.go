package merchant

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strings"
)

// The picture a fetched offer carries, and the rule it keeps.
//
// # The rule, restated, because a live offer is where it gets tested
//
// CatalogueEntry.ImageURL is root-relative and Validate refuses anything else,
// on the recorded ground that an image from a host this project does not
// control would make a screenshot depend on somebody else's uptime — and issue
// #215 added TestEveryShippedImageURLNamesAFileThatExists after four broken
// images shipped. A fetched offer has no file in this repository to point at,
// so something has to give. Issue #243 named three ways and none of them is
// free:
//
//   - **Point at the shop's CDN**, and relax the rule for offers that did not
//     come from the file. Simple, and it is the one option that can put a
//     broken image on the screen — which is precisely the state the rule exists
//     to prevent, and precisely what #215 spent a pull request ending. It also
//     concedes the rule rather than answering it: "no host we do not control"
//     would become "no host we do not control, except here".
//   - **No image at all**, and let the column render empty. It cannot be done
//     honestly from this side of the tree: the product table renders an img per
//     row, and `src=""` resolves to the page itself, so "no image" ships as a
//     broken-image placeholder rather than as blank space. Making it blank is a
//     change to frontend/, which is not what a merchant deciding what it sells
//     should require.
//   - **A mark, derived from the offer's identifier** — issue #160's answer for
//     the sixty derived offers, which is already what most of the shelf shows.
//
// # What is different here, and why it is stricter rather than looser
//
// The third one is taken, with the picture inlined as a `data:` URI instead of
// written to a file. That is not a relaxation of the rule: a data URI **is** the
// image, so it depends on no host at all, cannot 404, and cannot be fetched
// even by accident. A root-relative path is the weaker of the two — it still
// needs a file to exist at the other end, which is the failure #215 was about.
//
// So the rule Validate keeps is unchanged in what it protects and now has two
// shapes, one per kind of offer: **an offer from the file names a file this
// repository ships; a fetched offer carries its picture with it. Neither may
// point at a host.**
//
// # It is a second implementation of tools/catalogue/mark.go, and that is checked
//
// The two cannot share code: tools/catalogue is a separate Go module and a
// `package main`, so nothing here can import it and nothing there can import
// this. What stops the copy drifting is not care, it is
// TestALiveMarkIsTheMarkThisShopAlreadyDraws — it re-derives every derived
// picture committed under frontend/public and compares byte for byte, so a
// change to either implementation fails in Go rather than showing up as a shelf
// where half the pictures came out of a different program.
//
// Drawing it the same way is the point rather than an economy: the fetched half
// and the derived half sit in one table, and a viewer should read them as one
// shop.
//
// # And what a mark deliberately does not do
//
// It claims nothing. A mark is not a photograph of a product and does not
// pretend to be — which matters more for a fetched offer than for a derived
// one, because everything else about a fetched offer is real. Real title, real
// category, real price, a signed mandate beside it on the screen. A photograph
// there would invite a reader to believe a purchase happened at a shop that
// sells things, and nothing in this repository moves money or enrols a card.

// The seven colours frontend/src/styles.css declares for the light theme, as
// literal hex.
//
// Copied rather than referenced, on the same ground tools/catalogue gives: an
// SVG reached through an img tag is isolated from the page's stylesheet, so a
// custom property would simply fail to resolve. A data URI is reached the same
// way and is isolated for the same reason.
const (
	markWash     = "#e8dfcc"
	markInk      = "#10243a"
	markGraphite = "#546375"
	markSignal   = "#1f5fbf"
	markSeal     = "#1f6b4a"
	markBroken   = "#a14917"
)

// markAccents are the four a mark may pick its second colour from. paper is left
// out because it is the page behind the card, and wash is the card itself.
var markAccents = []string{markSignal, markSeal, markBroken, markGraphite}

// markDataURI is a mark for one offer, as the `data:` URI its image_url carries.
//
// Base64 rather than percent-encoding: an SVG holds `<`, `>`, `"`, `#` and `%`,
// every one of which has to be escaped in a URI, and one missed escape is an
// image that fails to render on some browsers and not others. Base64 costs a
// third more bytes and has no such edge.
func markDataURI(id, category, title string) string {
	return markDataURIPrefix +
		base64.StdEncoding.EncodeToString(markSVG(id, category, title, liveMarkNote(id)))
}

// liveMarkNote is the comment inside a live offer's mark, and it is the one
// thing about the picture that is *not* byte-identical to the file
// tools/catalogue would have written for the same offer.
//
// That file's comment says the picture was generated by tools/catalogue and
// that `make catalogue` rewrites the directory it sits in. Neither sentence is
// true of a mark drawn at start-up and carried inside an offer, and copying it
// across for the sake of a simpler comparison would put a false statement of
// provenance inside an artefact — in a repository whose whole subject is
// artefacts that say where they came from. The comparison keeps its exactness
// instead by making this block an argument: see
// TestALiveMarkIsTheMarkThisShopAlreadyDraws, which passes the other one.
func liveMarkNote(id string) string {
	return "    Drawn by the merchant from " + id + ", and carried inside the offer rather\n" +
		"    than fetched, so this picture depends on no host and cannot 404. The\n" +
		"    colours are the light-theme tokens frontend/src/styles.css declares, as\n" +
		"    literal hex.\n"
}

// markDataURIPrefix is what a live offer's image_url starts with, and what
// Validate checks for. One constant, so the writer and the rule cannot disagree.
const markDataURIPrefix = "data:image/svg+xml;base64,"

// markSVG draws one offer's picture.
//
// A four by four grid, mirrored down the middle so the result is symmetrical
// and therefore reads as a mark rather than as noise, with each cell's shape
// and colour taken from the hash of the identifier. The frame is the hand-drawn
// four's, to the pixel.
//
// Byte-identical to tools/catalogue/mark.go's `mark` for the same offer, given
// that program's own comment block as note — which is the one thing the two
// implementations legitimately disagree about, and the reason note is an
// argument. See liveMarkNote, and the test that holds the rest to the byte.
func markSVG(id, category, title, note string) []byte {
	sum := sha256.Sum256([]byte("mark\x00" + id))
	accent := markAccents[markDraw(category, "accent", len(markAccents))]

	// Two columns decided and two mirrored. Eight cells, each taking two bits
	// of shape and one of colour out of its own byte, so a cell's appearance
	// depends on its position as well as on the offer.
	const cells = 8
	shapes := make([]int, cells)
	accented := make([]bool, cells)
	filled := 0
	for i := range cells {
		shapes[i] = int(sum[i] % 4)
		accented[i] = sum[i+cells]%3 == 0
		if shapes[i] != 0 {
			filled++
		}
	}
	if filled == 0 {
		// A blank card is the one outcome that would look like a mistake rather
		// than like a mark. Two cells rather than one, so the mirrored result is
		// still a shape.
		shapes[0], shapes[5] = 1, 2
	}

	var b strings.Builder
	b.WriteString(`<svg viewBox="0 0 200 200" xmlns="http://www.w3.org/2000/svg" role="img" aria-hidden="true">` + "\n")
	b.WriteString("  <!--\n")
	b.WriteString(note)
	b.WriteString("  -->\n")
	b.WriteString("  <title>" + markEscape(title) + "</title>\n")
	fmt.Fprintf(&b, "  <rect x=\"6\" y=\"6\" width=\"188\" height=\"188\" rx=\"20\" fill=%q stroke=%q stroke-width=\"3\"/>\n",
		markWash, markInk)

	for row := range 4 {
		for column := range 4 {
			cell := row*2 + min(column, 3-column)
			shape := shapes[cell]
			if shape == 0 {
				continue
			}
			colour := markInk
			if accented[cell] {
				colour = accent
			}
			cx := 52 + 32*column
			cy := 52 + 32*row
			switch shape {
			case 1:
				fmt.Fprintf(&b, "  <circle cx=\"%d\" cy=\"%d\" r=\"12\" fill=%q/>\n", cx, cy, colour)
			case 2:
				fmt.Fprintf(&b, "  <rect x=\"%d\" y=\"%d\" width=\"24\" height=\"24\" rx=\"6\" fill=%q/>\n",
					cx-12, cy-12, colour)
			default:
				fmt.Fprintf(&b, "  <path d=\"M%d,%d L%d,%d L%d,%d L%d,%d Z\" fill=%q/>\n",
					cx, cy-13, cx+13, cy, cx, cy+13, cx-13, cy, colour)
			}
		}
	}
	b.WriteString("</svg>\n")
	return []byte(b.String())
}

// markDraw is a stable choice in [0, n) from a string and a purpose.
func markDraw(id, purpose string, n int) int {
	sum := sha256.Sum256([]byte(purpose + "\x00" + id))
	return int(binary.BigEndian.Uint64(sum[:8]) % uint64(n))
}

// markEscape makes a string safe as XML text. The titles come from somebody
// else's shop, and an ampersand in one would otherwise produce an image no
// browser will parse.
func markEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

package merchant

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/merchant/shop"
)

// The picture a fetched offer shows, the mark it falls back to, and what issue
// #300 gave up to get there.
//
// # What this file used to say, and what changed
//
// CatalogueEntry.ImageURL is root-relative and Validate refuses anything else,
// on the recorded ground that an image from a host this project does not
// control would make a screenshot depend on somebody else's uptime — and issue
// #215 added TestEveryShippedImageURLNamesAFileThatExists after four broken
// images shipped. A fetched offer has no file in this repository to point at,
// so something had to give. Issue #243 named three ways:
//
//   - **Point at the shop's CDN**, and relax the rule for offers that did not
//     come from the file.
//   - **No image at all**, and let the column render empty.
//   - **A mark, derived from the offer's identifier** — issue #160's answer for
//     the sixty derived offers, which is what most of the shelf shows.
//
// #243 took the third and rejected the first, on the ground that it is the one
// option that can put a broken image on the screen, and that "no host we do not
// control" would become "no host we do not control, except here".
//
// **Issue #300 reversed that, knowingly.** The first is now what a fetched offer
// shows when the shop supplies one: `image_url` is the shop's own
// `thumbnail`, an https URL on a CDN nobody here operates, and the browser
// loads it. Both objections above still stand — nothing about them turned out
// to be wrong — and what outweighed them is that everything else on a fetched
// row is the shop's. Real title, real category, real price, a signed mandate
// beside it. A drawn mark in that row is the one cell that is this project's
// invention, and on a shelf whose whole argument is *"this stock is not ours and
// the guarantees still hold"* it read as the shelf being ours after all.
//
// The second option is still rejected and for the reason it always was: the
// product table renders an img per row and `src=""` resolves to the page
// itself, so "no image" ships as a broken-image placeholder rather than as
// blank space. That is what makes the fallback below mandatory rather than
// tidy.
//
// # So there are three shapes now, and only one of them is new
//
//   - **An offer the file lists names a file this repository ships.**
//     Unchanged, and every rejection that used to apply to it still does —
//     `http://`, `https://`, `//` and `data:` are all refused. The 63 committed
//     offers are exactly what they were.
//   - **A fetched offer shows the shop's photograph**, as an https URL. New,
//     and the concession.
//   - **A fetched offer the shop gave no usable photograph for carries a drawn
//     mark**, inlined as a `data:` URI. This is #243's answer kept as the
//     fallback rather than deleted — see pictureFor, which decides between the
//     two.
//
// # What was given up, stated so a reader can disagree with it
//
// A fetched row can now render broken, which is the state #215 spent a pull
// request ending. It renders broken when cdn.dummyjson.com is down, when the
// shop moves a path, or when whoever is watching has no route to it — and it
// renders broken *silently*, because both img tags carry `alt=""`.
//
// The substance check goes with it. Validate asks two questions of a committed
// path (is it shaped right, and is a file actually there) and two of a data URI
// (does it decode, and is what comes out an SVG). It can ask only the first of
// an https URL, because the second needs a request and hard rule 4 forbids one.
// That asymmetry is not an oversight to close later; it is the thing being
// traded away, and validateImage says so where it makes the trade.
//
// What is **not** given up: `make demo`, `make check`, `make vectors` and CI
// still reach no network, and no test does. Only a browser looking at
// `make demo-live` fetches anything, and only for the fetched half of the shelf.
//
// # The Content-Security-Policy line this now needs
//
// There is no CSP anywhere in this repository — not in frontend/index.html, not
// in vite.config.ts, and the backend sets exactly two response headers, neither
// of them this — so nothing blocks either shape. But `img-src 'self'` is the
// obvious first line anybody adding a policy writes, and it would blank every
// fetched offer's picture at once. Since #300 the policy that keeps this screen
// working is:
//
//	img-src 'self' data: https://cdn.dummyjson.com
//
// `data:` for the fallback marks, and the host for the photographs. NOTICE
// carries the same line, because a policy is the place a reader would look for
// which third-party hosts a page talks to, and NOTICE is where this repository
// answers that question.
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
// pretend to be, which is why the sixty derived offers get one and why a
// fetched offer with no photograph gets one rather than a stand-in picture of
// something else.
//
// That argument used to run one step further and say a *photograph* on a
// fetched row would invite a reader to believe a purchase happened at a shop
// that sells things. #300 does not accept the step: what a photograph on that
// row says is that DummyJSON lists this product with this picture, which is
// true, and the reason nothing here is bought for real is that no card is ever
// enrolled and no money moves — which is stated in NOTICE, in the scope section
// of AGENTS.md and on the screen, none of which a drawn square was ever the
// load-bearing part of.

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
//
// Which of the four an offer draws in is seeded on its identifier, and issue
// #236 is where that was decided: it was seeded on the category, and a category
// is something a whole shelf shares, so every shelf came out in one colour.
//
// # The numbers for this shelf, which are not the catalogue's
//
// Measured over the recording at shop/data — 194 products across 24 categories,
// which is what the live shop answered when it was taken. Under the category
// seeding, **24 of the 24 categories drew in a single accent**, and seal carried
// 68 of the 194. Seeding on the identifier makes it 0 of 24, with the largest
// share 54.
//
// Issue #279 is why that is spelled out rather than borrowed. The wording here
// used to quote tools/catalogue's figures — six shelves over four accents,
// graphite on a third of sixty — beside a drawing for a different shop, where
// graphite never carried a third and was not even the accent that monopolised.
// The catalogue's numbers are true where they are written and were false here.
//
// The argument for scattering rather than for giving each shelf a colour of its
// own is written out once, beside the seeding in tools/catalogue/mark.go's
// accentOf, and deliberately not restated here. Two copies of a decision drift;
// two copies of a *drawing* are held to the byte by
// TestALiveMarkIsTheMarkThisShopAlreadyDraws, which is why the duplication below
// is safe and this paragraph is a pointer.
var markAccents = []string{markSignal, markSeal, markBroken, markGraphite}

// pictureFor is the choice this file's header argues: the shop's photograph
// where there is one, and a drawn mark where there is not.
//
// # Why the shop is not simply believed
//
// `thumbnail` is a string somebody else's server chose, and several of the
// values it could hold would each put a broken image on the screen: the empty
// string, which resolves to the page itself; anything not carrying a host, which
// resolves against whichever page rendered it; `http://` on a page served over
// https, which a browser blocks as mixed content and never draws; and a value
// with whitespace or a quote in it, which is a string on its way into an img
// tag. So the question asked here is not "did the shop send something" but "is
// this a thing a browser will load", and everything that fails it gets a mark.
// The whole of the rule is `https://`, a host after it, and no whitespace or
// quote anywhere in it.
//
// # Why a bad thumbnail costs a picture and not the row
//
// It could be refused instead — Validate would then stop the merchant, because
// a fetched offer's image_url is checked and one it cannot accept fails the
// merged catalogue. That is the wrong end of the same asymmetry
// decodeDummyJSON already states: a constraint nobody understands is refused
// because it is a limit a user set, and a row in somebody else's placeholder
// shop is nobody's claim about anything. One malformed CDN path out of 194
// should not be the difference between `make demo-live` running and not, and
// the fallback costs exactly one drawn square.
//
// The **committed** half is untouched by all of this. pictureFor is reached
// only from entryFor, which only Extend calls.
func pictureFor(p shop.Product) string {
	host, over := strings.CutPrefix(p.Thumbnail, liveImagePrefix)
	if over && host != "" && !strings.ContainsAny(p.Thumbnail, liveImageForbidden) {
		return p.Thumbnail
	}
	return markDataURI(p.ID, p.Title)
}

// liveImagePrefix is what a fetched offer's photograph starts with, and what
// Validate checks for. One constant for the same reason markDataURIPrefix is
// one: the writer and the rule cannot disagree about it.
//
// The scheme and nothing else — not the shop's host. A merchant reads its stock
// through shop.Fetcher and does not know which shop answered, so a rule naming
// cdn.dummyjson.com would be this file knowing something the interface exists to
// keep from it, and the second shop would arrive as a change here rather than as
// a second file in shop/.
const liveImagePrefix = "https://"

// liveImageForbidden is what a fetched offer's photograph may not carry
// anywhere in it: whitespace, and the character that ends an HTML attribute.
//
// One constant for the reason liveImagePrefix is one, and it was two literals
// until the architect review of #300 pointed out that the reason applies to
// both halves of the rule. pictureFor decides what to put in an offer and
// validateImage decides what a catalogue may hold; if those two disagree about
// a character, the disagreement is silent in one direction — a photograph
// pictureFor allows and validateImage refuses fails the merged catalogue and
// stops `make demo-live` — and invisible in the other.
//
// **It is checked one character at a time**, in
// TestEveryCharacterAFetchedPictureMayNotCarryIsRefusedOnItsOwn, derived from
// this constant rather than written out beside it. That test exists because
// the tables that came before it covered the class with two rows carrying a
// quote *and* a space, so each of those two characters could be deleted from
// here with nothing going red — an assertion reddening only under some other
// mutation, which is the shape AGENTS.md names.
const liveImageForbidden = " \t\r\n\""

// markDataURI is a mark for one offer, as the `data:` URI its image_url carries.
//
// Base64 rather than percent-encoding: an SVG holds `<`, `>`, `"`, `#` and `%`,
// every one of which has to be escaped in a URI, and one missed escape is an
// image that fails to render on some browsers and not others. Base64 costs a
// third more bytes and has no such edge.
func markDataURI(id, title string) string {
	return markDataURIPrefix +
		base64.StdEncoding.EncodeToString(markSVG(id, title, liveMarkNote(id)))
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
// **It cannot see the offer's category, and that is the point.** It took one
// until issue #236, for the accent, and the parameter is gone rather than merely
// unread: a mark claims nothing about what its offer is, and the way to keep a
// rule like that is to leave the drawing nothing to claim it with. Restoring the
// category-seeded accent *here* is now a signature change rather than a one-word
// one — the same reasoning AGENTS.md gives for joseVerifier having no KeyID
// method.
//
// What it does not do is stop the caller handing the shelf over, because a
// category is a string and so is an identifier: markDataURI(p.Category, p.Title)
// in entryFor compiles, and it would draw one picture per category for every
// fetched offer. The signature cannot reach that and a test has to, which is
// TestAFetchedOffersPictureIsDrawnFromItsOwnIdentifier.
//
// Byte-identical to tools/catalogue/mark.go's `mark` for the same offer, given
// that program's own comment block as note — which is the one thing the two
// implementations legitimately disagree about, and the reason note is an
// argument. See liveMarkNote, and the test that holds the rest to the byte.
func markSVG(id, title, note string) []byte {
	sum := sha256.Sum256([]byte("mark\x00" + id))
	accent := markAccents[markDraw(id, "accent", len(markAccents))]

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

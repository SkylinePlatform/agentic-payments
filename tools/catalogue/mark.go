package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// derivedDir is the one subdirectory of the image directory this program owns.
//
// Everything in it is emptied and rewritten on every run; nothing beside it is
// opened. That separation is the whole of what keeps the four illustrations
// issue #215 drew by hand safe from a generator that knows nothing about them.
const derivedDir = "derived"

// The seven colours frontend/src/styles.css declares for the light theme, as
// literal hex.
//
// Copied rather than referenced for the reason the hand-drawn four give: these
// files are loaded through an img tag, and a document reached that way is
// isolated from the page's stylesheet, so a custom property here would simply
// fail to resolve. Nothing below names an eighth colour.
const (
	wash     = "#e8dfcc"
	ink      = "#10243a"
	graphite = "#546375"
	signal   = "#1f5fbf"
	seal     = "#1f6b4a"
	broken   = "#a14917"
)

// accents are the four a mark may pick its second colour from. paper is left out
// because it is the page behind the card, and wash is the card itself.
var accents = []string{signal, seal, broken, graphite}

// writeMarks empties the derived image directory and writes one mark per offer.
//
// # Why a mark per offer, and not five drawings shared out
//
// Sixty offers is sixty pictures, and issue #160 named the three ways to get
// them. Fetching is out before the argument starts: `image_url` is root-relative
// and refused at load if it is not, on the recorded ground that an image from a
// host this project does not control would make a screenshot depend on somebody
// else's uptime and would put a network call one careless test away.
//
// **A small set of category illustrations reused across offers** was the
// cheapest answer and is the wrong one. A table where every third row repeats
// the same drawing does not read as a shop, it reads as a mock-up of a shop —
// and the screen this repository exists to screenshot is the one place that
// distinction costs something. It also moves the problem down a level rather
// than solving it: the next shelf still needs somebody to draw, which is exactly
// the thing that does not scale.
//
// **Dropping the picture for everything but the heroes** cannot be done
// honestly here. `Validate` refuses an `image_url` that is not root-relative and
// an empty string is not, the product table renders an img for every row, and
// frontend/src belongs to other branches — so "no image" would ship as sixty
// broken-image placeholders, which is the state issue #215 existed to end.
//
// So: **a mark per offer, derived from its identifier**. It is stable across
// runs, so re-running this program is a no-op rather than sixty changed files;
// it is distinct per offer, so the table reads as sixty things; it is drawn from
// the same seven tokens and inside the same frame as the four hand-drawn
// illustrations, so the shelf and the heroes look like one shop; and it claims
// nothing. A mark is not a photograph of a camera, and it does not pretend to
// be — which is the same honesty the hand-drawn four were chosen for, since a
// photograph would be somebody else's copyright.
func writeMarks(imagesPath string, offers []entry) error {
	dir := filepath.Join(imagesPath, derivedDir)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("clear %s: %w", dir, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}

	for _, o := range offers {
		name := filepath.Base(o.ImageURL)
		if err := os.WriteFile(filepath.Join(dir, name), mark(o), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	return os.WriteFile(filepath.Join(dir, "README.md"), []byte(derivedReadme), 0o644)
}

// markURL is where an offer's mark is served from, which is the root-relative
// path its image_url carries.
func markURL(id string) string {
	return "/images/catalogue/" + derivedDir + "/" + slug(id) + ".svg"
}

// slug turns an identifier into a filename. `wd:Q1004258` becomes
// `wd-q1004258`, `route:BEG-AMS` becomes `route-beg-ams`.
func slug(id string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		default:
			return '-'
		}
	}, id)
}

// mark draws one offer's picture.
//
// A four by four grid, mirrored down the middle so the result is symmetrical and
// therefore reads as a mark rather than as noise, with each cell's shape and
// colour taken from the hash of the identifier. The frame is the hand-drawn
// four's, to the pixel.
func mark(o entry) []byte {
	sum := sha256.Sum256([]byte("mark\x00" + o.ID))
	accent := accents[draw(o.Category, "accent", len(accents))]

	// Two columns decided and two mirrored. Eight cells, each taking two bits of
	// shape and one of colour out of its own byte, so a cell's appearance
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
	b.WriteString("    Generated by tools/catalogue from " + o.ID + ". Do not hand-edit: `make\n")
	b.WriteString("    catalogue` rewrites every file in this directory. The colours are the\n")
	b.WriteString("    light-theme tokens frontend/src/styles.css declares, as literal hex.\n")
	b.WriteString("  -->\n")
	b.WriteString("  <title>" + escape(o.Title) + "</title>\n")
	fmt.Fprintf(&b, "  <rect x=\"6\" y=\"6\" width=\"188\" height=\"188\" rx=\"20\" fill=%q stroke=%q stroke-width=\"3\"/>\n",
		wash, ink)

	for row := range 4 {
		for column := range 4 {
			cell := row*2 + min(column, 3-column)
			shape := shapes[cell]
			if shape == 0 {
				continue
			}
			colour := ink
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

// escape makes a string safe as XML text. The titles come from a dataset, and
// an ampersand in one would otherwise produce a file no browser will parse.
func escape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

const derivedReadme = `# derived/

Generated. Every file here is written by ` + "`make catalogue`" + ` — see
` + "`tools/catalogue/mark.go`" + ` — and the whole directory is emptied on every
run, so an edit made here survives exactly until the next one.

The four hand-drawn illustrations live one directory up and are never touched by
that program. If a picture needs to be *drawn* rather than generated, it belongs
beside them, and its offer belongs in ` + "`Heroes`" + `.
`

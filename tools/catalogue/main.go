// Command catalogue derives deploy/catalogue.json, and the picture beside every
// row of it, from a frozen snapshot of an openly licensed dataset.
//
// # Why a generator and not a fetcher
//
// Issue #160's whole argument. The catalogue is a scenario file rather than a
// product list: `prices: [24000, 21000, 18900]` against a cap of 20000 is the
// engine of the Human Not Present demonstration, and a scrape returns one price
// at one moment. The schedule has to be synthesised whichever way the titles
// arrive, so fetching at runtime would not remove the hardcoding — it would move
// it somewhere less visible, and take three properties with it:
//
//   - **Determinism.** Issue #158 requires that the refusal at $210 happen on
//     every run and that a test say so. A catalogue that filled differently each
//     run would make that sentence unwritable.
//   - **AGENTS.md hard rule 4.** No test may depend on an external network call,
//     and `make demo` is what produces the screenshots.
//   - **`image_url` is root-relative and refused at load if it is not**, on the
//     recorded ground that an image from a host this project does not control
//     would make a screenshot depend on somebody else's uptime.
//
// So this is a program a person runs, `make catalogue`, and nothing in CI or in
// `make demo` runs it. It reads `data/`, which is committed, and reaches no
// network itself: the fetching happened once, by hand, and
// `data/PROVENANCE.md` records exactly what was asked for and when.
//
// # Why it is a module of its own
//
// `contracts/tools` and `tools/mockery` are both outside `backend/go.mod` on
// the rule that a code generator is not a dependency of the thing it generates.
// A catalogue generator is the same kind of thing. It is also why this program
// cannot import `internal/roles/merchant` and re-use `CatalogueFile.Validate`:
// Go's `internal` rule scopes that package to `backend/`, and the shape below
// is therefore a second statement of the same JSON. That is deliberate rather
// than tolerated — the loader stays the single authority on what a catalogue may
// say, this program stays a producer of candidate text, and `make check` is what
// judges the result. A generator that validated its own output against its own
// copy of the rules would agree with itself.
//
// # What it will not touch
//
// The **hero offers**: see [Heroes]. They are copied through from whatever the
// file already says, never restated here, so no re-run can move a price several
// tests and every screenshot are written against.
//
// The **hand-drawn images**: only the `derived/` subdirectory of the image
// directory is emptied and rewritten. The four illustrations issue #215 drew sit
// beside it and are never opened — four rather than three because the concert's
// survived issue #244 removing the offer it was drawn for, unreferenced and
// untouched, which deploy/catalogue.json's own $comment records.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	cataloguePath := flag.String("catalogue", filepath.Join("..", "..", "deploy", "catalogue.json"),
		"the catalogue to rewrite; the default resolves from this module's own directory, "+
			"which is where `go -C tools/catalogue run .` leaves the working directory")
	imagesPath := flag.String("images", filepath.Join("..", "..", "frontend", "public", "images", "catalogue"),
		"where the offer images live; a `derived/` subdirectory of it is emptied and rewritten, "+
			"and nothing beside that subdirectory is opened")
	flag.Parse()

	if err := run(*cataloguePath, *imagesPath); err != nil {
		fmt.Fprintln(os.Stderr, "catalogue:", err)
		os.Exit(1)
	}
}

// run is main with the exit path removed, so that a test can drive the whole
// program rather than the pieces it is made of.
func run(cataloguePath, imagesPath string) error {
	existing, err := readCatalogue(cataloguePath)
	if err != nil {
		return err
	}

	derived, err := derive()
	if err != nil {
		return err
	}

	rewritten, err := existing.rewrite(derived)
	if err != nil {
		return err
	}

	if err := writeMarks(imagesPath, derived); err != nil {
		return err
	}
	if err := writeCatalogue(cataloguePath, rewritten); err != nil {
		return err
	}

	fmt.Printf("catalogue: %d offers (%d kept, %d derived) -> %s\n",
		len(rewritten.Offers), len(rewritten.Offers)-len(derived), len(derived), cataloguePath)
	fmt.Printf("catalogue: %d marks -> %s\n", len(derived), filepath.Join(imagesPath, derivedDir))
	return nil
}

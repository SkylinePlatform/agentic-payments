package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tree this program writes into, from this module's own directory — which
// is where `go test ./...` leaves the working directory, and where the flags in
// main.go resolve their defaults from.
const (
	cataloguePath = "../../deploy/catalogue.json"
	imagesPath    = "../../frontend/public/images/catalogue"
)

// TestTheCommittedCatalogueIsWhatThisProgramProduces is the one that makes the
// word "derived" true rather than a claim about how the file was made once.
//
// A generator run by a person, never by CI, has an obvious failure mode: the
// file it wrote gets edited by hand afterwards, and from then on the program and
// the tree disagree about what the shop sells with nothing to say so. Re-running
// the derivation and comparing with what is committed closes that, and closes it
// without the network — the snapshot is embedded, the arithmetic is a hash of
// each identifier, so this is a pure function being checked against its own
// recorded output.
//
// It is also what makes issue #158's sentence hold one shelf over. "The refusal
// at $210 happens on every run, and a test says so rather than a reader hoping"
// is a claim about the flight's prices; that the other sixty offers' prices are
// equally fixed is a claim about this program, and this is where it is made.
func TestTheCommittedCatalogueIsWhatThisProgramProduces(t *testing.T) {
	t.Parallel()

	committed, err := os.ReadFile(cataloguePath)
	require.NoError(t, err, "the catalogue this program writes is not in the tree")

	existing, err := readCatalogue(cataloguePath)
	require.NoError(t, err)
	derived, err := derive()
	require.NoError(t, err)
	rewritten, err := existing.rewrite(derived)
	require.NoError(t, err)
	rendered, err := render(rewritten)
	require.NoError(t, err)

	assert.Equal(t, string(committed), string(rendered),
		"deploy/catalogue.json is not what `make catalogue` writes, so either it has been "+
			"hand-edited since or the derivation has moved without being re-run; either way the "+
			"file and the program that owns it disagree about what the shop sells")
}

// TestEveryDerivedMarkIsTheOneCommittedBesideIt is the same claim for the
// pictures, and it is not covered by the one above.
//
// The catalogue names an image path; TestEveryShippedImageURLNamesAFileThatExists,
// over in internal/roles/merchant, says a file is there. Neither says the file
// is the one this program draws — so a mark edited by hand, or a change to the
// drawing that nobody re-ran, would pass both.
func TestEveryDerivedMarkIsTheOneCommittedBesideIt(t *testing.T) {
	t.Parallel()

	derived, err := derive()
	require.NoError(t, err)

	for _, o := range derived {
		t.Run(o.ID, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(imagesPath, derivedDir, filepath.Base(o.ImageURL))
			committed, err := os.ReadFile(path)
			if !assert.NoError(t, err, "%s names a picture that is not in the tree", o.ID) {
				return
			}
			assert.Equal(t, string(mark(o)), string(committed),
				"the mark committed for %s is not the one this program draws, so `make "+
					"catalogue` has not been run since the drawing changed", o.ID)
		})
	}
}

// TestTheDerivationIsAFunctionOfTheSnapshotAlone is determinism stated directly
// rather than inferred from the two tests above agreeing today.
//
// Nothing here reads a clock, a random source or an environment variable, and
// this is what would fail if something started to. A catalogue that filled
// differently between two runs would make every scenario block in it a claim
// that happened to hold when it was written.
func TestTheDerivationIsAFunctionOfTheSnapshotAlone(t *testing.T) {
	t.Parallel()

	first, err := derive()
	require.NoError(t, err)
	second, err := derive()
	require.NoError(t, err)

	assert.Equal(t, first, second,
		"two runs over one snapshot produced different shops, so nothing downstream can assert "+
			"what a search returns")
}

// TestNothingDerivedCollidesWithWhatAScriptedSentenceNarrowsOn is the invariant
// Reserved exists for, checked over what was actually produced rather than over
// the list of exclusions that was meant to produce it.
//
// The cost of getting this wrong is the one worth restating: the agent takes the
// first candidate a search returns and asks nobody, so a second offer matching
// what a sentence narrows on does not fail to load and does not fail to search.
// It changes what the demonstration buys.
func TestNothingDerivedCollidesWithWhatAScriptedSentenceNarrowsOn(t *testing.T) {
	t.Parallel()

	derived, err := derive()
	require.NoError(t, err)

	routes := make(map[[2]string]string, len(derived))
	identifiers := make(map[string]struct{}, len(derived))
	for _, o := range derived {
		assert.NotContains(t, Reserved.Categories, o.Category,
			"%s is on a shelf a scripted sentence claims whole", o.ID)
		assert.NotContains(t, Reserved.Identifiers, o.ID,
			"%s is an identifier a scripted sentence names outright", o.ID)
		assert.NotContains(t, Heroes, o.ID,
			"%s would replace one of the three offers the demonstration is written around", o.ID)

		if _, repeated := identifiers[o.ID]; !assert.False(t, repeated,
			"%s is derived twice, which makes item.id ambiguous to a mandate", o.ID) {
			continue
		}
		identifiers[o.ID] = struct{}{}

		origin, hasOrigin := o.Attributes["route.origin"]
		destination, hasDestination := o.Attributes["route.destination"]
		if !hasOrigin && !hasDestination {
			continue
		}
		assert.True(t, hasOrigin && hasDestination,
			"%s describes half a route, which the loader refuses rather than reading as no route",
			o.ID)

		route := [2]string{origin, destination}
		assert.NotContains(t, Reserved.Routes, route,
			"%s answers to the route the Human Present flow quotes", o.ID)
		first, twice := routes[route]
		assert.False(t, twice, "%s and %s both answer to %s→%s, which is the one thing the "+
			"load-time rule on routes refuses", first, o.ID, origin, destination)
		routes[route] = o.ID
	}
}

// TestEveryDerivedOfferKeepsTheClaimItsScenarioMakes checks the arithmetic in
// price.go against the meaning merchant.Found gives the three values.
//
// The shipped catalogue is checked the same way one tree over, by
// TestEveryOfferFindsItselfWhenItsScenarioSaysItShould, which runs the real
// search. This is the narrower claim and the one that survives a refetch: it is
// about the arithmetic rather than about the sixty offers it happened to
// produce, so a snapshot that changed every price would still be held to it.
func TestEveryDerivedOfferKeepsTheClaimItsScenarioMakes(t *testing.T) {
	t.Parallel()

	derived, err := derive()
	require.NoError(t, err)

	claimed := make(map[string]int, 3)
	for _, o := range derived {
		require.GreaterOrEqual(t, len(o.Prices), 2,
			"%s holds one price, and a Human Not Present watch attempts only on a step change — "+
				"so an offer that never moves is one no watch can act on", o.ID)
		assert.Positive(t, o.Scenario.Cap,
			"%s names a bound of nothing, which no price can sit inside", o.ID)

		opening, settled := o.Prices[0], o.Prices[len(o.Prices)-1]
		assert.Less(t, settled, opening,
			"%s does not come down, so there is no crossing for a screen to show", o.ID)

		claimed[o.Scenario.Found]++
		switch o.Scenario.Found {
		case foundAlways:
			assert.GreaterOrEqual(t, o.Scenario.Cap, opening,
				"%s says a search finds it at every price and its first one is outside the cap",
				o.ID)
		case foundAtTheLastPrice:
			assert.Less(t, o.Scenario.Cap, opening,
				"%s says it is out of range until the schedule runs, and it is in range at once",
				o.ID)
			assert.GreaterOrEqual(t, o.Scenario.Cap, settled,
				"%s says it comes into range and never does, so a prompt going looking for it "+
					"finds nothing however long the demonstration runs", o.ID)
		case foundNever:
			assert.Less(t, o.Scenario.Cap, settled,
				"%s claims to be scenery and falls into range, which makes the search look like "+
					"a delay rather than a filter", o.ID)
		default:
			assert.Fail(t, "unreadable claim",
				"%s says it is found %q, which the loader refuses at load", o.ID, o.Scenario.Found)
		}
	}

	// And all three are actually used. Two of them being unreachable would leave
	// the switch above asserting nothing about a value the file can carry — and
	// foundNever in particular is the whole of what tells a reader that a search
	// returning forty rows filtered something out.
	for _, found := range []string{foundAlways, foundAtTheLastPrice, foundNever} {
		assert.Positive(t, claimed[found],
			"nothing derived claims %q, so a shop that is meant to show a filter working shows "+
				"a list with everything in it", found)
	}
}

// TestADerivedOfferSaysEnoughToBeSoldAtAll covers the fields the loader refuses
// an offer for not having.
//
// The loader is one module away and cannot be imported — Go's internal rule, and
// the reason the package comment gives for that being right — so this is the
// producer's own statement of the same requirements. It is deliberately not the
// whole of Validate: the authority stays over there, `make check` is what
// applies it to the file, and a copy of every rule here would be the second one
// that drifts.
func TestADerivedOfferSaysEnoughToBeSoldAtAll(t *testing.T) {
	t.Parallel()

	derived, err := derive()
	require.NoError(t, err)
	require.NotEmpty(t, derived)

	for _, o := range derived {
		assert.NotEmpty(t, o.Category, "%s carries no category, and a constraint on "+
			"item.category would silently never match it", o.ID)
		assert.NotEmpty(t, o.Title, "%s has nothing to put on the screen the demonstration "+
			"exists to show", o.ID)
		assert.NotEmpty(t, o.Description, "%s leaves a blank cell in the product table", o.ID)
		assert.NotEmpty(t, o.Retailer, "%s does not say who is behind the counter", o.ID)
		assert.NotEmpty(t, o.Attributes, "%s states no facts about itself, so no constraint on "+
			"this kind of purchase can be checked against it", o.ID)
		assert.True(t, strings.HasPrefix(o.ImageURL, "/"), "%s names %q, and an image_url that "+
			"is not root-relative is refused at load", o.ID, o.ImageURL)
		assert.False(t, strings.HasPrefix(o.ImageURL, "//"), "%s points its image at another "+
			"host with the scheme left off", o.ID)
	}
}

// TestRunningTheProgramTwiceChangesNothingTheSecondTime drives run, which is
// the whole program, and it is the only test that reaches the two things run
// does that nothing else can: it writes.
//
// The tests above check the derivation — a pure function against its own
// recorded output. What they cannot see is the half that touches a tree, and
// that half carries the two claims a reader of this module is most entitled to.
// **`make catalogue` is a no-op**, so a person can run it to find out whether it
// is, rather than to discover sixty changed files. And **only `derived/` is
// emptied**, which is the entire mechanism keeping the four illustrations issue
// #215 drew by hand safe from a generator that has never heard of them — stated
// in mark.go as a comment beside an os.RemoveAll, which is the kind of sentence
// that stops being true without anything failing.
//
// It runs against a copy in a temporary tree rather than against the real one,
// because a test that wrote the repository would be a build that regenerated the
// catalogue — the one thing the Makefile, the CI workflow and this module's own
// doc comment all say must not happen. Both claims are the same either way.
func TestRunningTheProgramTwiceChangesNothingTheSecondTime(t *testing.T) {
	t.Parallel()

	committed, err := os.ReadFile(cataloguePath)
	require.NoError(t, err)

	dir := t.TempDir()
	catalogue := filepath.Join(dir, "catalogue.json")
	require.NoError(t, os.WriteFile(catalogue, committed, 0o600))

	images := filepath.Join(dir, "images")
	require.NoError(t, os.MkdirAll(filepath.Join(images, derivedDir), 0o755))
	// One file standing in for a hand-drawn illustration, beside the directory
	// this program owns, and one stale file inside it. The first must survive
	// and the second must not.
	handDrawn := filepath.Join(images, "bicycle-vitesse-urbain-7.svg")
	require.NoError(t, os.WriteFile(handDrawn, []byte("<svg>drawn by a person</svg>"), 0o600))
	stale := filepath.Join(images, derivedDir, "wd-q0000000.svg")
	require.NoError(t, os.WriteFile(stale, []byte("<svg>a mark for an offer that has gone</svg>"), 0o600))

	require.NoError(t, run(catalogue, images), "the program will not run against a copy of its own output")

	kept, err := os.ReadFile(handDrawn)
	require.NoError(t, err, "the generator removed a picture beside derived/, which is where the "+
		"four hand-drawn illustrations live and where nothing it writes belongs")
	assert.Equal(t, "<svg>drawn by a person</svg>", string(kept),
		"the generator overwrote a hand-drawn illustration, and a drawing nobody can regenerate "+
			"is not one a program should be able to reach")

	_, err = os.Stat(stale)
	assert.Error(t, err, "a mark for an offer no longer derived survived the run, so the shipped "+
		"set would grow a file with nothing pointing at it every time a shelf changed")

	written, err := os.ReadFile(catalogue)
	require.NoError(t, err)
	assert.Equal(t, string(committed), string(written),
		"running the generator over the file it already wrote changed it, so `make catalogue` is "+
			"something a person has to think before running rather than something they can run "+
			"to find out")

	require.NoError(t, run(catalogue, images), "the program will not run a second time")
	again, err := os.ReadFile(catalogue)
	require.NoError(t, err)
	assert.Equal(t, string(written), string(again),
		"a second run disagreed with the first, so nothing downstream can assert what the shop sells")
}

// TestTheHeroesComeBackOutExactlyAsTheyWentIn is the property the whole rewrite
// is arranged around, and it is why the heroes are carried as raw JSON.
//
// "Every hero offer keeps its prices and scenario, and every existing beat
// reproduces unchanged" is issue #160's hardest requirement, and the way to fail
// it is not to change a number — it is to re-encode one. A struct round trip
// would sort the attribute keys, expand the price sequence and rewrite the
// escapes, and the diff that produced would be indistinguishable from a
// deliberate edit.
func TestTheHeroesComeBackOutExactlyAsTheyWentIn(t *testing.T) {
	t.Parallel()

	existing, err := readCatalogue(cataloguePath)
	require.NoError(t, err)

	before := make(map[string]string, len(Heroes))
	for _, raw := range existing.Offers {
		text := string(raw)
		for _, id := range Heroes {
			if strings.Contains(text, `"id": "`+id+`"`) {
				before[id] = text
			}
		}
	}
	require.Len(t, before, len(Heroes), "the file no longer lists every hero, so the property below would hold over whichever ones happened to still be there")

	derived, err := derive()
	require.NoError(t, err)
	rewritten, err := existing.rewrite(derived)
	require.NoError(t, err)

	after := make([]string, 0, len(Heroes))
	for _, raw := range rewritten.Offers[:len(Heroes)] {
		after = append(after, string(raw))
	}
	for _, id := range Heroes {
		assert.True(t, slices.Contains(after, before[id]),
			"%s did not come back out of the rewrite character for character, and its prices are "+
				"what several tests and every screenshot are written against", id)
	}
}

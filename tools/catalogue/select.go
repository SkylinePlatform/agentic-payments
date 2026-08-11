package main

import (
	"embed"
	"encoding/csv"
	"fmt"
	"strings"
)

// data is the frozen answer to the queries data/PROVENANCE.md records.
//
// Embedded rather than read from disk so that the program carries its source
// with it: there is no path to get wrong, no working directory to be in, and —
// the point of the whole exercise — nothing to fetch.
//
//go:embed data/*.csv
var data embed.FS

// Reserved is what this program refuses to produce, and it is the part of the
// file to read before adding a shelf.
//
// The agent takes the first candidate a search returns and asks nobody —
// `settle` in backend/internal/agent/authorise.go — which is defensible only
// while every scripted sentence narrows the catalogue to exactly one offer.
// Three of the five narrow on something a derived offer could collide with:
//
//   - **the category `ladders`**, which is the whole of what "find and buy
//     telescopic ladders, cheapest" sends to the merchant. A second ladder would
//     not fail to load and would not fail to search; it would make the
//     demonstration buy a different product, and only
//     TestEveryScriptedPromptFindsOneCandidate would say so.
//   - **the route BEG→PMI**, which is what "a flight to Palma" narrows on. Other
//     routes are welcome and the point — see the flights shelf — but this one
//     belongs to the hero.
//   - **the two identifiers** the bicycle and the concert sentences name
//     outright. Nothing derived here could reach them, since a derived
//     identifier is a Wikidata Q-number or a route, but a shelf added later
//     might, and the list is the place that would be read first.
//
// Widening the catalogue is what issue #160 is for; widening it *into* one of
// these three is how a wide catalogue would break the demonstration it exists to
// dress.
var Reserved = struct {
	Categories  []string
	Routes      [][2]string
	Identifiers []string
}{
	Categories:  []string{"ladders"},
	Routes:      [][2]string{{"BEG", "PMI"}},
	Identifiers: []string{"gtin:05012345678900", "event:vlado-georgijev-2026-11-14"},
}

// A shelf is one CSV turned into one catalogue category.
//
// take is a quota rather than "everything in the file" because the snapshot
// holds forty rows per class and the shop wants a shelf, not an inventory: sixty
// offers is enough that a scripted sentence *narrows* rather than exhausts,
// which is the property issue #160 asks for, and few enough that the file stays
// something a person can read.
//
// retailer is the one field on a derived offer that no dataset could supply. No
// register states who is behind the counter of a shop that does not exist, so
// each shelf names its counter, and the two that continue a hero's — Sever
// Cycles sells the bicycles, Adria Wings flies the routes — do so on purpose:
// the hero is then a row of the same shop rather than an exhibit beside it.
type shelf struct {
	category string
	file     string
	retailer string
	take     int
	band     band
}

// The shelves, in the order they are written into the file.
var shelves = []shelf{
	{category: "cameras", file: "cameras", retailer: "Sever Optics", take: 10,
		band: band{low: 24900, high: 189900}},
	{category: "camera-lenses", file: "camera-lenses", retailer: "Sever Optics", take: 10,
		band: band{low: 19900, high: 249900}},
	{category: "smartphones", file: "smartphones", retailer: "Sever Electronics", take: 10,
		band: band{low: 14900, high: 119900}},
	{category: "games-consoles", file: "games-consoles", retailer: "Sever Electronics", take: 10,
		band: band{low: 5900, high: 59900}},
	{category: "bicycles", file: "bicycles", retailer: "Sever Cycles", take: 8,
		band: band{low: 29900, high: 259900}},
}

// The flight shelf, which is not one of the above because a route is not a row.
const (
	// flightsOut is how many departures from the shop's own airport are derived,
	// and flightsBack how many of those are also sold in the other direction.
	//
	// Twelve routes rather than one is what turns TestEveryScriptedPromptFindsOneCandidate
	// from a claim about scarcity into a claim about narrowing: "a flight to
	// Palma" now picks one route out of thirteen by naming both its ends, which
	// is what it always said it did and could not previously demonstrate.
	flightsOut  = 8
	flightsBack = 4

	// flightRetailer is the hero flight's own, for the reason shelf.retailer
	// gives.
	flightRetailer = "Adria Wings"

	// homeAirport is the end every derived route has, because the shop has an
	// address: the hero flight departs from it, so a derived route sharing it
	// reads as the same counter selling the next departure rather than as a
	// separate airline appearing out of nowhere.
	homeAirport = "BEG"
)

var flightBand = band{low: 7900, high: 49900}

// derive builds every offer that is not a hero.
func derive() ([]entry, error) {
	var out []entry
	for _, s := range shelves {
		goods, err := s.derive()
		if err != nil {
			return nil, err
		}
		out = append(out, goods...)
	}

	flights, err := deriveFlights()
	if err != nil {
		return nil, err
	}
	out = append(out, flights...)

	seen := make(map[string]struct{}, len(out))
	for _, o := range out {
		if _, duplicate := seen[o.ID]; duplicate {
			return nil, fmt.Errorf("derived %s twice, and two offers under one identifier make "+
				"item.id ambiguous to a mandate", o.ID)
		}
		seen[o.ID] = struct{}{}
	}
	return out, nil
}

// derive turns one shelf's rows into offers.
func (s shelf) derive() ([]entry, error) {
	for _, reserved := range Reserved.Categories {
		if s.category == reserved {
			return nil, fmt.Errorf("the shelf %q is reserved; see Reserved for what a second one "+
				"would do to the demonstration", s.category)
		}
	}

	rows, err := read(s.file)
	if err != nil {
		return nil, err
	}

	out := make([]entry, 0, s.take)
	titles := make(map[string]struct{}, s.take)
	for _, row := range rows {
		if len(out) == s.take {
			break
		}

		id, ok := qid(row["item"])
		if !ok {
			continue
		}
		title := strings.TrimSpace(row["label"])
		// A Q-number is what the label service answers with when an item has no
		// English label, and a row whose title is its own identifier is not a
		// thing anybody would recognise on a shelf.
		if title == "" || title == id || len(title) > 48 {
			continue
		}
		if _, repeated := titles[title]; repeated {
			continue
		}
		maker := strings.TrimSpace(row["makerLabel"])
		if maker == "" {
			continue
		}

		titles[title] = struct{}{}
		offer := entry{
			ID:          "wd:" + id,
			Category:    s.category,
			Attributes:  map[string]string{"brand": maker},
			Title:       title,
			Description: describe(row["description"], maker, year(row["inception"])),
			Retailer:    s.retailer,
		}
		if y := year(row["inception"]); y != "" {
			offer.Attributes["released"] = y
		}
		offer.ImageURL = markURL(offer.ID)
		offer.Prices, offer.Scenario = price(offer.ID, s.band)
		out = append(out, offer)
	}

	if len(out) < s.take {
		return nil, fmt.Errorf("the shelf %q wanted %d offers and the snapshot yielded %d",
			s.category, s.take, len(out))
	}
	return out, nil
}

// deriveFlights builds the routes the shop sells besides the hero's.
//
// Departures are picked at even spacing through the airport list rather than off
// the front of it, because the list is ordered by IATA code and the first eight
// entries would be eight airports whose names all begin with A.
func deriveFlights() ([]entry, error) {
	rows, err := read("airports")
	if err != nil {
		return nil, err
	}

	home := airport{}
	pool := make([]airport, 0, len(rows))
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		a := airport{
			code:    strings.TrimSpace(row["iata"]),
			name:    strings.TrimSpace(row["label"]),
			country: strings.TrimSpace(row["countryLabel"]),
		}
		if a.code == "" || a.name == "" || a.country == "" {
			continue
		}
		// The snapshot lists a code more than once where two airports claim it.
		// The first wins, which is stable because the query orders by code and
		// then by nothing else this program can see — so a refetch that gained a
		// second claimant does not change which one is already in the file.
		if _, duplicate := seen[a.code]; duplicate {
			continue
		}
		seen[a.code] = struct{}{}

		if a.code == homeAirport {
			home = a
			continue
		}
		if reservedEnd(a.code) {
			continue
		}
		pool = append(pool, a)
	}

	if home.code == "" {
		return nil, fmt.Errorf("the snapshot lists no airport under %s, which is the end every "+
			"derived route has", homeAirport)
	}
	if len(pool) < flightsOut {
		return nil, fmt.Errorf("the snapshot yielded %d usable airports and %d routes were wanted",
			len(pool), flightsOut)
	}

	out := make([]entry, 0, flightsOut+flightsBack)
	for i := range flightsOut {
		far := pool[i*len(pool)/flightsOut]
		out = append(out, flight(home, far))
		if i < flightsBack {
			out = append(out, flight(far, home))
		}
	}
	return out, nil
}

// reservedEnd reports whether an airport code is one end of a reserved route.
//
// Both ends, rather than the pair: the reserved route is BEG→PMI and every
// derived route has BEG at one end, so admitting PMI to the pool would produce
// exactly the collision Reserved exists to prevent — and PMI→BEG, which is a
// different route and would be legal, is not worth the reader having to work
// that out.
func reservedEnd(code string) bool {
	for _, r := range Reserved.Routes {
		if r[0] == code || r[1] == code {
			return true
		}
	}
	return false
}

// airport is one row of the airport snapshot.
type airport struct {
	code    string
	name    string
	country string
}

// city is the airport's name with the words that are true of every airport
// taken out of it, so that a title reads as a pair of places rather than as a
// pair of institutions.
//
// Taken out wherever they appear rather than off the end, because the register
// does not always put them last: "Pisa International Airport 'Galileo Galilei'"
// carries its dedication after the words that would otherwise be a suffix.
func (a airport) city() string {
	name := a.name
	for _, words := range []string{" International Airport", " Airport", " Airfield"} {
		if strings.Contains(name, words) {
			name = strings.Replace(name, words, "", 1)
			break
		}
	}
	return strings.TrimSpace(name)
}

// flight is one route, sold in one direction.
func flight(from, to airport) entry {
	offer := entry{
		ID:       fmt.Sprintf("route:%s-%s", from.code, to.code),
		Category: "flights",
		Attributes: map[string]string{
			"route.origin":      from.code,
			"route.destination": to.code,
		},
		Title: fmt.Sprintf("%s → %s", from.city(), to.city()),
		Description: fmt.Sprintf("Scheduled service. %s, %s to %s, %s.",
			from.name, from.country, to.name, to.country),
		Retailer: flightRetailer,
	}
	offer.ImageURL = markURL(offer.ID)
	offer.Prices, offer.Scenario = price(offer.ID, flightBand)
	return offer
}

// describe composes the sentence under the title.
//
// The dataset's own description is short and formulaic — "digital camera model",
// "prime lens by Canon Inc" — which is enough to say what the thing is and not
// enough to fill the cell beside it. The maker and the year are the two other
// facts every row carries, so the sentence is those three and nothing invented.
func describe(description, maker, year string) string {
	sentence := strings.TrimSpace(description)
	if sentence != "" {
		sentence = strings.ToUpper(sentence[:1]) + sentence[1:]
		if !strings.HasSuffix(sentence, ".") {
			sentence += "."
		}
		sentence += " "
	}
	sentence += maker
	if year != "" {
		sentence += ", " + year
	}
	return sentence + "."
}

// qid reads the Q-number out of a Wikidata entity URI.
func qid(uri string) (string, bool) {
	const prefix = "http://www.wikidata.org/entity/"
	id, found := strings.CutPrefix(strings.TrimSpace(uri), prefix)
	if !found || id == "" {
		return "", false
	}
	return id, true
}

// year reads the year out of an xsd:dateTime, which is the only part of an
// inception date a shop has any use for.
func year(inception string) string {
	inception = strings.TrimSpace(inception)
	if len(inception) < 4 {
		return ""
	}
	return inception[:4]
}

// read parses one embedded CSV into rows keyed by column name.
func read(name string) ([]map[string]string, error) {
	f, err := data.Open("data/" + name + ".csv")
	if err != nil {
		return nil, fmt.Errorf("open snapshot %s: %w", name, err)
	}
	defer func() { _ = f.Close() }()

	records, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse snapshot %s: %w", name, err)
	}
	if len(records) < 2 {
		return nil, fmt.Errorf("snapshot %s holds no rows", name)
	}

	header := records[0]
	out := make([]map[string]string, 0, len(records)-1)
	for _, record := range records[1:] {
		row := make(map[string]string, len(header))
		for i, column := range header {
			if i < len(record) {
				row[column] = record[i]
			}
		}
		out = append(out, row)
	}
	return out, nil
}

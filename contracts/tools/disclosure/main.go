// Command disclosure extracts the selective-disclosure annotations from the
// JSON Schema contracts and emits them for both languages.
//
// SD-JWT issuance (#4) and disclosure minimisation (#14) both need to know
// which fields of a mandate may be withheld from a verifier that does not need
// them. That list has to come from the schemas: a hand-maintained copy drifts
// from the model the moment someone adds a field, and the failure mode of that
// drift is silent — the naive implementation simply discloses everything, which
// works, passes tests, and defeats the point of using SD-JWT at all.
//
// Two annotations are understood, both on a property schema:
//
//	x-disclosable:       true   the property itself may be withheld
//	x-disclosable-items: true   each element of this array may be withheld
//	                            independently, which is what disclosure
//	                            minimisation over a constraint list needs
//
// Paths are dotted property names, with "[]" marking array elements:
// "checkout", "constraints[]". Marks inside a $ref'd schema belong to that
// schema's own entry; refs are not followed, because a referenced type is a
// type in its own right and its callers compose the two lists.
//
// Emitting both languages from one walk is deliberate. Two extractors, one per
// language, would be the same cross-language duplication that contracts/ exists
// to remove.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

const (
	annotationField = "x-disclosable"
	annotationItems = "x-disclosable-items"
)

// knownAnnotations is the closed set of "x-" keywords a contract may carry.
//
// Closed on purpose. JSON Schema ignores unrecognised keywords by design, so a
// misspelled x-disclosible, or an x-disclosable carrying the string "true"
// instead of the boolean, would be walked past without a word — and the field
// it was meant to mark would then be disclosed to every verifier, forever.
// That is the same silent failure this tool exists to prevent, one level up:
// nothing breaks, the tests pass, and the privacy property is quietly gone.
// An annotation this tool does not recognise is therefore an error.
var knownAnnotations = map[string]bool{
	annotationField: true,
	annotationItems: true,
}

// typeDisclosure is one canonical type and the paths within it that may be
// withheld.
type typeDisclosure struct {
	Type  string
	Paths []string
}

func main() {
	goOut := flag.String("go-out", "", "path of the Go file to write")
	tsOut := flag.String("ts-out", "", "path of the TypeScript file to write")
	pkg := flag.String("package", "generated", "package clause for the generated Go file")
	flag.Parse()

	if *goOut == "" || *tsOut == "" {
		fatal(fmt.Errorf("both -go-out and -ts-out are required"))
	}
	if flag.NArg() == 0 {
		fatal(fmt.Errorf("no schema files given"))
	}

	disclosures, err := extractAll(flag.Args())
	if err != nil {
		fatal(err)
	}

	if err := writeGo(*goOut, *pkg, disclosures); err != nil {
		fatal(err)
	}
	if err := writeTS(*tsOut, disclosures); err != nil {
		fatal(err)
	}

	marks := 0
	for _, d := range disclosures {
		marks += len(d.Paths)
	}
	fmt.Printf("generate-disclosure: %d annotated paths across %d types\n", marks, len(disclosures))
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "disclosure: %v\n", err)
	os.Exit(1)
}

// extractAll reads every schema and returns the types that declare at least one
// withholdable field, ordered by type name. A type with no annotations is
// absent rather than present-and-empty: "nothing may be withheld" and "this
// type is unknown" are the same answer to a caller, and both mean disclose
// everything.
func extractAll(paths []string) ([]typeDisclosure, error) {
	var out []typeDisclosure

	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}

		var schema map[string]any
		if err := json.Unmarshal(raw, &schema); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}

		title, ok := schema["title"].(string)
		if !ok || title == "" {
			return nil, fmt.Errorf("%s: schema has no title; the generators derive type names from it", path)
		}

		if err := validate(schema, path); err != nil {
			return nil, err
		}

		var found []string
		collect(schema, "", &found)
		if len(found) == 0 {
			continue
		}
		sort.Strings(found)
		// x-disclosable on an items subschema and x-disclosable-items on its
		// parent express the same thing and would otherwise both be recorded.
		found = slices.Compact(found)
		out = append(out, typeDisclosure{Type: title, Paths: found})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out, nil
}

// validate rejects annotation mistakes that would otherwise pass in silence.
//
// The walk is exhaustive rather than mirroring collect: an annotation sitting
// somewhere collect never visits is just as ineffective as a misspelled one,
// and just as quiet about it. Every "x-" keyword anywhere in the document must
// be one this tool acts on, and must carry a boolean.
func validate(node any, file string) error {
	switch n := node.(type) {
	case map[string]any:
		for key, value := range n {
			if strings.HasPrefix(key, "x-") {
				if !knownAnnotations[key] {
					return fmt.Errorf("%s: unknown annotation %q; this tool acts on %s and %s only, and would ignore that key without complaining",
						file, key, annotationField, annotationItems)
				}
				if _, ok := value.(bool); !ok {
					return fmt.Errorf("%s: annotation %q must be a JSON boolean, got %T; a non-boolean is ignored, which silently discloses the field",
						file, key, value)
				}
			}
			if err := validate(value, file); err != nil {
				return err
			}
		}
	case []any:
		for _, item := range n {
			if err := validate(item, file); err != nil {
				return err
			}
		}
	}
	return nil
}

// collect walks a schema node, recording the path of every annotated property.
func collect(node any, path string, found *[]string) {
	schema, ok := node.(map[string]any)
	if !ok {
		return
	}

	if path != "" {
		if flag, ok := schema[annotationField].(bool); ok && flag {
			*found = append(*found, path)
		}
		if flag, ok := schema[annotationItems].(bool); ok && flag {
			*found = append(*found, path+"[]")
		}
	}

	if properties, ok := schema["properties"].(map[string]any); ok {
		for name, sub := range properties {
			child := name
			if path != "" {
				child = path + "." + name
			}
			collect(sub, child, found)
		}
	}

	if items, ok := schema["items"].(map[string]any); ok {
		collect(items, path+"[]", found)
	}
}

func writeGo(path, pkg string, disclosures []typeDisclosure) error {
	var buf bytes.Buffer

	fmt.Fprintf(&buf, "// Code generated from contracts/ by contracts/tools/disclosure. DO NOT EDIT.\n\n")
	fmt.Fprintf(&buf, "package %s\n\n", pkg)
	buf.WriteString("import \"slices\"\n\n")
	buf.WriteString(`// disclosableFields maps a canonical type name to the field paths within it
// that may be withheld from a verifier that does not need them. Paths are
// dotted property names; a "[]" suffix means the elements of that array are
// withholdable individually.
//
// The list is a privacy statement about the domain, not an SD-JWT instruction.
// How a securing format realises it is the adapter's problem, and a protocol
// with no selective disclosure ignores it entirely.
var disclosableFields = map[string][]string{
`)
	for _, d := range disclosures {
		fmt.Fprintf(&buf, "\t%q: {", d.Type)
		for i, p := range d.Paths {
			if i > 0 {
				buf.WriteString(", ")
			}
			fmt.Fprintf(&buf, "%q", p)
		}
		buf.WriteString("},\n")
	}
	buf.WriteString(`}

// Disclosable returns the withholdable field paths of the named canonical type.
// An unknown type, or one that declares nothing withholdable, yields no paths —
// which means every field of it must be disclosed.
func Disclosable(typeName string) []string {
	paths := disclosableFields[typeName]
	out := make([]string, len(paths))
	copy(out, paths)
	return out
}

// DisclosableTypes returns, in a stable order, the canonical type names that
// declare at least one withholdable field.
func DisclosableTypes() []string {
	out := make([]string, 0, len(disclosableFields))
	for name := range disclosableFields {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}
`)

	src, err := format.Source(buf.Bytes())
	if err != nil {
		return fmt.Errorf("formatting %s: %w", path, err)
	}

	return write(path, src)
}

func writeTS(path string, disclosures []typeDisclosure) error {
	var buf bytes.Buffer

	buf.WriteString(`/* eslint-disable */
/**
 * Code generated from contracts/ by contracts/tools/disclosure.
 * DO NOT EDIT — change the JSON Schema and run ` + "`make generate`" + ` from the repository root.
 */

/**
 * Field paths of each canonical type that may be withheld from a verifier that
 * does not need them. Paths are dotted property names; a "[]" suffix means the
 * elements of that array are withholdable individually.
 *
 * A type absent from this map has nothing withholdable: every field of it is
 * always disclosed.
 */
export const DISCLOSABLE: Readonly<Record<string, readonly string[]>> = {
`)
	for _, d := range disclosures {
		quoted := make([]string, len(d.Paths))
		for i, p := range d.Paths {
			quoted[i] = fmt.Sprintf("%q", p)
		}
		fmt.Fprintf(&buf, "  %s: [%s],\n", d.Type, strings.Join(quoted, ", "))
	}
	buf.WriteString("};\n")

	return write(path, buf.Bytes())
}

func write(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating directory for %s: %w", path, err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

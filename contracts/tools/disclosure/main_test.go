package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCollect(t *testing.T) {
	tests := []struct {
		name   string
		schema string
		want   []string
	}{
		{
			name: "unannotated schema discloses everything",
			schema: `{
				"type": "object",
				"properties": {"checkout_hash": {"type": "string"}}
			}`,
			want: nil,
		},
		{
			name: "property marked withholdable",
			schema: `{
				"type": "object",
				"properties": {
					"checkout_hash": {"type": "string"},
					"checkout": {"type": "string", "x-disclosable": true}
				}
			}`,
			want: []string{"checkout"},
		},
		{
			name: "array elements withholdable one by one",
			schema: `{
				"type": "object",
				"properties": {
					"constraints": {
						"type": "array",
						"x-disclosable-items": true,
						"items": {"$ref": "constraint.json"}
					}
				}
			}`,
			want: []string{"constraints[]"},
		},
		{
			name: "whole array withheld is not the same as its elements",
			schema: `{
				"type": "object",
				"properties": {
					"constraints": {
						"type": "array",
						"x-disclosable": true,
						"items": {"$ref": "constraint.json"}
					}
				}
			}`,
			want: []string{"constraints"},
		},
		{
			name: "nested object property",
			schema: `{
				"type": "object",
				"properties": {
					"payee": {
						"type": "object",
						"properties": {"website": {"type": "string", "x-disclosable": true}}
					}
				}
			}`,
			want: []string{"payee.website"},
		},
		{
			name: "property of an inline array element",
			schema: `{
				"type": "object",
				"properties": {
					"keys": {
						"type": "array",
						"items": {
							"type": "object",
							"properties": {"kid": {"type": "string", "x-disclosable": true}}
						}
					}
				}
			}`,
			want: []string{"keys[].kid"},
		},
		{
			name: "an annotation on the root is not a field and is ignored",
			schema: `{
				"type": "object",
				"x-disclosable": true,
				"properties": {"id": {"type": "string"}}
			}`,
			want: nil,
		},
		{
			name: "false is not an annotation",
			schema: `{
				"type": "object",
				"properties": {"checkout": {"type": "string", "x-disclosable": false}}
			}`,
			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var schema map[string]any
			if err := json.Unmarshal([]byte(tc.schema), &schema); err != nil {
				t.Fatalf("test schema is not valid JSON: %v", err)
			}

			var got []string
			collect(schema, "", &got)
			slices.Sort(got)

			if !slices.Equal(got, tc.want) {
				t.Errorf("collect() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExtractAllRequiresTitle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "untitled.json")
	if err := os.WriteFile(path, []byte(`{"type": "object"}`), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	if _, err := extractAll([]string{path}); err == nil {
		t.Fatal("extractAll() accepted a schema with no title; the generators derive type names from it")
	}
}

// The whole point of extracting the annotations is that Go and TypeScript get
// the same answer. Assert they are produced from one walk rather than trusting
// that two emitters stayed in step.
func TestGoAndTypeScriptAgree(t *testing.T) {
	dir := t.TempDir()
	schema := filepath.Join(dir, "mandate.json")
	if err := os.WriteFile(schema, []byte(`{
		"title": "ExampleMandate",
		"type": "object",
		"properties": {
			"checkout": {"type": "string", "x-disclosable": true},
			"constraints": {"type": "array", "x-disclosable-items": true, "items": {"type": "object"}}
		}
	}`), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	disclosures, err := extractAll([]string{schema})
	if err != nil {
		t.Fatalf("extractAll(): %v", err)
	}

	goFile := filepath.Join(dir, "disclosure.go")
	tsFile := filepath.Join(dir, "disclosure.ts")
	if err := writeGo(goFile, "generated", disclosures); err != nil {
		t.Fatalf("writeGo(): %v", err)
	}
	if err := writeTS(tsFile, disclosures); err != nil {
		t.Fatalf("writeTS(): %v", err)
	}

	for _, f := range []string{goFile, tsFile} {
		content, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		for _, want := range []string{"ExampleMandate", `"checkout"`, `"constraints[]"`} {
			if !strings.Contains(string(content), want) {
				t.Errorf("%s does not mention %s", filepath.Base(f), want)
			}
		}
	}
}

func TestValidateRejectsAnnotationsThatWouldPassInSilence(t *testing.T) {
	tests := []struct {
		name   string
		schema string
		// wantErr is a substring of the expected error. Empty means the schema
		// must be accepted.
		wantErr string
	}{
		{
			name:   "correct annotation is accepted",
			schema: `{"properties": {"checkout": {"x-disclosable": true}}}`,
		},
		{
			name:   "false is a legitimate value, not a mistake",
			schema: `{"properties": {"checkout": {"x-disclosable": false}}}`,
		},
		{
			name:    "misspelled annotation is rejected rather than walked past",
			schema:  `{"properties": {"checkout": {"x-disclosible": true}}}`,
			wantErr: "unknown annotation",
		},
		{
			name:    "string instead of boolean is rejected",
			schema:  `{"properties": {"checkout": {"x-disclosable": "true"}}}`,
			wantErr: "must be a JSON boolean",
		},
		{
			name:    "mistake buried where collect never walks is still caught",
			schema:  `{"$defs": {"inner": {"properties": {"x": {"x-disclosable-item": true}}}}}`,
			wantErr: "unknown annotation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var node any
			if err := json.Unmarshal([]byte(tt.schema), &node); err != nil {
				t.Fatalf("test schema is not valid JSON: %v", err)
			}

			err := validate(node, "example.json")
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("validate() = %v, want no error", err)
			case tt.wantErr != "" && err == nil:
				t.Fatalf("validate() = nil, want an error containing %q", tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Fatalf("validate() = %v, want an error containing %q", err, tt.wantErr)
			}
			if tt.wantErr != "" && err != nil && !strings.Contains(err.Error(), "example.json") {
				t.Errorf("error does not name the offending file: %v", err)
			}
		})
	}
}

package tools

import (
	"strings"
	"testing"
)

func TestSpecsToSchemaShape(t *testing.T) {
	specs := []*ArgSpec{
		{Name: "path", Kind: ArgString, Required: true, Description: "Path"},
		{Name: "limit", Kind: ArgInt, Default: 5, Min: intPtr(1), Max: intPtr(100)},
		{Name: "recursive", Kind: ArgBool, Default: false},
		{Name: "modes", Kind: ArgStringSlice, Items: &ArgSpec{Kind: ArgString}},
		{
			Name:           "nested",
			Kind:           ArgObject,
			ObjectRequired: []string{"key"},
			Properties:     []*ArgSpec{{Name: "key", Kind: ArgString, Required: true}},
		},
	}
	schema := SpecsToSchema(specs)

	if schema.Type != "object" {
		t.Fatalf("schema type = %q, want object", schema.Type)
	}
	if schema.AdditionalProperties {
		t.Fatal("expected AdditionalProperties false by default")
	}
	if len(schema.Required) != 1 || schema.Required[0] != "path" {
		t.Fatalf("required = %v, want [path]", schema.Required)
	}
	if got := schema.Properties["path"].Type; got != "string" {
		t.Fatalf("path type = %q", got)
	}
	lim := schema.Properties["limit"]
	if lim.Type != "integer" || lim.Default != 5 || lim.Minimum == nil || *lim.Minimum != 1 {
		t.Fatalf("limit schema bad: %+v", lim)
	}
	if schema.Properties["modes"].Type != "array" || schema.Properties["modes"].Items == nil {
		t.Fatalf("modes schema bad: %+v", schema.Properties["modes"])
	}
	nested := schema.Properties["nested"]
	if nested.Type != "object" || nested.Properties["key"].Type != "string" || len(nested.Required) != 1 {
		t.Fatalf("nested schema bad: %+v", nested)
	}
}

func TestParseArgsHappyAndAliases(t *testing.T) {
	specs := []*ArgSpec{
		{Name: "path", Kind: ArgString, Required: true},
		{Name: "file_size", Kind: ArgInt, Aliases: []string{"bytes"}, Default: 10, Min: intPtr(0)},
		{Name: "force", Kind: ArgBool, Default: false},
		{Name: "tags", Kind: ArgStringSlice, Items: &ArgSpec{Kind: ArgString}},
	}

	// canonical keys
	got, err := ParseArgs(specs, map[string]any{"path": "a.go", "file_size": 3, "force": true, "tags": []any{"x", "y"}})
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if got["file_size"].(int) != 3 {
		t.Fatalf("file_size = %v", got["file_size"])
	}
	if !got["force"].(bool) {
		t.Fatal("force not parsed")
	}
	if len(got["tags"].([]string)) != 2 {
		t.Fatalf("tags = %v", got["tags"])
	}

	// alias resolution
	got2, err := ParseArgs(specs, map[string]any{"path": "a.go", "bytes": 12})
	if err != nil {
		t.Fatalf("ParseArgs alias: %v", err)
	}
	if got2["file_size"].(int) != 12 {
		t.Fatalf("alias bytes not resolved: %v", got2["file_size"])
	}
	// default applied when absent
	if got2["force"].(bool) {
		t.Fatal("default force=false not applied")
	}
}

func TestParseArgsMissingRequiredAndUnknown(t *testing.T) {
	specs := []*ArgSpec{
		{Name: "path", Kind: ArgString, Required: true},
		{Name: "limit", Kind: ArgInt, Min: intPtr(1)},
	}
	if _, err := ParseArgs(specs, map[string]any{}); err == nil || !strings.Contains(err.Error(), "path is required") {
		t.Fatalf("expected missing required error, got %v", err)
	}
	if _, err := ParseArgs(specs, map[string]any{"path": "x", "limit": 0}); err == nil || !strings.Contains(err.Error(), "at least 1") {
		t.Fatalf("expected min bound error, got %v", err)
	}
	if _, err := ParseArgs(specs, map[string]any{"path": "x", "bogus": 1}); err == nil || !strings.Contains(err.Error(), "unknown argument") {
		t.Fatalf("expected unknown argument error, got %v", err)
	}
}

func TestParseArgsEnum(t *testing.T) {
	specs := []*ArgSpec{
		{Name: "mode", Kind: ArgString, Enum: []string{"a", "b"}},
	}
	if _, err := ParseArgs(specs, map[string]any{"mode": "c"}); err == nil || !strings.Contains(err.Error(), "must be one of") {
		t.Fatalf("expected enum error, got %v", err)
	}
	if _, err := ParseArgs(specs, map[string]any{"mode": "a"}); err != nil {
		t.Fatalf("valid enum rejected: %v", err)
	}
}

func TestParseArgsNestedObject(t *testing.T) {
	specs := []*ArgSpec{
		{Name: "perm", Kind: ArgObject, Properties: []*ArgSpec{
			{Name: "access", Kind: ArgString, Required: true, Enum: []string{"r", "w"}},
		}},
	}
	got, err := ParseArgs(specs, map[string]any{"perm": map[string]any{"access": "w"}})
	if err != nil {
		t.Fatalf("nested parse: %v", err)
	}
	nested := got["perm"].(map[string]any)
	if nested["access"].(string) != "w" {
		t.Fatalf("nested access = %v", nested["access"])
	}
	if _, err := ParseArgs(specs, map[string]any{"perm": map[string]any{"access": "x"}}); err == nil {
		t.Fatal("expected nested enum error")
	}
}

func TestParseArgsObjectSlice(t *testing.T) {
	specs := []*ArgSpec{
		{
			Name:     "edits",
			Kind:     ArgObjectSlice,
			Required: true,
			Items: &ArgSpec{
				Kind: ArgObject,
				Properties: []*ArgSpec{
					{Name: "old_string", Kind: ArgString, Required: true},
					{Name: "replace_all", Kind: ArgBool, Default: false},
				},
			},
		},
	}
	got, err := ParseArgs(specs, map[string]any{"edits": []any{
		map[string]any{"old_string": "a", "replace_all": true},
		map[string]any{"old_string": "b"},
	}})
	if err != nil {
		t.Fatalf("object slice parse: %v", err)
	}
	list := got["edits"].([]map[string]any)
	if len(list) != 2 {
		t.Fatalf("len = %d, want 2", len(list))
	}
	if list[0]["old_string"].(string) != "a" || list[0]["replace_all"].(bool) != true {
		t.Fatalf("item0 = %+v", list[0])
	}
	if list[1]["replace_all"].(bool) != false {
		t.Fatalf("item1 replace_all default = %v, want false", list[1]["replace_all"])
	}

	// Non-object item must fail.
	if _, err := ParseArgs(specs, map[string]any{"edits": []any{"not-an-object"}}); err == nil {
		t.Fatal("expected error for non-object slice item")
	}
	// Missing required field inside an item must fail.
	if _, err := ParseArgs(specs, map[string]any{"edits": []any{map[string]any{}}}); err == nil {
		t.Fatal("expected error for missing required item field")
	}
}

package tools

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ArgKind classifies a single tool parameter. A single AgSpec drives both the
// advertised JSON schema and the runtime argument parser, removing the drift that
// the legacy hand-rolled helpers (stringArg/intArg/boolArg) suffered from.
type ArgKind int

const (
	ArgString ArgKind = iota
	ArgInt
	ArgBool
	ArgStringSlice
	ArgObject
	ArgObjectSlice
)

// ArgSpec is the declarative definition of one tool parameter. It is the single
// source of truth for the parameter's schema and how its value is parsed.
type ArgSpec struct {
	// Name is the canonical parameter key advertised to providers.
	Name string
	// Kind selects the value type.
	Kind ArgKind
	// Required marks the parameter as mandatory.
	Required bool
	// Aliases are alternate keys accepted at runtime but not advertised.
	Aliases []string
	// Description feeds the schema description.
	Description string
	// Default is returned when the key is absent (and advertised in the schema).
	Default any
	// Enum restricts string values to the given set.
	Enum []string
	// Min / Max bound integer values.
	Min, Max *int
	// MinLength bounds string length (schema only). May also be enforced when
	// non-nil by minimum arg validation callers where relevant.
	MinLength *int
	// MinItems bounds slice minimum.
	MinItems *int
	// Items describes the element type for slice parameters.
	Items *ArgSpec
	// Properties / ObjectRequired describe a nested object parameter.
	Properties     []*ArgSpec
	ObjectRequired []string
}

// propertySchema renders this spec as a schema.PropertySchema.
func (spec *ArgSpec) propertySchema() PropertySchema {
	out := PropertySchema{
		Description: spec.Description,
		Enum:        append([]string(nil), spec.Enum...),
		Default:     spec.Default,
		Minimum:     spec.Min,
		Maximum:     spec.Max,
		MinLength:   spec.MinLength,
		MinItems:    spec.MinItems,
	}
	switch spec.Kind {
	case ArgString:
		out.Type = "string"
	case ArgInt:
		out.Type = "integer"
	case ArgBool:
		out.Type = "boolean"
	case ArgStringSlice:
		out.Type = "array"
		if spec.Items != nil {
			child := spec.Items.propertySchema()
			out.Items = &child
		}
	case ArgObject:
		out.Type = "object"
		out.Properties = make(map[string]PropertySchema, len(spec.Properties))
		out.Required = append([]string(nil), spec.ObjectRequired...)
		for _, child := range spec.Properties {
			out.Properties[child.Name] = child.propertySchema()
		}
	case ArgObjectSlice:
		out.Type = "array"
		if spec.Items != nil {
			child := spec.Items.propertySchema()
			out.Items = &child
		}
	}
	return out
}

// SpecsToSchema converts a list of specs into the full object schema. The result
// is identical in shape to the schemas constructed by hand elsewhere, so providers
// and the MCP bridge consume it without modification.
func SpecsToSchema(specs []*ArgSpec) Schema {
	schema := Schema{
		Type:                 "object",
		Properties:           make(map[string]PropertySchema, len(specs)),
		AdditionalProperties: false,
	}
	for _, spec := range specs {
		schema.Properties[spec.Name] = spec.propertySchema()
		if spec.Required {
			schema.Required = append(schema.Required, spec.Name)
		}
	}
	if len(schema.Required) == 0 {
		schema.Required = nil
	}
	return schema
}

// lookupKey returns the canonical key present in args (name or first matching
// alias), and whether any was present.
func (spec *ArgSpec) lookupKey(args map[string]any) (string, bool) {
	if _, ok := args[spec.Name]; ok {
		return spec.Name, true
	}
	for _, alias := range spec.Aliases {
		if _, ok := args[alias]; ok {
			return alias, true
		}
	}
	return "", false
}

// ParseArgs validates and coerces caller args against the spec list, keyed by
// canonical Name. Unknown keys are rejected, mirroring AdditionalProperties:false.
func ParseArgs(specs []*ArgSpec, args map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(specs))
	for _, spec := range specs {
		key, present := spec.lookupKey(args)
		if !present {
			if spec.Required {
				return nil, fmt.Errorf("%s is required", spec.Name)
			}
			if spec.Default != nil {
				out[spec.Name] = spec.Default
			}
			continue
		}
		coerced, err := parseSpecValue(spec, args[key])
		if err != nil {
			return nil, fmt.Errorf("%s %w", spec.Name, err)
		}
		out[spec.Name] = coerced
	}
	for inputKey := range args {
		if !specKnown(specs, inputKey) {
			return nil, fmt.Errorf("unknown argument %q", inputKey)
		}
	}
	return out, nil
}

func specKnown(specs []*ArgSpec, key string) bool {
	for _, spec := range specs {
		if key == spec.Name {
			return true
		}
		for _, alias := range spec.Aliases {
			if key == alias {
				return true
			}
		}
	}
	return false
}

// parseSpecValue converts a raw value and applies enum/bounds constraints.
func parseSpecValue(spec *ArgSpec, raw any) (any, error) {
	switch spec.Kind {
	case ArgString:
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("must be a string")
		}
		if spec.Enum != nil && !stringIn(s, spec.Enum) {
			return nil, fmt.Errorf("must be one of %v", spec.Enum)
		}
		return s, nil
	case ArgInt:
		n, err := coerceIntArg(raw)
		if err != nil {
			return nil, err
		}
		if spec.Min != nil && n < *spec.Min {
			return nil, fmt.Errorf("must be at least %d", *spec.Min)
		}
		if spec.Max != nil && n > *spec.Max {
			return nil, fmt.Errorf("must be at most %d", *spec.Max)
		}
		return n, nil
	case ArgBool:
		b, err := coerceBoolArg(raw)
		if err != nil {
			return nil, err
		}
		return b, nil
	case ArgStringSlice:
		items, err := coerceStringSliceArg(raw)
		if err != nil {
			return nil, err
		}
		if spec.MinItems != nil && len(items) < *spec.MinItems {
			return nil, fmt.Errorf("must contain at least %d items", *spec.MinItems)
		}
		return items, nil
	case ArgObject:
		object, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("must be an object")
		}
		nested, err := ParseArgs(spec.Properties, object)
		if err != nil {
			return nil, err
		}
		return nested, nil
	case ArgObjectSlice:
		list, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("must be an array")
		}
		if spec.MinItems != nil && len(list) < *spec.MinItems {
			return nil, fmt.Errorf("must contain at least %d items", *spec.MinItems)
		}
		out := make([]map[string]any, 0, len(list))
		for index, item := range list {
			object, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("item %d must be an object", index)
			}
			if spec.Items == nil {
				return nil, fmt.Errorf("item %d: missing item schema", index)
			}
			nested, err := ParseArgs(spec.Items.Properties, object)
			if err != nil {
				return nil, fmt.Errorf("item %d: %w", index, err)
			}
			out = append(out, nested)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported parameter kind")
	}
}

func stringIn(value string, set []string) bool {
	for _, candidate := range set {
		if value == candidate {
			return true
		}
	}
	return false
}

// coerceIntArg mirrors the existing intArg semantics (see args.go).
func coerceIntArg(value any) (int, error) {
	var number int
	switch typed := value.(type) {
	case int:
		number = typed
	case int32:
		number = int(typed)
	case int64:
		number = int(typed)
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || math.Trunc(typed) != typed ||
			typed >= float64(math.MaxInt) || typed < float64(math.MinInt) {
			return 0, fmt.Errorf("must be an integer")
		}
		number = int(typed)
	case string:
		trimmed := strings.TrimSpace(typed)
		if parsed, err := strconv.Atoi(trimmed); err == nil {
			number = parsed
		} else if f, ferr := strconv.ParseFloat(trimmed, 64); ferr == nil {
			if math.IsNaN(f) || math.IsInf(f, 0) || math.Trunc(f) != f ||
				f >= float64(math.MaxInt) || f < float64(math.MinInt) {
				return 0, fmt.Errorf("must be an integer")
			}
			number = int(f)
		} else {
			return 0, fmt.Errorf("must be an integer")
		}
	default:
		return 0, fmt.Errorf("must be an integer")
	}
	return number, nil
}

// coerceBoolArg mirrors the existing boolArg semantics (see args.go).
func coerceBoolArg(value any) (bool, error) {
	switch typed := value.(type) {
	case bool:
		return typed, nil
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "yes", "on", "1":
			return true, nil
		case "false", "no", "off", "0":
			return false, nil
		}
	case float64:
		if typed == 1 {
			return true, nil
		}
		if typed == 0 {
			return false, nil
		}
	case int:
		if typed == 1 {
			return true, nil
		}
		if typed == 0 {
			return false, nil
		}
	}
	return false, fmt.Errorf("must be a boolean")
}

func coerceStringSliceArg(value any) ([]string, error) {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...), nil
	case []any:
		out := make([]string, 0, len(typed))
		for index, item := range typed {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("item %d must be a string", index)
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("must be a slice of strings")
	}
}

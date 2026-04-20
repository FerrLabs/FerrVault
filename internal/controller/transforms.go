package controller

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	ffv1alpha1 "github.com/FerrFlow-Org/FerrFlow-Operator/api/v1alpha1"
)

// Transform kind constants. Kept as typed strings rather than an iota enum
// because they're also the wire format the user writes in YAML — any drift
// between code and spec would be a silent breakage.
const (
	TransformPrefix       = "prefix"
	TransformSuffix       = "suffix"
	TransformRename       = "rename"
	TransformBase64Decode = "base64Decode"
	TransformJSONExpand   = "jsonExpand"
)

// TransformError is returned by ApplyTransforms when the CR's transforms list
// can't be applied cleanly. It's shaped so the reconciler can stamp it on the
// Ready condition (reason `TransformError`) without reaching for string
// matching.
type TransformError struct {
	// Index is the zero-based position of the offending transform in
	// spec.transforms. -1 means the error isn't associated with a single
	// entry (e.g. output-key collision across transforms).
	Index int
	// Type is the transform kind that failed, for the user-facing message.
	Type string
	// Msg is the human-readable explanation. No secret values.
	Msg string
}

func (e *TransformError) Error() string {
	if e.Index < 0 {
		return fmt.Sprintf("transform %s: %s", e.Type, e.Msg)
	}
	return fmt.Sprintf("transforms[%d] (%s): %s", e.Index, e.Type, e.Msg)
}

// ApplyTransforms mutates a fresh copy of `data` through each transform in
// order and returns the resulting map. The input is not modified. On error,
// the map returned is the partial state at the failing step — callers are
// expected to surface the error rather than write the partial result.
//
// Semantics (kept deliberately conservative for the MVP):
//
//   - Renaming a missing key is a no-op, not an error. FerrFlow vaults are
//     user-editable; failing the sync just because a key was removed
//     upstream would make transforms fragile.
//   - Prefix / suffix apply to every current key. Running twice stacks.
//   - base64Decode with no keys decodes every value. With keys, only those
//     listed; missing keys are ignored.
//   - jsonExpand drops the source key and emits `<KEY>_<SUB>` entries.
//     Nested objects produce underscore-joined names, upper-cased.
//     Array values are marshalled back to JSON (opinionated: we don't
//     invent an index notation for the MVP).
func ApplyTransforms(input map[string]string, transforms []ffv1alpha1.SecretTransform) (map[string]string, error) {
	out := make(map[string]string, len(input))
	for k, v := range input {
		out[k] = v
	}
	for i, t := range transforms {
		next, err := applyOne(out, t)
		if err != nil {
			if te, ok := err.(*TransformError); ok {
				te.Index = i
				return out, te
			}
			return out, &TransformError{Index: i, Type: t.Type, Msg: err.Error()}
		}
		out = next
	}
	return out, nil
}

func applyOne(in map[string]string, t ffv1alpha1.SecretTransform) (map[string]string, error) {
	switch t.Type {
	case TransformPrefix:
		return applyAffix(in, t.Value, true), nil
	case TransformSuffix:
		return applyAffix(in, t.Value, false), nil
	case TransformRename:
		return applyRename(in, t)
	case TransformBase64Decode:
		return applyBase64Decode(in, t)
	case TransformJSONExpand:
		return applyJSONExpand(in, t)
	default:
		return nil, &TransformError{Type: t.Type, Msg: "unknown transform type"}
	}
}

func applyAffix(in map[string]string, affix string, prefix bool) map[string]string {
	if affix == "" {
		// Short-circuit: renaming every key to itself is pointless work.
		return in
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if prefix {
			out[affix+k] = v
		} else {
			out[k+affix] = v
		}
	}
	return out
}

func applyRename(in map[string]string, t ffv1alpha1.SecretTransform) (map[string]string, error) {
	if t.From == "" || t.To == "" {
		return nil, &TransformError{Type: t.Type, Msg: "rename requires both `from` and `to`"}
	}
	if t.From == t.To {
		return in, nil
	}
	v, ok := in[t.From]
	if !ok {
		// No-op: upstream removed or never had the key. Don't fail the sync.
		return in, nil
	}
	if _, clash := in[t.To]; clash {
		return nil, &TransformError{
			Type: t.Type,
			Msg:  fmt.Sprintf("destination key %q already exists", t.To),
		}
	}
	out := make(map[string]string, len(in))
	for k, vv := range in {
		if k == t.From {
			continue
		}
		out[k] = vv
	}
	out[t.To] = v
	return out, nil
}

func applyBase64Decode(in map[string]string, t ffv1alpha1.SecretTransform) (map[string]string, error) {
	targets := t.Keys
	if len(targets) == 0 {
		targets = make([]string, 0, len(in))
		for k := range in {
			targets = append(targets, k)
		}
		sort.Strings(targets)
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	for _, k := range targets {
		v, ok := out[k]
		if !ok {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			// Try the URL-safe variant before giving up — some upstreams
			// use it and the two are swap-compatible for our purposes.
			decoded, err = base64.URLEncoding.DecodeString(v)
			if err != nil {
				return nil, &TransformError{
					Type: t.Type,
					Msg:  fmt.Sprintf("key %q: not valid base64", k),
				}
			}
		}
		out[k] = string(decoded)
	}
	return out, nil
}

func applyJSONExpand(in map[string]string, t ffv1alpha1.SecretTransform) (map[string]string, error) {
	if t.Key == "" {
		return nil, &TransformError{Type: t.Type, Msg: "jsonExpand requires `key`"}
	}
	raw, ok := in[t.Key]
	if !ok {
		// Missing source: no-op for consistency with rename. The user can
		// compose a preceding transform if they want fail-hard semantics.
		return in, nil
	}
	var parsed any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, &TransformError{
			Type: t.Type,
			Msg:  fmt.Sprintf("key %q: not valid JSON", t.Key),
		}
	}
	obj, ok := parsed.(map[string]any)
	if !ok {
		return nil, &TransformError{
			Type: t.Type,
			Msg:  fmt.Sprintf("key %q: JSON value is not an object", t.Key),
		}
	}
	flat := make(map[string]string)
	if err := flattenJSON(strings.ToUpper(t.Key), obj, flat); err != nil {
		return nil, &TransformError{
			Type: t.Type,
			Msg:  fmt.Sprintf("key %q: %v", t.Key, err),
		}
	}

	// Don't pre-size with `len(in)+len(flat)` — CodeQL flags the add as a
	// potential overflow on 32-bit builds. The performance delta from
	// letting the map grow is noise next to the JSON unmarshal above.
	out := make(map[string]string)
	for k, v := range in {
		if k == t.Key {
			continue
		}
		out[k] = v
	}
	for k, v := range flat {
		if _, clash := out[k]; clash {
			return nil, &TransformError{
				Type: t.Type,
				Msg:  fmt.Sprintf("expanded key %q collides with existing key", k),
			}
		}
		out[k] = v
	}
	return out, nil
}

// flattenJSON walks a decoded JSON object and writes one entry per leaf.
// Nested objects compose names with `_`. Arrays and primitives are stored
// as their JSON representation at the current prefix — avoids inventing an
// array-index notation for the MVP while still round-tripping.
func flattenJSON(prefix string, v any, out map[string]string) error {
	switch typed := v.(type) {
	case map[string]any:
		// Sort keys for deterministic output — the content-hash annotation
		// and user-facing diffs both care.
		keys := make([]string, 0, len(typed))
		for k := range typed {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if err := flattenJSON(prefix+"_"+strings.ToUpper(k), typed[k], out); err != nil {
				return err
			}
		}
	case string:
		out[prefix] = typed
	case bool:
		if typed {
			out[prefix] = "true"
		} else {
			out[prefix] = "false"
		}
	case float64:
		// json.Unmarshal into `any` gives float64 for every number — format
		// back to the shortest lossless decimal so integers don't show up as
		// `42.000000`.
		out[prefix] = formatJSONNumber(typed)
	case nil:
		out[prefix] = ""
	default:
		// Arrays and anything exotic round-trip via JSON. In practice the
		// error branch is unreachable — every value in `v` came out of
		// json.Unmarshal into `any`, which only produces marshalable types
		// — but we propagate it rather than drop the error on the floor.
		b, err := json.Marshal(typed)
		if err != nil {
			return fmt.Errorf("marshal leaf at %s: %w", prefix, err)
		}
		out[prefix] = string(b)
	}
	return nil
}

func formatJSONNumber(f float64) string {
	if f == float64(int64(f)) {
		return fmt.Sprintf("%d", int64(f))
	}
	return fmt.Sprintf("%g", f)
}

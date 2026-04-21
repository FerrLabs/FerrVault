package controller

import (
	"encoding/base64"
	"reflect"
	"sort"
	"strings"
	"testing"

	ffv1alpha1 "github.com/FerrLabs/FerrFlow-Operator/api/v1alpha1"
)

// Tests exercise ApplyTransforms against the minimum set of behaviours we
// promise in the API doc: order-sensitive application, no-op semantics on
// missing sources, hard error on collisions and malformed values.

func TestApplyTransforms_NilAndEmpty(t *testing.T) {
	// Empty transform list returns a disconnected copy of the input. The
	// copy guarantee matters: the reconciler still hashes the input and we
	// don't want in-place mutation to leak into the hash calculation.
	in := map[string]string{"A": "1", "B": "2"}
	out, err := ApplyTransforms(in, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("want %v, got %v", in, out)
	}
	out["A"] = "mutated"
	if in["A"] == "mutated" {
		t.Fatalf("ApplyTransforms must not alias the input map")
	}
}

func TestApplyTransforms_Prefix(t *testing.T) {
	in := map[string]string{"DB_URL": "x", "API_KEY": "y"}
	out, err := ApplyTransforms(in, []ffv1alpha1.SecretTransform{
		{Type: TransformPrefix, Value: "APP_"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]string{"APP_DB_URL": "x", "APP_API_KEY": "y"}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("want %v, got %v", want, out)
	}
}

func TestApplyTransforms_Suffix(t *testing.T) {
	in := map[string]string{"DB_URL": "x"}
	out, err := ApplyTransforms(in, []ffv1alpha1.SecretTransform{
		{Type: TransformSuffix, Value: "_V2"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["DB_URL_V2"] != "x" {
		t.Fatalf("suffix not applied: %v", out)
	}
}

func TestApplyTransforms_RenameMissingKeyIsNoop(t *testing.T) {
	// Upstream removing a key shouldn't break the sync.
	in := map[string]string{"A": "1"}
	out, err := ApplyTransforms(in, []ffv1alpha1.SecretTransform{
		{Type: TransformRename, From: "MISSING", To: "NEW"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(out, in) {
		t.Fatalf("rename of missing key should be no-op, got %v", out)
	}
}

func TestApplyTransforms_RenameCollision(t *testing.T) {
	// Projecting A -> B when B already exists is ambiguous — fail loudly
	// rather than silently picking a winner.
	in := map[string]string{"A": "1", "B": "2"}
	_, err := ApplyTransforms(in, []ffv1alpha1.SecretTransform{
		{Type: TransformRename, From: "A", To: "B"},
	})
	if err == nil {
		t.Fatalf("expected TransformError for rename collision")
	}
	te, ok := err.(*TransformError)
	if !ok {
		t.Fatalf("want *TransformError, got %T", err)
	}
	if te.Index != 0 || te.Type != TransformRename {
		t.Fatalf("wrong TransformError: %+v", te)
	}
}

func TestApplyTransforms_RenameRequiresBothFields(t *testing.T) {
	_, err := ApplyTransforms(map[string]string{"A": "1"}, []ffv1alpha1.SecretTransform{
		{Type: TransformRename, From: "A"},
	})
	if err == nil {
		t.Fatalf("expected error when `to` is missing")
	}
}

func TestApplyTransforms_Base64DecodeSelected(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("hunter2"))
	in := map[string]string{
		"SECRET":    encoded,
		"PLAINTEXT": "untouched",
	}
	out, err := ApplyTransforms(in, []ffv1alpha1.SecretTransform{
		{Type: TransformBase64Decode, Keys: []string{"SECRET"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["SECRET"] != "hunter2" {
		t.Fatalf("expected decoded value, got %q", out["SECRET"])
	}
	if out["PLAINTEXT"] != "untouched" {
		t.Fatalf("unlisted key was altered: %q", out["PLAINTEXT"])
	}
}

func TestApplyTransforms_Base64DecodeAll(t *testing.T) {
	in := map[string]string{
		"A": base64.StdEncoding.EncodeToString([]byte("foo")),
		"B": base64.StdEncoding.EncodeToString([]byte("bar")),
	}
	out, err := ApplyTransforms(in, []ffv1alpha1.SecretTransform{
		{Type: TransformBase64Decode},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["A"] != "foo" || out["B"] != "bar" {
		t.Fatalf("want foo/bar, got %v", out)
	}
}

func TestApplyTransforms_Base64DecodeInvalid(t *testing.T) {
	in := map[string]string{"A": "!!!not-base64!!!"}
	_, err := ApplyTransforms(in, []ffv1alpha1.SecretTransform{
		{Type: TransformBase64Decode, Keys: []string{"A"}},
	})
	if err == nil {
		t.Fatalf("expected error for invalid base64")
	}
	if !strings.Contains(err.Error(), "not valid base64") {
		t.Fatalf("want base64 error, got %v", err)
	}
}

func TestApplyTransforms_JSONExpand(t *testing.T) {
	in := map[string]string{
		"CONFIG_JSON": `{"db":{"host":"pg","port":5432},"debug":true,"name":"svc"}`,
		"OTHER":       "keep",
	}
	out, err := ApplyTransforms(in, []ffv1alpha1.SecretTransform{
		{Type: TransformJSONExpand, Key: "CONFIG_JSON"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := out["CONFIG_JSON"]; ok {
		t.Fatalf("source key should be dropped: %v", out)
	}
	want := map[string]string{
		"OTHER":               "keep",
		"CONFIG_JSON_DB_HOST": "pg",
		"CONFIG_JSON_DB_PORT": "5432",
		"CONFIG_JSON_DEBUG":   "true",
		"CONFIG_JSON_NAME":    "svc",
	}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("want %v, got %v", want, out)
	}
}

func TestApplyTransforms_JSONExpandNonObject(t *testing.T) {
	// Top-level must be an object — arrays and scalars have no natural
	// key/value fan-out.
	in := map[string]string{"LIST": `[1,2,3]`}
	_, err := ApplyTransforms(in, []ffv1alpha1.SecretTransform{
		{Type: TransformJSONExpand, Key: "LIST"},
	})
	if err == nil {
		t.Fatalf("expected error for non-object JSON")
	}
}

func TestApplyTransforms_JSONExpandMissingKeyIsNoop(t *testing.T) {
	in := map[string]string{"A": "1"}
	out, err := ApplyTransforms(in, []ffv1alpha1.SecretTransform{
		{Type: TransformJSONExpand, Key: "MISSING"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(out, in) {
		t.Fatalf("missing source should be no-op, got %v", out)
	}
}

func TestApplyTransforms_JSONExpandCollision(t *testing.T) {
	// A pre-existing key named `FOO_BAR` would collide with a flattened
	// `{FOO: {BAR: ...}}`. Fail rather than silently overwrite.
	in := map[string]string{
		"FOO":     `{"BAR":"new"}`,
		"FOO_BAR": "existing",
	}
	_, err := ApplyTransforms(in, []ffv1alpha1.SecretTransform{
		{Type: TransformJSONExpand, Key: "FOO"},
	})
	if err == nil {
		t.Fatalf("expected collision error")
	}
}

func TestApplyTransforms_Composition(t *testing.T) {
	// Rename first, then prefix — order matters. If prefix came first, the
	// rename `from` would point to an already-prefixed key.
	encoded := base64.StdEncoding.EncodeToString([]byte("s3cret"))
	in := map[string]string{
		"DATABASE_URL": "postgres://",
		"STRIPE_KEY":   encoded,
		"CONFIG_JSON":  `{"region":"eu-west"}`,
	}
	out, err := ApplyTransforms(in, []ffv1alpha1.SecretTransform{
		{Type: TransformRename, From: "DATABASE_URL", To: "DB_URL"},
		{Type: TransformBase64Decode, Keys: []string{"STRIPE_KEY"}},
		{Type: TransformJSONExpand, Key: "CONFIG_JSON"},
		{Type: TransformPrefix, Value: "APP_"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]string{
		"APP_DB_URL":             "postgres://",
		"APP_STRIPE_KEY":         "s3cret",
		"APP_CONFIG_JSON_REGION": "eu-west",
	}
	if !reflect.DeepEqual(out, want) {
		gotKeys := make([]string, 0, len(out))
		for k := range out {
			gotKeys = append(gotKeys, k)
		}
		sort.Strings(gotKeys)
		t.Fatalf("want %v, got %v (keys=%v)", want, out, gotKeys)
	}
}

func TestApplyTransforms_UnknownType(t *testing.T) {
	_, err := ApplyTransforms(map[string]string{"A": "1"}, []ffv1alpha1.SecretTransform{
		{Type: "encrypt"},
	})
	if err == nil {
		t.Fatalf("expected error for unknown transform type")
	}
	if !strings.Contains(err.Error(), "unknown transform type") {
		t.Fatalf("want unknown-type error, got %v", err)
	}
}

func TestApplyTransforms_ErrorCarriesIndex(t *testing.T) {
	// A failing transform at index 2 must surface that index so the user
	// can pinpoint the bad entry in `spec.transforms`.
	in := map[string]string{"A": "valid", "B": "!!bad"}
	_, err := ApplyTransforms(in, []ffv1alpha1.SecretTransform{
		{Type: TransformPrefix, Value: "X_"},
		{Type: TransformSuffix, Value: "_Y"},
		{Type: TransformBase64Decode, Keys: []string{"X_B_Y"}},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	te, ok := err.(*TransformError)
	if !ok {
		t.Fatalf("want *TransformError, got %T", err)
	}
	if te.Index != 2 {
		t.Fatalf("want index 2, got %d (%s)", te.Index, te.Error())
	}
}

func TestApplyTransforms_JSONExpandNested(t *testing.T) {
	in := map[string]string{
		"C": `{"a":{"b":{"c":"deep"}}}`,
	}
	out, err := ApplyTransforms(in, []ffv1alpha1.SecretTransform{
		{Type: TransformJSONExpand, Key: "C"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["C_A_B_C"] != "deep" {
		t.Fatalf("nested flatten failed: %v", out)
	}
}

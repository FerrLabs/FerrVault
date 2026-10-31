package controller

import (
	"encoding/base64"
	"errors"
	"reflect"
	"testing"

	fvv1alpha1 "github.com/FerrLabs/FerrVault/api/ferrvault/v1alpha1"
)

var fuzzTransformTypes = []string{
	TransformPrefix,
	TransformSuffix,
	TransformRename,
	TransformBase64Decode,
	TransformJSONExpand,
	"unknown",
}

func FuzzApplyTransforms(f *testing.F) {
	f.Add(uint8(4), "CONFIG", `{"a":{"b":1},"c":[1,2],"d":null,"e":true}`, "", "", "")
	f.Add(uint8(3), "TOKEN", "c2VjcmV0", "", "", "")
	f.Add(uint8(3), "TOKEN", "c2VjcmV0_-8=", "", "", "")
	f.Add(uint8(0), "K", "v", "APP_", "", "")
	f.Add(uint8(1), "K", "v", "_V2", "", "")
	f.Add(uint8(2), "K", "v", "", "K", "K2")
	f.Add(uint8(2), "K", "v", "", "K", "K")
	f.Add(uint8(4), "CONFIG", `{"x":1e400}`, "", "", "")

	f.Fuzz(func(t *testing.T, kind uint8, key, val, affix, from, to string) {
		typ := fuzzTransformTypes[int(kind)%len(fuzzTransformTypes)]
		if key == "OTHER" {
			t.Skip()
		}
		input := map[string]string{key: val, "OTHER": "o"}
		snapshot := map[string]string{key: val, "OTHER": "o"}
		transforms := []fvv1alpha1.SecretTransform{
			{Type: typ, Value: affix, From: from, To: to, Key: key, Keys: []string{key}},
		}

		out, err := ApplyTransforms(input, transforms)
		if !reflect.DeepEqual(input, snapshot) {
			t.Fatalf("input mutated: before %q after %q", snapshot, input)
		}
		if err != nil {
			if te := (*TransformError)(nil); !errors.As(err, &te) {
				t.Fatalf("error is %T, want *TransformError", err)
			}
		}

		out2, err2 := ApplyTransforms(input, transforms)
		if (err == nil) != (err2 == nil) || !reflect.DeepEqual(out, out2) {
			t.Fatalf("not deterministic: %q/%v vs %q/%v", out, err, out2, err2)
		}

		if err != nil {
			return
		}
		switch typ {
		case TransformPrefix:
			if got := out[affix+key]; got != val {
				t.Fatalf("prefix lost value for %q: %q", key, got)
			}
		case TransformSuffix:
			if got := out[key+affix]; got != val {
				t.Fatalf("suffix lost value for %q: %q", key, got)
			}
		case TransformBase64Decode:
			if want, ok := decodeEither(val); ok && out[key] != want {
				t.Fatalf("base64Decode %q: got %q want %q", val, out[key], want)
			}
		}
	})
}

func decodeEither(s string) (string, bool) {
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return string(b), true
	}
	if b, err := base64.URLEncoding.DecodeString(s); err == nil {
		return string(b), true
	}
	return "", false
}

func FuzzHashSecretData(f *testing.F) {
	f.Add("a", "1", "b", "2")
	f.Add("a\x00b", "c", "a", "b\x00c")
	f.Add("", "", "", "")

	f.Fuzz(func(t *testing.T, k1, v1, k2, v2 string) {
		if k1 == k2 {
			t.Skip()
		}
		a := map[string]string{k1: v1, k2: v2}
		b := map[string]string{k2: v2, k1: v1}
		if hashSecretData(a) != hashSecretData(b) {
			t.Fatalf("hash depends on construction order for %q", a)
		}
	})
}

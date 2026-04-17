// Package v1alpha1 holds the CRD types for ferrflow.io/v1alpha1 — the
// FerrFlow-Operator's first API version. The `v1alpha1` suffix advertises
// that field names and semantics may still change before we promote to v1.
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

// GroupVersion is the API group/version pair the controller watches.
var GroupVersion = schema.GroupVersion{Group: "ferrflow.io", Version: "v1alpha1"}

// SchemeBuilder registers the package's types with the runtime scheme so that
// the manager can encode/decode them and the informers can list/watch them.
var SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

// AddToScheme is the canonical entry point used by cmd/main.go.
var AddToScheme = SchemeBuilder.AddToScheme

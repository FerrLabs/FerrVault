package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// hashSecretData returns a stable SHA-256 over the sorted key=value pairs so
// equal maps produce equal hashes regardless of Go's map iteration order.
// Values are NOT logged or otherwise surfaced — only the digest leaves the
// function.
func hashSecretData(data map[string]string) string {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{0x00})
		h.Write([]byte(data[k]))
		h.Write([]byte{0x00})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// setCondition is the minimal upsert we need: replace the entry with the same
// Type or append. Matches the semantics of `meta.SetStatusCondition` but
// avoids pulling in the helper for a single call site.
func setCondition(conds *[]metav1.Condition, c metav1.Condition) {
	c.LastTransitionTime = metav1.Now()
	for i := range *conds {
		if (*conds)[i].Type == c.Type {
			if (*conds)[i].Status == c.Status {
				c.LastTransitionTime = (*conds)[i].LastTransitionTime
			}
			(*conds)[i] = c
			return
		}
	}
	*conds = append(*conds, c)
}

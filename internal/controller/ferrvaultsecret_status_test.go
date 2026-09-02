package controller

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/FerrLabs/FerrVault/internal/ferrvault"
)

// Le bug d'origine : un 429 arrivant une seconde après une synchronisation
// réussie réécrivait Ready=False par-dessus. Quatorze secrets dont les données
// étaient à jour dans le cluster se déclaraient en panne, et une vraie panne y
// aurait été indiscernable.
//
// Le correctif tient à ce que le 429 sorte de `Reconcile` AVANT d'atteindre
// `failReadyWithRequeue`. Ce test fige ce tri : il est le seul point où la
// distinction se joue.
func TestOnlyRateLimitingSkipsTheStatusWrite(t *testing.T) {
	cases := []struct {
		name          string
		err           error
		wantRateLimit bool
		wantAuth      bool
		wantNotFound  bool
	}{
		{"429 : report, statut inchange", &ferrvault.APIError{Status: 429, Message: "Too Many Requests"}, true, false, false},
		{"401 : panne reelle", &ferrvault.AuthError{Kind: ferrvault.AuthUnauthorized, Message: "nope"}, false, true, false},
		{"404 : panne reelle", &ferrvault.NotFoundError{Message: "vault absent"}, false, false, true},
		{"500 : pas une limitation", &ferrvault.APIError{Status: 500, Message: "boom"}, false, false, false},
		{"503 : pas une limitation", &ferrvault.APIError{Status: 503, Message: "indisponible"}, false, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ferrvault.IsRateLimited(c.err); got != c.wantRateLimit {
				t.Fatalf("IsRateLimited: want %v, got %v", c.wantRateLimit, got)
			}
			if got := ferrvault.IsAuthError(c.err); got != c.wantAuth {
				t.Fatalf("IsAuthError: want %v, got %v", c.wantAuth, got)
			}
			if got := ferrvault.IsNotFound(c.err); got != c.wantNotFound {
				t.Fatalf("IsNotFound: want %v, got %v", c.wantNotFound, got)
			}
		})
	}
}

// `setCondition` conserve `LastTransitionTime` quand le statut ne change pas.
// C'est ce qui permet de distinguer « en panne depuis dix minutes » de « en
// panne depuis trois jours » en lisant la ressource — la seule façon, une fois
// la liste blanche retirée, de juger de la gravité d'un `Unreachable`.
func TestSetConditionPreservesTransitionTime(t *testing.T) {
	old := metav1.NewTime(time.Now().Add(-72 * time.Hour))
	conds := []metav1.Condition{{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		Reason:             "Unreachable",
		LastTransitionTime: old,
	}}
	setCondition(&conds, metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionFalse,
		Reason:  "Unreachable",
		Message: "toujours injoignable",
	})
	if !conds[0].LastTransitionTime.Equal(&old) {
		t.Fatal("un echec qui persiste ne doit pas rajeunir sa date de transition")
	}

	setCondition(&conds, metav1.Condition{
		Type:   "Ready",
		Status: metav1.ConditionTrue,
		Reason: "Synced",
	})
	if conds[0].LastTransitionTime.Equal(&old) {
		t.Fatal("un passage a True doit redater la transition")
	}
}

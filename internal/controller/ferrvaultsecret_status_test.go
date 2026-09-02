package controller

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fvv1alpha1 "github.com/FerrLabs/FerrVault/api/ferrvault/v1alpha1"
)

// The bug these tests pin: a rate-limited retry, one second after a successful
// sync of the same generation, rewrote Ready=False over it. Fourteen secrets
// whose data was current in the cluster reported as broken, and a real failure
// would have been indistinguishable among them.

func syncedAt(gen int64, observed int64, when *metav1.Time) *fvv1alpha1.FerrVaultSecret {
	cr := &fvv1alpha1.FerrVaultSecret{}
	cr.Generation = gen
	cr.Status.ObservedGeneration = observed
	cr.Status.LastSyncedAt = when
	return cr
}

func TestSyncedThisGeneration(t *testing.T) {
	now := metav1.Now()
	cases := []struct {
		name string
		cr   *fvv1alpha1.FerrVaultSecret
		want bool
	}{
		{"jamais synchronise", syncedAt(1, 0, nil), false},
		{"synchronise sur cette generation", syncedAt(3, 3, &now), true},
		{
			// Le spec a changé depuis le dernier succès : un échec porte sur
			// une configuration que personne n'a encore réussi à appliquer.
			"spec modifie depuis le dernier succes",
			syncedAt(4, 3, &now),
			false,
		},
		{"generation a jour mais aucune synchro", syncedAt(2, 2, nil), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := syncedThisGeneration(c.cr); got != c.want {
				t.Fatalf("want %v, got %v", c.want, got)
			}
		})
	}
}

func TestIsTransient(t *testing.T) {
	// Liste blanche courte, pas liste noire : une raison ajoutée plus tard est
	// traitée comme une panne réelle jusqu'à décision contraire.
	for _, r := range []string{"Unreachable", "RateLimited"} {
		if !isTransient(r) {
			t.Fatalf("%s devrait etre transitoire", r)
		}
	}
	// Celles-ci sont de vraies pannes : un jeton révoqué, un vault supprimé.
	// Les masquer derrière un succès antérieur laisserait l'opérateur vert
	// alors qu'il ne peut plus rien lire — bien pire que le bug corrigé ici.
	for _, r := range []string{
		"AuthFailed", "VaultNotFound", "InvalidConnection",
		"TransformError", "SecretWriteFailed", "MissingKeys", "TokenUnreadable",
	} {
		if isTransient(r) {
			t.Fatalf("%s ne doit PAS etre traite comme transitoire", r)
		}
	}
	if isTransient("UneRaisonAjouteePlusTard") {
		t.Fatal("une raison inconnue doit etre traitee comme une panne reelle")
	}
}

func TestTransientFailureDoesNotOverwriteASuccess(t *testing.T) {
	now := metav1.NewTime(time.Now())
	cr := syncedAt(2, 2, &now)

	// Un 429 juste après un succès sur la même génération : les données sont
	// à jour, la ressource ne doit pas se déclarer en échec.
	if !isTransient("RateLimited") || !syncedThisGeneration(cr) {
		t.Fatal("premisse du test")
	}

	// Mais un échec d'authentification sur la même ressource, lui, doit
	// ressortir : c'est une panne que le temps ne répare pas.
	if isTransient("AuthFailed") {
		t.Fatal("AuthFailed ne doit jamais etre masque par un succes anterieur")
	}
}

package controller

import (
	"errors"
	"net/http"
	"testing"

	"github.com/FerrLabs/FerrVault/internal/ferrvault"
)

// Le tri que fait `Reconcile` sur le résultat de `probe`.
//
// Un 429 doit sortir tôt, sans réécrire la condition : la sonde était bien
// formée et autorisée, le serveur a seulement demandé qu'on le rappelle. Le
// traiter comme une panne a figé 25 connexions sur 33, entraînant avec elles
// tous les `FerrVaultSecret` qui en dépendent, alors que les instances
// FerrVault répondaient normalement.
//
// Le test porte sur le prédicat appliqué aux erreurs que `probe` remonte
// réellement, et non sur trois valeurs construites pour l'occasion : c'est la
// distinction qui décide si la condition est écrasée ou laissée en place.
func TestOnlyRateLimitingSkipsTheConditionUpdate(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			// Le seul cas qui doit court-circuiter.
			name: "429 : report, condition inchangee",
			err:  &ferrvault.APIError{Status: http.StatusTooManyRequests, Message: "Too Many Requests"},
			want: true,
		},
		{
			// Succès : `probe` rend `nil`, et le prédicat doit le supporter
			// sans paniquer — c'est le chemin nominal, de loin le plus fréquent.
			name: "succes : aucune erreur",
			err:  nil,
			want: false,
		},
		{
			// Les vraies pannes doivent continuer d'écrire la condition, sinon
			// une connexion cassée resterait verte indéfiniment : l'inverse
			// exact du défaut corrigé, et bien pire.
			name: "500 : vraie panne",
			err:  &ferrvault.APIError{Status: http.StatusInternalServerError, Message: "boom"},
			want: false,
		},
		{
			name: "401 : jeton refuse",
			err:  &ferrvault.AuthError{Kind: ferrvault.AuthUnauthorized, Message: "nope"},
			want: false,
		},
		{
			// `TokenUnreadable` : le Secret n'existe pas. Ne se répare jamais
			// seul, doit rester visible.
			name: "Secret de jeton absent",
			err:  errors.New(`load token Secret ns/name: Secret "name" not found`),
			want: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ferrvault.IsRateLimited(c.err); got != c.want {
				t.Fatalf("IsRateLimited(%v) = %v, attendu %v", c.err, got, c.want)
			}
		})
	}
}

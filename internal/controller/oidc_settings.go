package controller

import (
	"fmt"

	fvv1alpha1 "github.com/FerrLabs/FerrVault/api/ferrvault/v1alpha1"
)

type OIDCSettings struct {
	TokenPath string
	URL       string
}

type OIDCNotAllowedError struct {
	Namespace string
	Name      string
	Field     string
	Got       string
	Allowed   string
}

func (e *OIDCNotAllowedError) Error() string {
	allowed := fmt.Sprintf("the operator only allows %q", e.Allowed)
	if e.Allowed == "" {
		allowed = "the operator has no OIDC url configured (--oidc-url)"
	}
	return fmt.Sprintf("FerrVaultConnection %s/%s: oidc %s %q refused, %s",
		e.Namespace, e.Name, e.Field, e.Got, allowed)
}

func (s OIDCSettings) tokenPath() string {
	if s.TokenPath == "" {
		return fvv1alpha1.DefaultTokenPath
	}
	return s.TokenPath
}

func (s OIDCSettings) validate(conn *fvv1alpha1.FerrVaultConnection) error {
	if got := conn.Spec.OIDC.TokenPath; got != "" && got != s.tokenPath() {
		return &OIDCNotAllowedError{conn.Namespace, conn.Name, "tokenPath", got, s.tokenPath()}
	}
	if got := conn.Spec.URL; got != s.URL {
		return &OIDCNotAllowedError{conn.Namespace, conn.Name, "url", got, s.URL}
	}
	return nil
}

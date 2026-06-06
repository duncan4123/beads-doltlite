//go:build cgo

package doltlite

import (
	"errors"
	"testing"
)

func TestIsRetryableConcurrencyErrorCatalogPrepare(t *testing.T) {
	err := errors.New("doltlite commit: failed to prepare catalog")
	if !isRetryableConcurrencyError(err) {
		t.Fatalf("isRetryableConcurrencyError(%q) = false, want true", err)
	}
}

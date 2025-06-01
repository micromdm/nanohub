package nanohub

import (
	"context"
	"crypto/x509"
	"testing"

	"github.com/micromdm/nanomdm/storage/inmem"
)

type nopVerifier struct{}

func (v *nopVerifier) Verify(context.Context, *x509.Certificate) error {
	return nil
}

func TestInvalidConfig(t *testing.T) {
	s := inmem.New()

	// requires a separate check-in handler
	_, err := New(s, WithoutServerCombinedHandler())
	if err == nil {
		t.Fatal("expected error")
	}

	// specifying a verifier and roots (or intermediate) PEMs should not be allowed
	_, err = New(s, WithRootPEMs([]byte("hello")), WithVerifier(new(nopVerifier)))
	if err == nil {
		t.Fatal("expected error")
	}

}

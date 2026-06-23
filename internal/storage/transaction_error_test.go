package storage

import (
	"errors"
	"testing"
)

func TestPostTransactionCommitErrorWrapsCause(t *testing.T) {
	cause := errors.New("doltlite add dependencies: database is locked")
	err := NewPostTransactionCommitError("bd: graph-apply 2 nodes", cause)

	postCommitErr, ok := AsPostTransactionCommitError(err)
	if !ok {
		t.Fatalf("AsPostTransactionCommitError did not match %T", err)
	}
	if postCommitErr.CommitMessage != "bd: graph-apply 2 nodes" {
		t.Fatalf("CommitMessage = %q", postCommitErr.CommitMessage)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("wrapped error does not match cause")
	}
}

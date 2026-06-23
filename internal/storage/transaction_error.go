package storage

import (
	"errors"
	"fmt"
)

// PostTransactionCommitError reports a failure that happened after the SQL
// transaction body committed, while creating the backend version-control commit.
type PostTransactionCommitError struct {
	CommitMessage string
	Err           error
}

func NewPostTransactionCommitError(commitMessage string, err error) error {
	if err == nil {
		return nil
	}
	return &PostTransactionCommitError{
		CommitMessage: commitMessage,
		Err:           err,
	}
}

func (e *PostTransactionCommitError) Error() string {
	if e == nil || e.Err == nil {
		return "post-transaction commit failed"
	}
	if e.CommitMessage == "" {
		return fmt.Sprintf("post-transaction commit failed: %v", e.Err)
	}
	return fmt.Sprintf("post-transaction commit %q failed: %v", e.CommitMessage, e.Err)
}

func (e *PostTransactionCommitError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func AsPostTransactionCommitError(err error) (*PostTransactionCommitError, bool) {
	var target *PostTransactionCommitError
	if errors.As(err, &target) {
		return target, true
	}
	return nil, false
}

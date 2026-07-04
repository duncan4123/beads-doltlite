//go:build cgo

package doltlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestValidateDoltliteRemoteSyncURLRejectsGitProtocol(t *testing.T) {
	err := validateDoltliteRemoteSyncURL("origin", "git+ssh://git@github.com/org/repo.git")
	if !errors.Is(err, errDoltliteUnsupportedRemoteURL) {
		t.Fatalf("validateDoltliteRemoteSyncURL error = %v, want errDoltliteUnsupportedRemoteURL", err)
	}
	for _, want := range []string{"origin", "git+ssh://git@github.com/org/repo.git", "DoltLite", "file://", "http://", "Dolt backend"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q should contain %q", err.Error(), want)
		}
	}
}

func TestValidateDoltliteRemoteSyncURLAllowsNativeRemotes(t *testing.T) {
	for _, url := range []string{"file:///tmp/beads-remote", "http://127.0.0.1:8080/repo"} {
		if err := validateDoltliteRemoteSyncURL("origin", url); err != nil {
			t.Fatalf("validateDoltliteRemoteSyncURL(%q) = %v, want nil", url, err)
		}
	}
}

func TestGuardDoltliteRemoteSyncURL(t *testing.T) {
	t.Run("rejects unsupported configured remote before transfer", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		defer db.Close()

		mock.ExpectQuery(`SELECT url FROM dolt_remotes WHERE name = \?`).
			WithArgs("origin").
			WillReturnRows(sqlmock.NewRows([]string{"url"}).AddRow("git+ssh://git@github.com/org/repo.git"))

		err = guardDoltliteRemoteSyncURL(context.Background(), db, "origin")
		if !errors.Is(err, errDoltliteUnsupportedRemoteURL) {
			t.Fatalf("guardDoltliteRemoteSyncURL error = %v, want errDoltliteUnsupportedRemoteURL", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("leaves missing remote to transfer path", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		defer db.Close()

		mock.ExpectQuery(`SELECT url FROM dolt_remotes WHERE name = \?`).
			WithArgs("origin").
			WillReturnError(sql.ErrNoRows)

		if err := guardDoltliteRemoteSyncURL(context.Background(), db, "origin"); err != nil {
			t.Fatalf("guardDoltliteRemoteSyncURL missing remote = %v, want nil", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})
}

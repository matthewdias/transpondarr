package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/matthewdias/transpondarr/internal/core/decide"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

const (
	// MaxAliases bounds the searches a pass adds, one per alias.
	MaxAliases     = 5
	MaxAliasLength = 200
)

// AliasError is a rejected alias list, worded for the person who entered it.
type AliasError struct{ msg string }

func (e *AliasError) Error() string { return e.msg }

// SetAliases replaces a title's aliases (#345) and returns them as stored. The
// title's search cadence resets too, because what its search asks for changed.
func (s *Service) SetAliases(ctx context.Context, titleID int64, aliases []string) ([]string, error) {
	clean, err := cleanAliases(aliases)
	if err != nil {
		return nil, err
	}
	tx, err := s.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful Commit
	q := s.store.Q.WithTx(tx)

	rows, err := q.SetTitleAliases(ctx, db.SetTitleAliasesParams{Aliases: store.EncodeAliases(clean), ID: titleID})
	if err != nil {
		return nil, fmt.Errorf("store aliases: %w", err)
	}
	if rows == 0 {
		return nil, fmt.Errorf("title %d: %w", titleID, sql.ErrNoRows)
	}
	if err := q.ResetTitleSearchState(ctx, titleID); err != nil {
		return nil, fmt.Errorf("reset search cadence: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return clean, nil
}

// cleanAliases collapses each alias's whitespace and refuses a list it cannot store.
func cleanAliases(in []string) ([]string, error) {
	if len(in) > MaxAliases {
		return nil, &AliasError{fmt.Sprintf("A title can have at most %d aliases.", MaxAliases)}
	}
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, a := range in {
		a = strings.Join(strings.Fields(a), " ")
		key := strings.ToLower(a)
		switch {
		case a == "":
			return nil, &AliasError{"An alias can't be blank."}
		case utf8.RuneCountInString(a) > MaxAliasLength:
			return nil, &AliasError{fmt.Sprintf("An alias is longer than %d characters.", MaxAliasLength)}
		case !decide.Matchable(a):
			return nil, &AliasError{fmt.Sprintf("The alias %q has no Latin letters or digits, so it can't match a release name.", a)}
		case seen[key]:
			return nil, &AliasError{fmt.Sprintf("The alias %q is listed twice. Remove one.", a)}
		}
		seen[key] = true
		out = append(out, a)
	}
	return out, nil
}

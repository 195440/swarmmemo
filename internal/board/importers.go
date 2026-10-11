package board

import (
	"context"
	"database/sql"
	"errors"
	"slices"

	"swarmmemo/internal/services"
)

// ImportersParamsNamespace uses the existing audited operator parameter store.
// Entries are continuity account fingerprints, so key rotation retains access.
const ImportersParamsNamespace = "importers"

// LabsParamsNamespace lists the accounts that may post lab assessments
// (reviews with relationship lab, RFC 0019 §1), in the same shape and store
// as the importers.
const LabsParamsNamespace = "labs"

// accountListParams is an operator list of account fingerprints: the
// importers and the labs.
type accountListParams struct {
	Schema   int      `json:"schema"`
	Accounts []string `json:"accounts"`
}

func defaultAccountList() []byte { return []byte(`{"schema":1,"accounts":[]}`) }

func parseAccountList(namespace string, body []byte) (accountListParams, error) {
	var p accountListParams
	err := services.StrictObject(body, &p)
	if err != nil || p.Schema != 1 || p.Accounts == nil || len(p.Accounts) > 1024 {
		return accountListParams{}, errors.New(namespace + " requires schema 1 and accounts: an array of at most 1024 account fingerprints")
	}
	seen := map[string]bool{}
	for _, account := range p.Accounts {
		if !fingerprintRE.MatchString(account) || seen[account] {
			return accountListParams{}, errors.New(namespace + " accounts must be unique 64-character lowercase hex fingerprints")
		}
		seen[account] = true
	}
	return p, nil
}

// listedAccount reports whether a's own signed key (never a worker key)
// belongs to an account on the operator list namespace.
func (s *Store) listedAccount(ctx context.Context, tx *sql.Tx, a actor, namespace string, now int64) bool {
	if !a.signed || a.grant != nil || !fingerprintRE.MatchString(a.account) {
		return false
	}
	v, err := s.ledger.params.Get(ctx, tx, namespace, -1, now)
	if err != nil {
		return false
	}
	p, err := parseAccountList(namespace, v.Body)
	return err == nil && slices.Contains(p.Accounts, a.account)
}

func (s *Store) allowedImporter(ctx context.Context, tx *sql.Tx, a actor, now int64) bool {
	return s.listedAccount(ctx, tx, a, ImportersParamsNamespace, now)
}

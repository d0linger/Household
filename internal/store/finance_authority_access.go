package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"slices"
	"strings"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type financeGrantInput struct {
	MembershipID string `json:"membership_id"`
	Permission   string `json:"permission"`
}

func financeAccessCommand(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, c FinanceCommand) (Record, error) {
	var targetKind, targetID, mode, responsible, reason string
	var policyVersion int64
	var grants []financeGrantInput
	switch c.Action {
	case "change":
		var in struct {
			TargetKind    string              `json:"target_kind"`
			TargetID      string              `json:"target_id"`
			PolicyVersion int64               `json:"policy_version"`
			Mode          string              `json:"access_mode"`
			Responsible   *string             `json:"responsible_membership_id"`
			Grants        []financeGrantInput `json:"grants"`
		}
		if err := decodeFinancePayload(c.Payload, &in); err != nil {
			return nil, err
		}
		if in.Mode != "household" && in.Mode != "selected" || in.Grants == nil {
			return nil, Invalid{"Freigabemodus und vollständige Mitgliederliste prüfen"}
		}
		targetKind, targetID, policyVersion, mode, grants = in.TargetKind, in.TargetID, in.PolicyVersion, in.Mode, in.Grants
		if in.Responsible != nil {
			responsible = *in.Responsible
		}
	case "assign":
		var in struct {
			TargetKind    string `json:"target_kind"`
			TargetID      string `json:"target_id"`
			PolicyVersion int64  `json:"policy_version"`
			Responsible   string `json:"responsible_membership_id"`
		}
		if err := decodeFinancePayload(c.Payload, &in); err != nil {
			return nil, err
		}
		if !strings.EqualFold(in.Responsible, id.MembershipID) {
			return nil, ErrForbidden
		}
		targetKind, targetID, policyVersion, responsible = in.TargetKind, in.TargetID, in.PolicyVersion, in.Responsible
	case "recover":
		var in struct {
			TargetKind    string `json:"target_kind"`
			TargetID      string `json:"target_id"`
			PolicyVersion int64  `json:"policy_version"`
			Responsible   string `json:"new_responsible_membership_id"`
			Reason        string `json:"reason"`
		}
		if err := decodeFinancePayload(c.Payload, &in); err != nil {
			return nil, err
		}
		if id.Role != "admin" || !id.Recent() || c.Version != 0 || !financeText(in.Reason, 1, 1000) {
			return nil, ErrForbidden
		}
		targetKind, targetID, policyVersion, responsible, reason = in.TargetKind, in.TargetID, in.PolicyVersion, in.Responsible, in.Reason
	default:
		return nil, ErrNotFound
	}
	if !slices.Contains([]string{"entries", "accounts", "cash_events", "category_budgets", "savings_goals",
		"recurring_templates", "categorization_rules", "reconciliations", "receipt_reviews"}, targetKind) ||
		!value.ValidUUID(targetID) || !strings.EqualFold(targetID, c.ID) || policyVersion <= 0 ||
		responsible != "" && !value.ValidUUID(responsible) || len(grants) > 100 {
		return nil, Invalid{"Finanzziel, Verantwortung und Freigabeversion prüfen"}
	}
	targetID, responsible = strings.ToLower(targetID), strings.ToLower(responsible)
	if mode == "selected" && responsible == "" || c.Action != "change" && responsible == "" {
		return nil, Invalid{"Aktives verantwortliches Mitglied auswählen"}
	}
	seen := make(map[string]bool, len(grants))
	for i := range grants {
		g := &grants[i]
		g.MembershipID = strings.ToLower(g.MembershipID)
		if !value.ValidUUID(g.MembershipID) || seen[g.MembershipID] || g.Permission != "view" && g.Permission != "edit" {
			return nil, Invalid{"Eindeutige Mitgliedsfreigaben mit Lesen oder Bearbeiten prüfen"}
		}
		seen[g.MembershipID] = true
	}
	slices.SortFunc(grants, func(a, b financeGrantInput) int { return strings.Compare(a.MembershipID, b.MembershipID) })
	if grants == nil {
		grants = []financeGrantInput{}
	}
	encoded, err := json.Marshal(grants)
	if err != nil {
		return nil, err
	}
	var reasonHash []byte
	if reason != "" {
		digest := sha256.Sum256([]byte(reason))
		reasonHash = digest[:]
	}
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT core.finance_access_command($1,$2,$3,nullif($4,'')::uuid,$5,$6,$7,$8,$9,$10,$11,$12,$13,nullif($14,'')::uuid,$15::jsonb,$16)`,
		id.HouseholdID, id.UserID, id.MembershipID, id.SessionID, id.AuthVersion, scope.generation,
		c.Action, targetKind, targetID, c.Version, policyVersion, c.AccessVersion, mode, responsible, encoded, reasonHash).Scan(&raw)
	if err != nil {
		return nil, financeAuthorityError(err)
	}
	return decode(raw)
}

func financeAccessInfoTx(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, kind, rid string) (Record, error) {
	if !financeKnownKind(kind) || !value.ValidUUID(rid) {
		return nil, ErrNotFound
	}
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT core.finance_access_projection($1,$2,$3,nullif($4,'')::uuid,$5,$6,$7,$8,true)`,
		id.HouseholdID, id.UserID, id.MembershipID, id.SessionID, id.AuthVersion, scope.generation,
		kind, strings.ToLower(rid)).Scan(&raw)
	if err != nil {
		return nil, financeAuthorityError(err)
	}
	return decode(raw)
}

// FinanceAccessInfo returns current policy metadata; recovery never reads a
// private booking, amount, title, filename or hidden aggregate.
func (s *Store) FinanceAccessInfo(ctx context.Context, id auth.Identity, kind, rid string) (Record, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	scope, err := readFinancialScope(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	result, err := financeAccessInfoTx(ctx, tx, id, scope, kind, rid)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

func prepareFinanceArtifact(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope,
	kind, rid string, sources []FinanceResourceRef,
) (string, error) {
	if !slices.Contains([]string{"attachment", "attachment_derivative", "capture", "import_batch"}, kind) {
		return "", ErrNotFound
	}
	origin := "capture"
	if kind == "import_batch" {
		origin = "import"
	}
	return prepareFinanceOrigin(ctx, tx, id, scope, kind, rid, origin, sources)
}

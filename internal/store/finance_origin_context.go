package store

import (
	"context"
	"slices"
)

type financeOriginContextKey struct{}
type financeInitialAccessContextKey struct{}

type financeInitialAccessContext struct {
	kind, id string
	spec     FinanceInitialAccess
}

func withFinanceInitialAccess(ctx context.Context, kind, rid string, spec FinanceInitialAccess) context.Context {
	copySpec := spec
	copySpec.Grants = append([]FinanceInitialGrant{}, spec.Grants...)
	if spec.ResponsibleMembershipID != nil {
		responsible := *spec.ResponsibleMembershipID
		copySpec.ResponsibleMembershipID = &responsible
	}
	return context.WithValue(ctx, financeInitialAccessContextKey{}, financeInitialAccessContext{kind: kind, id: rid, spec: copySpec})
}

func financeInitialAccessFor(ctx context.Context, kind, rid string) *FinanceInitialAccess {
	initial, ok := ctx.Value(financeInitialAccessContextKey{}).(financeInitialAccessContext)
	if !ok || initial.kind != kind || initial.id != rid {
		return nil
	}
	copySpec := initial.spec
	copySpec.Grants = append([]FinanceInitialGrant{}, initial.spec.Grants...)
	if initial.spec.ResponsibleMembershipID != nil {
		responsible := *initial.spec.ResponsibleMembershipID
		copySpec.ResponsibleMembershipID = &responsible
	}
	return &copySpec
}

type financeOriginContext struct {
	origin  string
	sources []FinanceResourceRef
}

// withFinanceOrigin is only called by trusted producers after resolving their
// actual source. Client bodies can never choose a provenance class or policy.
func withFinanceOrigin(ctx context.Context, origin string, sources []FinanceResourceRef) context.Context {
	previous, _ := ctx.Value(financeOriginContextKey{}).(financeOriginContext)
	union := append([]FinanceResourceRef{}, previous.sources...)
	union = append(union, sources...)
	return context.WithValue(ctx, financeOriginContextKey{}, financeOriginContext{origin: origin, sources: union})
}

func financeOriginFor(ctx context.Context, ownSources []FinanceResourceRef) (string, []FinanceResourceRef) {
	hint, exists := ctx.Value(financeOriginContextKey{}).(financeOriginContext)
	if !exists {
		hint.origin = "manual"
	}
	union := append([]FinanceResourceRef{}, hint.sources...)
	union = append(union, ownSources...)
	slices.SortFunc(union, func(a, b FinanceResourceRef) int {
		if a.Kind != b.Kind {
			if a.Kind < b.Kind {
				return -1
			}
			return 1
		}
		if a.ID != b.ID {
			if a.ID < b.ID {
				return -1
			}
			return 1
		}
		if a.RowNumber != b.RowNumber {
			if a.RowNumber < b.RowNumber {
				return -1
			}
			return 1
		}
		if a.AuditID < b.AuditID {
			return -1
		}
		if a.AuditID > b.AuditID {
			return 1
		}
		return 0
	})
	return hint.origin, slices.Compact(union)
}

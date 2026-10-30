package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func shoppingSnapshotIDs(in ShoppingSnapshotInput) ([]string, error) {
	if in.Generation <= 0 || in.Generation > shoppingSafeInteger || len(in.ListIDs) < 1 || len(in.ListIDs) > 10 {
		return nil, Invalid{"1 bis 10 Einkaufslisten auswählen"}
	}
	seen := map[string]bool{}
	ids := make([]string, 0, len(in.ListIDs))
	for _, raw := range in.ListIDs {
		v := strings.ToLower(raw)
		if !value.ValidUUID(v) || seen[v] {
			return nil, Invalid{"Einkaufslisten eindeutig auswählen"}
		}
		seen[v] = true
		ids = append(ids, v)
	}
	return ids, nil
}

func shoppingSealSnapshot(out *ShoppingSnapshot) error {
	out.Fingerprint = ""
	out.Totals.UTF8Bytes = 0
	raw, err := json.Marshal(out)
	if err != nil {
		return err
	}
	h := sha256.Sum256(raw)
	out.Fingerprint = hex.EncodeToString(h[:])
	for range 8 {
		raw, err = json.Marshal(out)
		if err != nil {
			return err
		}
		if len(raw) > shoppingSnapshotBytes {
			return Invalid{"Vollständige Lesekopie überschreitet 2 MiB"}
		}
		if out.Totals.UTF8Bytes == len(raw) {
			return nil
		}
		out.Totals.UTF8Bytes = len(raw)
	}
	return Invalid{"Lesekopie konnte nicht vollständig bemessen werden"}
}

func (s *Store) ShoppingOfflineSnapshots(ctx context.Context, id auth.Identity, in ShoppingSnapshotInput) (ShoppingSnapshot, error) {
	var out ShoppingSnapshot
	ids, err := shoppingSnapshotIDs(in)
	if err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer shoppingRollback(tx)
	out.Context, err = shoppingScope(ctx, tx, id)
	if err != nil {
		return out, err
	}
	if in.Generation != out.Context.Generation {
		return out, ErrConflict
	}
	if !out.Context.OfflineAllowed {
		return out, ErrForbidden
	}
	out.Schema, out.Complete = 1, true
	out.CapturedAt = out.Context.ServerTime
	out.ExpiresAt = out.CapturedAt.Add(7 * 24 * time.Hour)
	out.Lists = make([]ShoppingSnapshotList, 0, len(ids))
	index := map[string]int{}
	for _, lid := range ids {
		var list ShoppingSnapshotList
		err = tx.QueryRow(ctx, `SELECT id,version::text,name,archived_at IS NOT NULL,is_template FROM shopping.lists WHERE household_id=$1 AND id=$2`, id.HouseholdID, lid).
			Scan(&list.ID, &list.Version, &list.Name, &list.Archived, &list.IsTemplate)
		if err == pgx.ErrNoRows {
			return ShoppingSnapshot{}, ErrNotFound
		}
		if err != nil {
			return ShoppingSnapshot{}, err
		}
		if v, e := shoppingRevision(list.Version); e != nil || v == 0 {
			return ShoppingSnapshot{}, Invalid{"Gespeicherte Listenversion ist ungültig"}
		}
		list.Items = []ShoppingSnapshotItem{}
		index[lid] = len(out.Lists)
		out.Lists = append(out.Lists, list)
	}
	// The sentinel and byte gate precede materialization of arbitrary item notes.
	var count int
	var bytes int64
	err = tx.QueryRow(ctx, `WITH bounded AS (
	 SELECT octet_length(i.name)::bigint+octet_length(i.group_name)+octet_length(i.note)+octet_length(i.availability)
 +coalesce(octet_length(i.quantity::text),0)+coalesce(octet_length(i.unit_code),0)+coalesce(octet_length(u.display_name),0)
 +coalesce(octet_length(p.name),0)+coalesce(octet_length(p.pack_quantity::text||' '||p.pack_unit),0)+coalesce(octet_length(rp.name),0) AS bytes
 FROM shopping.items i
 LEFT JOIN core.memberships m ON m.household_id=i.household_id AND m.id=i.assignee_id AND m.inactive_at IS NULL
 LEFT JOIN core.users u ON u.id=m.user_id AND u.disabled_at IS NULL
 LEFT JOIN shopping.products p ON p.household_id=i.household_id AND p.id=i.product_id
 LEFT JOIN shopping.products rp ON rp.household_id=i.household_id AND rp.id=i.replacement_product_id
 WHERE i.household_id=$1 AND i.list_id=ANY($2::uuid[]) ORDER BY i.list_id,i.sort_order,i.id LIMIT 1001)
 SELECT count(*),coalesce(sum(bytes),0)::bigint FROM bounded`, id.HouseholdID, ids).Scan(&count, &bytes)
	if err != nil {
		return ShoppingSnapshot{}, err
	}
	if count > shoppingSnapshotItems || bytes > shoppingSnapshotBytes {
		return ShoppingSnapshot{}, Invalid{"Vollständige Lesekopie überschreitet die Mengen- oder Bytegrenze"}
	}
	rows, err := tx.Query(ctx, `SELECT i.list_id,i.id,i.version::text,i.name,i.quantity::text,i.unit_code,i.group_name,i.note,
 i.checked_at IS NOT NULL,i.availability,u.display_name,p.name,
 CASE WHEN p.id IS NOT NULL THEN p.pack_quantity::text||' '||p.pack_unit END,rp.name
 FROM shopping.items i
 LEFT JOIN core.memberships m ON m.household_id=i.household_id AND m.id=i.assignee_id AND m.inactive_at IS NULL
 LEFT JOIN core.users u ON u.id=m.user_id AND u.disabled_at IS NULL
 LEFT JOIN shopping.products p ON p.household_id=i.household_id AND p.id=i.product_id
 LEFT JOIN shopping.products rp ON rp.household_id=i.household_id AND rp.id=i.replacement_product_id
 WHERE i.household_id=$1 AND i.list_id=ANY($2::uuid[]) ORDER BY i.list_id,i.sort_order,i.id LIMIT 1001`, id.HouseholdID, ids)
	if err != nil {
		return ShoppingSnapshot{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var lid string
		var item ShoppingSnapshotItem
		if err = rows.Scan(&lid, &item.ID, &item.Version, &item.Name, &item.Quantity, &item.Unit, &item.Group, &item.Note, &item.Checked, &item.Availability, &item.AssigneeLabel, &item.ProductLabel, &item.PackLabel, &item.ReplacementLabel); err != nil {
			return ShoppingSnapshot{}, err
		}
		if v, e := shoppingRevision(item.Version); e != nil || v == 0 {
			return ShoppingSnapshot{}, Invalid{"Gespeicherte Artikelversion ist ungültig"}
		}
		if item.Quantity != nil {
			q, e := value.Decimal(*item.Quantity)
			if e != nil || q.Sign() <= 0 {
				return ShoppingSnapshot{}, Invalid{"Gespeicherte Artikelmenge ist ungültig"}
			}
		}
		idx, ok := index[lid]
		if !ok || out.Totals.Items >= shoppingSnapshotItems {
			return ShoppingSnapshot{}, Invalid{"Lesekopie ist nicht vollständig"}
		}
		out.Lists[idx].Items = append(out.Lists[idx].Items, item)
		out.Totals.Items++
	}
	if err = rows.Err(); err != nil {
		return ShoppingSnapshot{}, err
	}
	rows.Close()
	if out.Totals.Items != count {
		return ShoppingSnapshot{}, ErrConflict
	}
	out.Totals.Lists = len(out.Lists)
	if err = shoppingSealSnapshot(&out); err != nil {
		return ShoppingSnapshot{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return ShoppingSnapshot{}, err
	}
	// RR gives complete content; a fresh independent snapshot gates the response.
	current, err := s.ShoppingExtensionsContext(ctx, id)
	if err != nil {
		return ShoppingSnapshot{}, err
	}
	if !current.OfflineAllowed || current.Generation != out.Context.Generation || current.AuthScope != out.Context.AuthScope || current.SessionScope != out.Context.SessionScope {
		return ShoppingSnapshot{}, ErrForbidden
	}
	return out, nil
}

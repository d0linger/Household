package web

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"household/internal/store"

	"github.com/jackc/pgx/v5"
)

func shoppingProductLabel(product store.Record) string {
	parts := []string{store.String(product["name"])}
	if brand := store.String(product["brand"]); brand != "" {
		parts = append(parts, brand)
	}
	parts = append(parts, formatQuantity(product["pack_quantity"])+" "+formatChoice(product["pack_unit"]))
	return strings.Join(parts, " · ")
}

// The catalog joins are scoped to the same household; previews remain protected
// by the existing download handler even if a source changes after this read.
const shoppingBoardQuery = `SELECT to_jsonb(t)||jsonb_build_object('quantity',t.quantity::text,
 'product',CASE WHEN p.id IS NOT NULL THEN jsonb_build_object('id',p.id,'name',p.name,'brand',p.brand,'quantity',p.pack_quantity::text,'unit',p.pack_unit) END,
 'replacement',CASE WHEN rp.id IS NOT NULL THEN jsonb_build_object('id',rp.id,'name',rp.name,'brand',rp.brand,'quantity',rp.pack_quantity::text,'unit',rp.pack_unit) END,
 'assignee_name',u.display_name,'assignee_inactive',m.inactive_at IS NOT NULL OR u.disabled_at IS NOT NULL,
 'image_id',img.id,'aisle_order',coalesce(a.sort_order,1000000001)),t.created_at,coalesce(a.sort_order,1000000001),lower(t.group_name),lower(t.name)
 FROM shopping.items t JOIN shopping.lists l ON l.household_id=t.household_id AND l.id=t.list_id
 LEFT JOIN shopping.aisles a ON a.household_id=t.household_id AND a.store_id=l.store_id AND lower(a.name)=lower(t.group_name)
 LEFT JOIN shopping.products p ON p.household_id=t.household_id AND p.id=t.product_id
 LEFT JOIN shopping.products rp ON rp.household_id=t.household_id AND rp.id=t.replacement_product_id
 LEFT JOIN core.memberships m ON m.household_id=t.household_id AND m.id=t.assignee_id
 LEFT JOIN core.users u ON u.id=m.user_id
 LEFT JOIN LATERAL (SELECT f.id FROM core.attachments f JOIN core.attachment_derivatives d ON d.attachment_id=f.id AND d.variant='preview'
  WHERE f.household_id=t.household_id AND f.module_id='shopping' AND f.module_generation=$9 AND f.resource_type='products'
  AND f.resource_id=coalesce(t.replacement_product_id,t.product_id) AND f.state='ready' AND f.media_type IN('image/jpeg','image/png')
  ORDER BY f.created_at DESC,f.id DESC LIMIT 1) img ON true
 WHERE t.household_id=$1 AND t.list_id=$2
 AND ($10::uuid IS NULL OR t.id=$10)
 AND ($3::uuid IS NULL OR (coalesce(a.sort_order,1000000001),lower(t.group_name),t.sort_order,lower(t.name),t.id)>($5::bigint,$6::text,$7::bigint,$8::text,$3::uuid))
 AND ($4='' OR strpos(lower(t.name||' '||t.group_name||' '||coalesce(p.name,'')||' '||coalesce(p.brand,'')),lower($4))>0)
 ORDER BY coalesce(a.sort_order,1000000001),lower(t.group_name),t.sort_order,lower(t.name),t.id LIMIT 101`

func (s *Server) shoppingItem(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	state, enabled := s.Store.State("shopping")
	if !enabled {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	m, _ := s.Store.Module("shopping")
	e, _ := m.Entity("items")
	item, err := s.Store.Get(r.Context(), identity(r), m, e, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var raw []byte
	var created time.Time
	var rank int64
	var group, name string
	err = s.Pool.QueryRow(r.Context(), shoppingBoardQuery, identity(r).HouseholdID, item["list_id"], nil, "", 0, "", 0, "", state.Generation, item["id"]).Scan(&raw, &created, &rank, &group, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		err = store.ErrNotFound
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	item, err = store.DecodeRecord(raw)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) shoppingContext(r *http.Request, list store.Record) (store.Record, error) {
	var raw []byte
	err := s.Pool.QueryRow(r.Context(), `WITH priced AS (
 SELECT i.checked_at,i.availability,price.observed_on,
 CASE WHEN i.availability<>'unavailable' THEN round(i.quantity*
  (CASE WHEN i.unit_code='piece' THEN p.pack_quantity*pack.factor_to_base ELSE unit.factor_to_base END)*price.price_minor/
  (price.quantity*CASE WHEN price.unit_code='piece' THEN p.pack_quantity*pack.factor_to_base ELSE price.factor_to_base END)) END AS amount
 FROM shopping.items i JOIN shopping.lists l ON l.household_id=i.household_id AND l.id=i.list_id
 JOIN core.households h ON h.id=i.household_id
 LEFT JOIN shopping.products p ON p.household_id=i.household_id AND p.id=coalesce(i.replacement_product_id,i.product_id)
 LEFT JOIN core.units pack ON pack.code=p.pack_unit LEFT JOIN core.units unit ON unit.code=i.unit_code
 LEFT JOIN LATERAL (SELECT pr.*,pu.factor_to_base FROM shopping.price_observations pr JOIN core.units pu ON pu.code=pr.unit_code
  WHERE pr.household_id=i.household_id AND pr.product_id=p.id AND pr.store_id=l.store_id
  AND pr.observed_on<=(now() AT TIME ZONE h.timezone)::date AND (pr.unit_code='piece' OR pu.dimension=pack.dimension)
  AND (i.unit_code='piece' OR unit.dimension=pack.dimension)
  ORDER BY pr.observed_on DESC,pr.created_at DESC,pr.id DESC LIMIT 1) price ON true
 WHERE i.household_id=$1 AND i.list_id=$2)
 SELECT jsonb_build_object('total',count(*),'completed',count(*) FILTER(WHERE checked_at IS NOT NULL),
 'estimate_minor',coalesce(sum(amount),0)::text,'unknown',count(*) FILTER(WHERE amount IS NULL AND availability<>'unavailable'),
 'unavailable',count(*) FILTER(WHERE availability='unavailable'),
 'oldest_price',min(observed_on) FILTER(WHERE amount IS NOT NULL),'newest_price',max(observed_on) FILTER(WHERE amount IS NOT NULL)) FROM priced`, identity(r).HouseholdID, list["id"]).Scan(&raw)
	if err != nil {
		return nil, err
	}
	context, err := store.DecodeRecord(raw)
	if err != nil {
		return nil, err
	}
	var shop, shopper, until string
	var own bool
	err = s.Pool.QueryRow(r.Context(), `SELECT coalesce(s.name,''),coalesce(u.display_name,''),
 CASE WHEN u.id IS NOT NULL THEN to_char(l.shopper_until AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"') ELSE '' END,coalesce(u.id=$3::uuid,false)
 FROM shopping.lists l LEFT JOIN shopping.stores s ON s.household_id=l.household_id AND s.id=l.store_id
 LEFT JOIN core.memberships m ON m.household_id=l.household_id AND m.id=l.shopper_id AND m.inactive_at IS NULL AND l.shopper_until>now()
 LEFT JOIN core.users u ON u.id=m.user_id AND u.disabled_at IS NULL WHERE l.household_id=$1 AND l.id=$2`, identity(r).HouseholdID, list["id"], identity(r).UserID).Scan(&shop, &shopper, &until, &own)
	if err != nil {
		return nil, err
	}
	context["store_name"], context["shopper_name"], context["shopper_until"], context["shopper_self"] = shop, shopper, until, own
	return context, nil
}

package web

import (
	"household/internal/module"
	"household/internal/store"
	"household/internal/value"
	"net/http"

	"github.com/jackc/pgx/v5"
)

func (s *Server) related(r *http.Request, m module.Descriptor, e module.Entity, rid string) ([]Related, error) {
	if m.ID == "expenses" && (e.ID == "entries" || e.ID == "categories") {
		records, err := s.Store.FinancialRelated(r.Context(), identity(r), e.ID, rid)
		if err != nil {
			return nil, err
		}
		child, _ := m.Entity("entry_lines")
		field := "entry_id"
		if e.ID == "categories" {
			field = "category_id"
		}
		return []Related{{Entity: child, Field: field, Records: records}}, nil
	}
	if m.ID == "recipes" && e.ID == "recipes" {
		bundle, err := s.Store.Recipe(r.Context(), identity(r), rid)
		if err != nil {
			return nil, err
		}
		ingredients, _ := m.Entity("ingredients")
		steps, _ := m.Entity("steps")
		return []Related{{Entity: ingredients, Field: "recipe_id", Records: bundle.Ingredients}, {Entity: steps, Field: "recipe_id", Records: bundle.Steps}}, nil
	}
	groups := []Related{}
	for _, child := range m.Entities {
		for _, field := range child.Fields {
			if field.Reference != m.ID+"/"+e.ID {
				continue
			}
			order := "created_at,id"
			for _, f := range child.Fields {
				if f.Name == "position" {
					order = "position,id"
				}
				if f.Name == "sort_order" {
					order = "sort_order,id"
				}
			}
			rows, err := s.Pool.Query(r.Context(), "SELECT to_jsonb(t) FROM "+pgx.Identifier{m.ID, child.ID}.Sanitize()+" t WHERE household_id=$1 AND "+pgx.Identifier{field.Name}.Sanitize()+"=$2 ORDER BY "+order+" LIMIT 100", identity(r).HouseholdID, rid)
			if err != nil {
				return nil, err
			}
			group := Related{Entity: child, Field: field.Name, Records: []store.Record{}}
			for rows.Next() {
				var raw []byte
				if err = rows.Scan(&raw); err != nil {
					rows.Close()
					return nil, err
				}
				record, err := store.DecodeRecord(raw)
				if err != nil {
					rows.Close()
					return nil, err
				}
				group.Records = append(group.Records, record)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return nil, err
			}
			groups = append(groups, group)
		}
	}
	return groups, nil
}
func (s *Server) reportPage(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("module") == "pantry" {
		s.pantryPage(w, r)
		return
	}
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	m, ok := s.Store.Module(r.PathValue("module"))
	if !ok {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	if _, ok = s.Store.State(m.ID); !ok {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	p := s.page(r, m.Title+" · Auswertung")
	p.View = "report"
	p.Module = m
	s.render(w, r, p)
}
func prefill(r *http.Request, e module.Entity) store.Record {
	record := store.Record{}
	for _, field := range e.Fields {
		if field.Kind == "reference" {
			valueString := r.URL.Query().Get(field.Name)
			if value.ValidUUID(valueString) {
				record[field.Name] = valueString
			}
		}
	}
	return record
}

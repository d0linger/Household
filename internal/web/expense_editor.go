package web

import (
	"errors"
	"net/http"

	"household/internal/expenseedit"
	"household/internal/store"
)

func (s *Server) expenseEditorPage(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, enabled := s.Store.State("expenses"); !enabled {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	bundle, err := s.Store.ExpenseBundle(r.Context(), identity(r), r.URL.Query().Get("entry"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := s.page(r, "Ausgabe gemeinsam aufteilen")
	if bundle.Header.ID == "" {
		p.Title = "Neue Ausgabe aufteilen"
	}
	if !bundle.Editable {
		p.Title = "Buchung und Aufteilung"
	}
	p.Module, _ = s.Store.Module("expenses")
	p.View, p.Extra = "expense-editor", bundle
	s.render(w, r, p)
}

func (s *Server) expenseEditorAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, enabled := s.Store.State("expenses"); !enabled {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	var result any
	var err error
	if r.Method == http.MethodPost {
		var input store.ExpenseEditInput
		if err = readJSON(w, r, &input); err == nil {
			result, err = s.Store.SaveExpense(r.Context(), identity(r), r.Header.Get("Idempotency-Key"), trace(r), input)
		}
	} else {
		result, err = s.Store.ExpenseBundle(r.Context(), identity(r), r.URL.Query().Get("entry"))
	}
	var problems expenseedit.Problems
	if errors.As(err, &problems) {
		writeJSON(w, http.StatusUnprocessableEntity, store.Record{"error": problems.Error(), "fields": problems, "request_id": trace(r)})
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

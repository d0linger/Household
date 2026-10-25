package web

import (
	"net/http"
	"strings"

	"household/internal/store"
	"household/internal/value"
)

func (s *Server) planningPurchasesPage(w http.ResponseWriter, r *http.Request) {
	plan := r.URL.Query().Get("plan")
	if plan != "" && !value.ValidUUID(plan) {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	p := s.page(r, "Einkauf aus der Planung")
	p.View = "planning-purchases"
	p.Record = store.Record{"plan": plan, "week": r.URL.Query().Get("week"), "selected": strings.Join(r.URL.Query()["meal"], ","), "operation": r.URL.Query().Get("operation")}
	s.render(w, r, p)
}

func planningPurchaseSelection(r *http.Request) ([]store.PlanningPurchaseSelection, error) {
	values := r.URL.Query()["source"]
	if len(values) > 42 {
		return nil, store.Invalid{Message: "Höchstens 42 ganze Mahlzeiten auswählen"}
	}
	selected := make([]store.PlanningPurchaseSelection, 0, len(values))
	for _, value := range values {
		plan, meal, found := strings.Cut(value, "/")
		if !found {
			return nil, store.Invalid{Message: "Plan und ganze Mahlzeit auswählen"}
		}
		selected = append(selected, store.PlanningPurchaseSelection{PlanID: plan, MealIDs: []string{meal}})
	}
	return selected, nil
}

func (s *Server) planningPurchasesAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	var result any
	var err error
	switch {
	case r.PathValue("list") != "":
		result, err = s.Store.PlanningPurchaseDestination(r.Context(), identity(r), r.PathValue("list"))
	case r.PathValue("operation") != "":
		result, err = s.Store.PlanningPurchaseOperation(r.Context(), identity(r), r.PathValue("operation"))
	case r.URL.Path == "/api/v1/planning-purchases/scope":
		result, err = s.Store.PlanningPurchaseScope(r.Context(), identity(r))
	case r.Method == http.MethodGet:
		var selected []store.PlanningPurchaseSelection
		if selected, err = planningPurchaseSelection(r); err == nil {
			var decisions []store.PlanningPurchaseDecision
			values := r.URL.Query()["decision"]
			if len(values) > 500 {
				err = store.Invalid{Message: "Höchstens 500 ausdrückliche Entscheidungen auswählen"}
			} else {
				for _, choice := range values {
					identifier, action, _ := strings.Cut(choice, "/")
					decision := store.PlanningPurchaseDecision{RequirementID: identifier, Action: action}
					if action == "reactivate" {
						decision.RequirementID, decision.MealID = "", identifier
					}
					decisions = append(decisions, decision)
				}
				result, err = s.Store.ReviewPlanningPurchases(r.Context(), identity(r), selected, decisions)
			}
		}
	case r.URL.Path == "/api/v1/planning-purchases/control":
		var input store.PlanningPurchaseControlInput
		if err = readJSON(w, r, &input); err == nil {
			result, err = s.Store.ControlPlanningPurchases(r.Context(), identity(r), r.Header.Get("Idempotency-Key"), trace(r), input)
		}
	default:
		var input store.PlanningPurchaseInput
		if err = readJSON(w, r, &input); err == nil {
			result, err = s.Store.SyncPlanningPurchases(r.Context(), identity(r), r.Header.Get("Idempotency-Key"), trace(r), input)
		}
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

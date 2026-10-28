package web

import (
	"net/http"
	"net/url"
)

func (s *Server) fuelModuleFactPage(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	vehicle, pane, err := s.Store.FuelModuleFactVehicle(r.Context(), identity(r), r.PathValue("entity"), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	values := url.Values{"vehicle": {vehicle}, "entity": {r.PathValue("entity")}, "record": {r.PathValue("id")}}
	http.Redirect(w, r, "/m/fuel/workspace/"+pane+"?"+values.Encode(), http.StatusSeeOther)
}

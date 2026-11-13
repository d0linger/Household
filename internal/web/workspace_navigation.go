package web

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"household/internal/store"
	"household/internal/value"
)

// Workspace navigation separates destinations from actions and configuration.
// Links never grant authority; their existing handlers retain all access checks.
type WorkspaceLink struct {
	Label, Href, Pane, ModuleID string
	Active                      bool
	Primary                     bool
}
type WorkspaceSection struct {
	Label, Href string
	Panes       []string
	Links       []WorkspaceLink
}
type WorkspaceSpec struct {
	Sections        []WorkspaceSection
	Actions, Manage []WorkspaceLink
}
type WorkspaceNavigation struct {
	ModuleID                                         string
	ManageHref                                       string
	Primary, Context, Actions, Manage, Data, Parents []WorkspaceLink
	ManageActive                                     bool
}

func workspaceLink(label, href string) WorkspaceLink {
	pane := ""
	if _, after, ok := strings.Cut(href, "/workspace/"); ok && !strings.Contains(after, "/") {
		pane = after
	}
	return WorkspaceLink{Label: label, Href: href, Pane: pane}
}

func workspaceSpec(mid string) WorkspaceSpec {
	if spec := operationalWorkspaceSpec(mid); len(spec.Sections) != 0 {
		return spec
	}
	l := workspaceLink
	switch mid {
	case "shopping":
		return WorkspaceSpec{Sections: []WorkspaceSection{
			{Label: "Listen", Href: "/m/shopping/lists", Links: []WorkspaceLink{l("Aktive Listen", "/m/shopping/lists"), l("Vorlagen", "/m/shopping/lists?q=is:template"), l("Archiv", "/m/shopping/lists?q=is:archived")}},
			{Label: "Produkte", Href: "/m/shopping/products", Links: []WorkspaceLink{l("Alle Produkte", "/m/shopping/products"), l("Favoriten", "/m/shopping/products?q=is:favorite")}},
		}}
	case "recipes":
		return WorkspaceSpec{Sections: []WorkspaceSection{
			{Label: "Rezepte", Href: "/m/recipes/recipes", Links: []WorkspaceLink{l("Rezeptbuch", "/m/recipes/recipes"), l("Rezepte finden", "/m/recipes/find"), l("Offline lesen", "/offline/recipes")}},
			{Label: "Kochen", Href: "/m/recipes/cooking"},
		}, Actions: []WorkspaceLink{l("Import prüfen", "/m/recipes/import-review")}}
	case "pantry":
		return WorkspaceSpec{Sections: []WorkspaceSection{
			{Label: "Bestand", Href: "/m/pantry/stock", Links: []WorkspaceLink{l("Bestand", "/m/pantry/stock"), l("Gekochte Reste", "/m/pantry/leftovers"), l("Bestandsbewegungen", "/m/pantry/stock_movements")}},
			{Label: "Zuerst verbrauchen", Href: "/m/pantry/priority", Links: []WorkspaceLink{l("Zuerst verbrauchen", "/m/pantry/priority"), l("Rezeptvorschläge", "/m/pantry/recipe-suggestions"), l("Abfallanalyse", "/m/pantry/waste")}},
		}, Actions: []WorkspaceLink{l("Einlagern / Inventur", "/m/pantry/batches"), l("Scanentwürfe", "/m/pantry/scans")}, Manage: []WorkspaceLink{l("Produktinformationen", "/m/pantry/food-facts"), l("Packungen", "/m/pantry/packs"), l("Barcodes", "/m/pantry/barcodes")}}
	case "expenses":
		return WorkspaceSpec{Sections: []WorkspaceSection{
			{Label: "Buchungen", Href: "/m/expenses/workspace/entries", Panes: []string{"overview", "entries", "accounts", "import"}, Links: []WorkspaceLink{l("Buchungen", "/m/expenses/workspace/entries"), l("Konten", "/m/expenses/workspace/accounts"), l("Übersicht", "/m/expenses/workspace/overview"), l("Belege und Import", "/m/expenses/workspace/import")}},
			{Label: "Planung", Href: "/m/expenses/workspace/planning", Panes: []string{"planning"}},
			{Label: "Berichte", Href: "/m/expenses/workspace/reports", Panes: []string{"reports"}},
		}, Manage: []WorkspaceLink{l("Personen", "/m/expenses/workspace/people")}}
	case "documents":
		return WorkspaceSpec{Sections: []WorkspaceSection{
			{Label: "Eingang", Href: "/m/documents/workspace/inbox", Panes: []string{"inbox", "processing"}, Links: []WorkspaceLink{l("Eingang", "/m/documents/workspace/inbox"), l("Verarbeitung", "/m/documents/workspace/processing")}},
			{Label: "Bibliothek", Href: "/m/documents/workspace/library", Panes: []string{"library", "review", "duplicates"}, Links: []WorkspaceLink{l("Bibliothek", "/m/documents/workspace/library"), l("Dubletten", "/m/documents/workspace/duplicates")}},
			{Label: "Fristen", Href: "/m/documents/workspace/deadlines", Panes: []string{"deadlines"}},
		}, Manage: []WorkspaceLink{l("Regeln", "/m/documents/workspace/rules"), l("Eingänge", "/m/documents/workspace/integrations")}}
	case "mealplan":
		return WorkspaceSpec{Sections: []WorkspaceSection{{Label: "Pläne und Vorlagen", Href: "/m/mealplan/plans"}, {Label: "Mahlzeiten", Href: "/m/mealplan/meals"}}}
	}
	return WorkspaceSpec{}
}

func workspaceNavigation(r *http.Request, p Page) WorkspaceNavigation {
	n := WorkspaceNavigation{ModuleID: p.Module.ID, ManageHref: "/m/" + p.Module.ID + "/manage", ManageActive: p.View == "workspace-manage"}
	spec := workspaceSpec(p.Module.ID)
	path := r.URL.Path
	selectionPath := path
	if p.View == "detail" && workspaceChildEntity(p.Module.ID, p.Entity.ID) {
		for _, field := range p.Entity.Fields {
			if strings.HasPrefix(field.Reference, p.Module.ID+"/") && value.ValidUUID(store.String(p.Record[field.Name])) {
				selectionPath = "/m/" + field.Reference
				break
			}
		}
	}
	pane := ""
	if _, tail, ok := strings.Cut(path, "/workspace/"); ok {
		pane = strings.Split(tail, "/")[0]
	}
	active := -1
	for i, section := range spec.Sections {
		match := selectionPath == section.Href || strings.HasPrefix(selectionPath, section.Href+"/")
		for _, candidate := range section.Panes {
			match = match || pane == candidate
		}
		for _, link := range section.Links {
			u, _ := url.Parse(link.Href)
			match = match || path == u.Path
		}
		if match {
			active = i
		}
	}
	if active < 0 {
		active, n.ManageActive = workspaceParentLocation(p, path, n.ManageActive)
	}
	for i, section := range spec.Sections {
		link := workspaceLink(section.Label, section.Href)
		link.Active = i == active
		link.Primary = true
		n.Primary = append(n.Primary, link)
		if link.Active {
			for _, child := range section.Links {
				if p.Module.ID == "pantry" && child.Href == "/m/pantry/leftovers" && !p.User.CanWrite() {
					continue
				}
				u, _ := url.Parse(child.Href)
				child.Active = path == u.Path && r.URL.Query().Get("q") == u.Query().Get("q")
				n.Context = append(n.Context, child)
			}
		}
	}
	if p.User.CanWrite() {
		n.Actions = spec.Actions
	}
	for _, link := range spec.Manage {
		if p.Module.ID == "pantry" && link.Href == "/m/pantry/food-facts" && !p.User.CanWrite() {
			continue
		}
		if p.Module.ID == "documents" && link.Pane == "integrations" && p.User.Role != "admin" {
			continue
		}
		n.Manage = append(n.Manage, link)
		n.ManageActive = n.ManageActive || path == link.Href
	}
	if n.ManageActive {
		for i := range n.Primary {
			n.Primary[i].Active = false
		}
		n.Context = nil
	}
	if n.ManageActive && !strings.HasSuffix(path, "/manage") {
		for _, link := range n.Manage {
			link.Active = path == link.Href
			n.Context = append(n.Context, link)
		}
	}
	// A selected workspace record is carried only as its module's own validated
	// selector. Record/deep-link parameters are deliberately not copied globally.
	contextKey := map[string]string{"fuel": "vehicle", "meters": "register"}[p.Module.ID]
	if contextKey != "" && value.ValidUUID(r.URL.Query().Get(contextKey)) {
		n.ManageHref += "?" + contextKey + "=" + url.QueryEscape(r.URL.Query().Get(contextKey))
		for _, links := range [][]WorkspaceLink{n.Primary, n.Context, n.Actions, n.Manage} {
			for i := range links {
				links[i].Href += "?" + contextKey + "=" + url.QueryEscape(r.URL.Query().Get(contextKey))
			}
		}
	}
	for _, entity := range p.Module.Entities {
		// Child records belong to their parent details; their old URLs remain valid.
		if workspaceChildEntity(p.Module.ID, entity.ID) {
			continue
		}
		href := "/m/" + p.Module.ID + "/" + entity.ID
		primary := false
		for _, link := range n.Primary {
			primary = primary || link.Href == href
		}
		for _, link := range n.Manage {
			primary = primary || link.Href == href
		}
		if !primary {
			n.Data = append(n.Data, workspaceLink(entity.Title, href))
		}
	}
	if p.Module.ID == "mealplan" && p.View == "planning" && value.ValidUUID(store.String(p.Record["id"])) {
		n.Primary = []WorkspaceLink{{Label: "Pläne und Vorlagen", Href: "/m/mealplan/plans", Primary: true}, {Label: "Woche", Href: r.URL.RequestURI(), Active: true, Primary: true}}
	}
	if p.View == "detail" {
		if p.Module.ID == "mealplan" && p.Entity.ID == "plans" && value.ValidUUID(store.String(p.Record["id"])) {
			href := "/m/mealplan/workspace/" + store.String(p.Record["id"])
			if day := r.URL.Query().Get("week"); day != "" {
				if _, err := time.Parse(time.DateOnly, day); err == nil {
					href += "?week=" + url.QueryEscape(day)
				}
			}
			n.Parents = append(n.Parents, workspaceLink("Zur Wochenplanung", href))
		}
		for _, field := range p.Entity.Fields {
			if !strings.HasPrefix(field.Reference, p.Module.ID+"/") {
				continue
			}
			id := store.String(p.Record[field.Name])
			if value.ValidUUID(id) {
				href := "/m/" + field.Reference + "/" + id
				if field.Reference == "recipes/recipes" {
					href += "/workspace"
				}
				if field.Reference == "mealplan/plans" {
					href = "/m/mealplan/workspace/" + id
					if p.Entity.ID == "meals" && value.ValidUUID(store.String(p.Record["id"])) {
						query := url.Values{"meal": {store.String(p.Record["id"])}}
						if day := store.String(p.Record["planned_on"]); day != "" {
							if _, err := time.Parse(time.DateOnly, day); err == nil {
								query.Set("week", day)
							}
						}
						href += "?" + query.Encode()
					}
				}
				n.Parents = append(n.Parents, workspaceLink(field.Label, href))
			}
		}
	}
	for _, links := range [][]WorkspaceLink{n.Primary, n.Context, n.Actions, n.Manage, n.Data} {
		for i := range links {
			links[i].ModuleID = p.Module.ID
		}
	}
	return n
}

// Legacy entity routes and focused editors still belong to a visible section.
// The mapping is compiled product navigation, never inferred from user data.
func workspaceParentLocation(p Page, path string, managing bool) (int, bool) {
	if managing {
		return -1, true
	}
	if p.Module.ID == "recipes" {
		if strings.HasPrefix(path, "/m/recipes/cook/") {
			return 1, false
		}
		if path == "/m/recipes/new" || path == "/m/recipes/import-review" {
			return 0, false
		}
	}
	if p.Module.ID == "pantry" {
		if path == "/m/pantry/reports" {
			return 0, false
		}
		for _, prefix := range []string{"/m/pantry/consume/", "/m/pantry/replenish/", "/m/pantry/batches", "/m/pantry/scans"} {
			if strings.HasPrefix(path, prefix) {
				return 0, false
			}
		}
		if strings.HasSuffix(path, "/waste") {
			return 1, false
		}
	}
	if p.Module.ID == "expenses" && path == "/m/expenses/editor" {
		return 0, false
	}
	if p.Module.ID == "chores" && strings.HasPrefix(path, "/m/chores/workspace/work/") {
		return 0, false
	}
	if p.Module.ID == "fuel" && path == "/m/fuel/overview" {
		return 0, false
	}
	if strings.HasSuffix(path, "/reports") {
		if p.Module.ID == "fuel" || p.Module.ID == "meters" || p.Module.ID == "expenses" {
			return 2, false
		}
	}
	sections := map[string]map[string]int{
		"shopping":  {"lists": 0, "items": 0, "products": 1, "price_observations": 1},
		"recipes":   {"recipes": 0, "ingredients": 0, "steps": 0},
		"pantry":    {"products": 0, "lots": 0, "stock_movements": 0},
		"expenses":  {"entries": 0, "entry_lines": 0},
		"fuel":      {"vehicles": 0, "odometer_series": 0, "fillups": 1},
		"meters":    {"meters": 0, "devices": 0, "readings": 1},
		"chores":    {"task_templates": 1, "task_occurrences": 0},
		"documents": {"documents": 1},
		"mealplan":  {"plans": 0, "meals": 1},
	}
	if section, ok := sections[p.Module.ID][p.Entity.ID]; ok {
		return section, false
	}
	return -1, p.Entity.ID != ""
}

func workspaceChildEntity(mid, entity string) bool {
	children := map[string]string{
		"shopping": " items aisles price_observations ", "recipes": " ingredients steps recipe_tags ingredient_aliases ",
		"pantry": " lots stock_movements ", "expenses": " entry_lines ",
		"mealplan": " meal_components ",
	}
	return strings.Contains(children[mid], " "+entity+" ")
}

func (s *Server) workspaceManage(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	m, ok := s.Store.Module(r.PathValue("module"))
	if !ok {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	if _, enabled := s.Store.State(m.ID); !enabled {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	p := s.page(r, "Verwalten")
	p.Module, p.View = m, "workspace-manage"
	s.render(w, r, p)
}

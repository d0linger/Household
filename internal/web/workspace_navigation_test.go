package web

import (
	"bytes"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"household/internal/auth"
	"household/internal/module"
	"household/internal/store"
)

func TestWorkspaceNavigationPrimaryAndContext(t *testing.T) {
	for _, tc := range []struct {
		module, path, primary string
		context               int
	}{
		{"shopping", "/m/shopping/lists?q=is:archived", "/m/shopping/lists", 3},
		{"recipes", "/m/recipes/find", "/m/recipes/recipes", 3},
		{"pantry", "/m/pantry/waste", "/m/pantry/priority", 3},
		{"expenses", "/m/expenses/workspace/accounts", "/m/expenses/workspace/entries", 4},
		{"documents", "/m/documents/workspace/duplicates", "/m/documents/workspace/library", 2},
		{"mealplan", "/m/mealplan/plans", "/m/mealplan/plans", 0},
	} {
		t.Run(tc.module, func(t *testing.T) {
			p := Page{Module: module.Descriptor{ID: tc.module}, User: auth.Identity{Role: "editor"}}
			n := workspaceNavigation(httptest.NewRequest("GET", tc.path, nil), p)
			if len(n.Primary) < 2 || len(n.Primary) > 3 || len(n.Context) != tc.context {
				t.Fatalf("unexpected hierarchy: %+v", n)
			}
			active := 0
			for _, link := range n.Primary {
				if link.Active {
					active++
					if link.Href != tc.primary {
						t.Errorf("active %s, want %s", link.Href, tc.primary)
					}
				}
			}
			if active != 1 {
				t.Errorf("want exactly one primary, got %d", active)
			}
		})
	}
}

func TestShoppingListDetailKeepsPrimaryNavigationWithoutCollectionFilters(t *testing.T) {
	for _, role := range []string{"reader", "editor", "admin"} {
		p := Page{View: "detail", Module: module.Descriptor{ID: "shopping"}, Entity: module.Entity{ID: "lists"}, User: auth.Identity{Role: role}}
		n := workspaceNavigation(httptest.NewRequest("GET", "/m/shopping/lists/02e532e4-6c04-4e12-8f31-86fe8072202b", nil), p)
		if len(n.Context) != 0 || len(n.Primary) != 2 || !n.Primary[0].Active || n.Primary[0].Href != "/m/shopping/lists" || n.Primary[1].Href != "/m/shopping/products" || n.ManageHref != "/m/shopping/manage" {
			t.Fatalf("%s list detail lost primary navigation or retained collection filters: %+v", role, n)
		}
		p.View = "module"
		n = workspaceNavigation(httptest.NewRequest("GET", "/m/shopping/lists?q=is:archived", nil), p)
		if len(n.Context) != 3 || n.Context[0].Href != "/m/shopping/lists" || n.Context[1].Href != "/m/shopping/lists?q=is:template" || n.Context[2].Href != "/m/shopping/lists?q=is:archived" || !n.Context[2].Active {
			t.Fatalf("%s collection filters or archive selection changed: %+v", role, n.Context)
		}
	}
}

func TestWorkspaceNavigationScopesAndRoles(t *testing.T) {
	const vehicle = "02e532e4-6c04-4e12-8f31-86fe8072202b"
	p := Page{Module: module.Descriptor{ID: "fuel"}, User: auth.Identity{Role: "editor"}}
	n := workspaceNavigation(httptest.NewRequest("GET", "/m/fuel/workspace/history?vehicle="+vehicle+"&entity=fillups&record="+vehicle, nil), p)
	for _, links := range [][]WorkspaceLink{n.Primary, n.Context, n.Actions, n.Manage} {
		for _, link := range links {
			if !strings.Contains(link.Href, "vehicle="+vehicle) || strings.Contains(link.Href, "record=") {
				t.Errorf("lost vehicle or propagated unrelated record: %s", link.Href)
			}
		}
	}
	if !strings.Contains(n.ManageHref, "vehicle="+vehicle) {
		t.Fatal("manage loses vehicle")
	}
	n = workspaceNavigation(httptest.NewRequest("GET", "/m/fuel/workspace/history?vehicle=https://outside.invalid", nil), p)
	for _, link := range n.Primary {
		if strings.Contains(link.Href, "?") {
			t.Fatal("invalid selector propagated")
		}
	}
	p = Page{Module: module.Descriptor{ID: "documents"}, User: auth.Identity{Role: "reader"}}
	n = workspaceNavigation(httptest.NewRequest("GET", "/m/documents/manage", nil), p)
	if len(n.Actions) != 0 {
		t.Fatal("reader sees mutation actions")
	}
	for _, link := range n.Manage {
		if link.Pane == "integrations" {
			t.Fatal("reader sees admin integration entry")
		}
	}
}

func TestWorkspaceChildLivesAtAuthorizedParent(t *testing.T) {
	const id = "02e532e4-6c04-4e12-8f31-86fe8072202b"
	p := Page{View: "detail", Module: module.Descriptor{ID: "shopping", Entities: []module.Entity{{ID: "items"}, {ID: "stores", Title: "Geschäfte"}}}, Entity: module.Entity{ID: "items", Fields: []module.Field{{Name: "list_id", Label: "Liste", Reference: "shopping/lists"}}}, Record: store.Record{"list_id": id}}
	n := workspaceNavigation(httptest.NewRequest("GET", "/m/shopping/items/"+id, nil), p)
	if len(n.Parents) != 1 || n.Parents[0].Href != "/m/shopping/lists/"+id {
		t.Fatalf("missing parent %+v", n.Parents)
	}
	if len(n.Data) != 1 || n.Data[0].Href != "/m/shopping/stores" {
		t.Fatalf("child escaped to management %+v", n.Data)
	}
}

func TestWorkspaceTemplateRetainsPaneHooksAndEscapes(t *testing.T) {
	source, err := os.ReadFile("templates/workspace-navigation.html")
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := template.New("workspace").Funcs(template.FuncMap{"t": func(s string) string { return s }}).Parse(string(source))
	if err != nil {
		t.Fatal(err)
	}
	p := Page{Module: module.Descriptor{ID: "fuel"}, Workspace: WorkspaceNavigation{ModuleID: "fuel", ManageHref: "/m/fuel/manage", Primary: []WorkspaceLink{{Label: "<script>bad</script>", Href: "/m/fuel/workspace/overview", Pane: "overview", ModuleID: "fuel", Primary: true, Active: true}}}}
	var out bytes.Buffer
	if err := tmpl.ExecuteTemplate(&out, "workspace-nav", p); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`data-pane="overview"`, `data-workspace-primary`, `aria-current="page"`, `&lt;script&gt;bad&lt;/script&gt;`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %s", want)
		}
	}
}

func TestWorkspaceFocusedRecordsRetainTheirParentSection(t *testing.T) {
	for _, tc := range []struct {
		mid, entity, path string
		section           int
		manage            bool
	}{
		{"fuel", "vehicles", "/m/fuel/vehicles/record", 0, false},
		{"pantry", "products", "/m/pantry/products/record", 0, false},
		{"shopping", "stores", "/m/shopping/stores", -1, true},
		{"recipes", "ingredient_catalog", "/m/recipes/ingredient_catalog", -1, true},
		{"recipes", "", "/m/recipes/new", 0, false},
		{"recipes", "", "/m/recipes/cook/record", 1, false},
		{"pantry", "", "/m/pantry/consume/record", 0, false},
		{"pantry", "", "/m/pantry/reports", 0, false},
		{"expenses", "entries", "/m/expenses/editor", 0, false},
		{"chores", "", "/m/chores/workspace/work/record", 0, false},
		{"meters", "readings", "/m/meters/readings/record", 1, false},
	} {
		t.Run(tc.mid+tc.path, func(t *testing.T) {
			p := Page{Module: module.Descriptor{ID: tc.mid}, Entity: module.Entity{ID: tc.entity}}
			n := workspaceNavigation(httptest.NewRequest("GET", tc.path, nil), p)
			if n.ManageActive != tc.manage {
				t.Fatalf("manage=%v want%v", n.ManageActive, tc.manage)
			}
			for i, link := range n.Primary {
				if link.Active != (i == tc.section) {
					t.Errorf("primary%d active%v want%v", i, link.Active, i == tc.section)
				}
			}
		})
	}
}

func TestWorkspaceManagementRejectsUnavailableModulesBeforeDataAccess(t *testing.T) {
	s := &Server{Store: store.New(nil, []module.Descriptor{{ID: "shopping"}})}
	for _, mid := range []string{"shopping", "unknown"} {
		r := httptest.NewRequest(http.MethodGet, "/m/"+mid+"/manage", nil)
		r.SetPathValue("module", mid)
		r.Header.Set("Accept", "application/json")
		w := httptest.NewRecorder()
		s.workspaceManage(w, r)
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s status%d", mid, w.Code)
		}
	}
}

func TestWorkspaceReadOnlyToolsStayReachable(t *testing.T) {
	p := Page{Module: module.Descriptor{ID: "expenses"}, User: auth.Identity{Role: "reader"}}
	n := workspaceNavigation(httptest.NewRequest("GET", "/m/expenses/workspace/entries", nil), p)
	found := false
	for _, link := range n.Context {
		found = found || link.Pane == "import"
	}
	if !found {
		t.Fatal("authorized receipt reads hidden behind write action")
	}
	p.Module.ID = "pantry"
	n = workspaceNavigation(httptest.NewRequest("GET", "/m/pantry/manage", nil), p)
	for _, link := range n.Manage {
		if link.Href == "/m/pantry/food-facts" {
			t.Fatal("reader offered writer-only product information tool")
		}
	}
	n = workspaceNavigation(httptest.NewRequest("GET", "/m/pantry/stock", nil), p)
	for _, link := range n.Context {
		if link.Href == "/m/pantry/leftovers" {
			t.Fatal("reader offered writer-only leftovers workflow")
		}
	}
	p.User.Role = "editor"
	p.Entity.ID = "products"
	n = workspaceNavigation(httptest.NewRequest("GET", "/m/pantry/food-facts", nil), p)
	if !n.ManageActive {
		t.Fatal("product information must remain in Manage")
	}
	for _, link := range n.Primary {
		if link.Active {
			t.Fatal("configuration page also marks stock current")
		}
	}
}

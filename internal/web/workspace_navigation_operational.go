package web

func operationalWorkspaceSpec(moduleID string) WorkspaceSpec {
	switch moduleID {
	case "fuel":
		return WorkspaceSpec{
			Sections: []WorkspaceSection{
				{Label: "Übersicht", Href: "/m/fuel/workspace/overview", Panes: []string{"overview", "maintenance", "documents", "odometer"}, Links: []WorkspaceLink{
					{Label: "Fahrzeuge", Href: "/m/fuel/workspace/overview", Pane: "overview"},
					{Label: "Wartung", Href: "/m/fuel/workspace/maintenance", Pane: "maintenance"},
					{Label: "Dokumente", Href: "/m/fuel/workspace/documents", Pane: "documents"},
					{Label: "Kilometerzähler", Href: "/m/fuel/workspace/odometer", Pane: "odometer"},
				}},
				{Label: "Verlauf", Href: "/m/fuel/workspace/history", Panes: []string{"history", "charging", "trips", "capture"}, Links: []WorkspaceLink{
					{Label: "Tankhistorie", Href: "/m/fuel/workspace/history", Pane: "history"},
					{Label: "Ladevorgänge", Href: "/m/fuel/workspace/charging", Pane: "charging"},
					{Label: "Fahrten", Href: "/m/fuel/workspace/trips", Pane: "trips"},
				}},
				{Label: "Auswertung", Href: "/m/fuel/workspace/reports", Panes: []string{"reports", "costs"}, Links: []WorkspaceLink{
					{Label: "Verbrauch", Href: "/m/fuel/workspace/reports", Pane: "reports"},
					{Label: "Kosten", Href: "/m/fuel/workspace/costs", Pane: "costs"},
				}},
			},
			Actions: []WorkspaceLink{{Label: "Tankung erfassen", Href: "/m/fuel/workspace/capture", Pane: "capture"}},
			Manage: []WorkspaceLink{
				{Label: "Vorgaben", Href: "/m/fuel/workspace/settings", Pane: "settings"},
				{Label: "CSV-Import", Href: "/m/fuel/workspace/imports", Pane: "imports"},
				{Label: "Bisherige Übersicht", Href: "/m/fuel/overview"},
			},
		}
	case "chores":
		return WorkspaceSpec{
			Sections: []WorkspaceSection{
				{Label: "Heute", Href: "/m/chores/workspace/today", Panes: []string{"today", "work"}},
				{Label: "Tagesplan", Href: "/m/chores/workspace/plan", Panes: []string{"plan", "areas", "series"}, Links: []WorkspaceLink{
					{Label: "Tagesplan", Href: "/m/chores/workspace/plan", Pane: "plan"},
					{Label: "Bereiche", Href: "/m/chores/workspace/areas", Pane: "areas"},
					{Label: "Serien", Href: "/m/chores/workspace/series", Pane: "series"},
				}},
				{Label: "Verlauf", Href: "/m/chores/workspace/history", Panes: []string{"history"}},
			},
			Manage: []WorkspaceLink{{Label: "Einstellungen", Href: "/m/chores/workspace/settings", Pane: "settings"}},
		}
	case "meters":
		return WorkspaceSpec{
			Sections: []WorkspaceSection{
				{Label: "Übersicht", Href: "/m/meters/workspace/overview", Panes: []string{"overview", "plans"}, Links: []WorkspaceLink{
					{Label: "Ableseplan", Href: "/m/meters/workspace/plans", Pane: "plans"},
				}},
				{Label: "Verlauf", Href: "/m/meters/workspace/history", Panes: []string{"history", "capture"}},
				{Label: "Kosten", Href: "/m/meters/workspace/costs", Panes: []string{"costs"}},
			},
			Actions: []WorkspaceLink{{Label: "Ablesen", Href: "/m/meters/workspace/capture", Pane: "capture"}},
			Manage: []WorkspaceLink{
				{Label: "Einrichten", Href: "/m/meters/workspace/settings", Pane: "settings"},
				{Label: "Import", Href: "/m/meters/workspace/imports", Pane: "imports"},
			},
		}
	default:
		return WorkspaceSpec{}
	}
}

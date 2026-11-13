package web

import (
	"net/url"
	"testing"
)

func TestOperationalVehicleWorkspaceDestinations(t *testing.T) {
	spec := operationalWorkspaceSpec("fuel")
	if len(spec.Sections) != 3 || len(spec.Actions) != 1 || len(spec.Manage) != 3 {
		t.Fatal("vehicle navigation must separate three primary sections, capture and management")
	}
	seen := make(map[string]bool)
	check := func(link WorkspaceLink) {
		t.Helper()
		if link.Href == "/m/fuel/overview" && link.Pane == "" {
			return
		}
		parsed, err := url.Parse(link.Href)
		if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.Path != "/m/fuel/workspace/"+link.Pane || parsed.RawQuery != "" {
			t.Fatalf("invalid compiled destination: %+v", link)
		}
		seen[link.Pane] = true
	}
	for _, section := range spec.Sections {
		if len(section.Links) > 4 {
			t.Fatal("context group exceeds four destinations")
		}
		for _, link := range section.Links {
			check(link)
		}
	}
	for _, link := range append(spec.Actions, spec.Manage...) {
		check(link)
	}
	for _, pane := range fuelModulePanes {
		if !seen[pane] {
			t.Fatalf("existing vehicle area lost: %s", pane)
		}
	}
}

func TestOperationalChoreWorkspaceDestinations(t *testing.T) {
	spec := operationalWorkspaceSpec("chores")
	if len(spec.Sections) != 3 || len(spec.Manage) != 1 {
		t.Fatal("task navigation must separate today, planning and history from settings")
	}
	seen := make(map[string]bool)
	for _, section := range spec.Sections {
		for _, pane := range section.Panes {
			seen[pane] = true
		}
		if len(section.Links) > 4 {
			t.Fatal("too many planning context links")
		}
	}
	for _, link := range spec.Manage {
		seen[link.Pane] = true
	}
	for _, pane := range append(chorePanes, "work") {
		if !seen[pane] {
			t.Fatalf("existing task context lost: %s", pane)
		}
	}
	if spec.Sections[1].Links[1].Pane != "areas" || spec.Sections[1].Links[2].Pane != "series" {
		t.Fatal("areas and series belong to planning")
	}
}

func TestOperationalMeterWorkspaceDestinations(t *testing.T) {
	spec := operationalWorkspaceSpec("meters")
	if len(spec.Sections) != 3 || len(spec.Actions) != 1 || len(spec.Manage) != 2 {
		t.Fatal("meter navigation must separate overview, history and costs from capture and management")
	}
	seen := make(map[string]bool)
	for _, section := range spec.Sections {
		for _, pane := range section.Panes {
			seen[pane] = true
		}
		if len(section.Links) > 4 {
			t.Fatal("meter context group exceeds four destinations")
		}
	}
	for _, link := range append(spec.Actions, spec.Manage...) {
		parsed, err := url.Parse(link.Href)
		if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.Path != "/m/meters/workspace/"+link.Pane || parsed.RawQuery != "" {
			t.Fatalf("invalid meter destination: %+v", link)
		}
		seen[link.Pane] = true
	}
	for _, pane := range energyPanes {
		if !seen[pane] {
			t.Fatalf("existing meter area lost: %s", pane)
		}
	}
	if spec.Sections[0].Links[0].Pane != "plans" || spec.Actions[0].Pane != "capture" {
		t.Fatal("reading plan belongs to Overview and capture is the sole direct action")
	}
}

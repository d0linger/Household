// Package modules is the single composition point for compiled product modules.
package modules

import (
	"household/internal/module"
	"household/internal/modules/chores"
	"household/internal/modules/documents"
	"household/internal/modules/expenses"
	"household/internal/modules/fuel"
	"household/internal/modules/mealplan"
	"household/internal/modules/meters"
	"household/internal/modules/pantry"
	"household/internal/modules/recipes"
	"household/internal/modules/shopping"
	"sort"
)

func Registry() []module.Descriptor {
	list := []module.Descriptor{shopping.Module, fuel.Module, recipes.Module, pantry.Module, expenses.Module, chores.Module, meters.Module, documents.Module, mealplan.Module}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Order == list[j].Order {
			return list[i].ID < list[j].ID
		}
		return list[i].Order < list[j].Order
	})
	return list
}

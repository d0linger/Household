package web

import (
	"net/http"

	"household/internal/module"
)

const moduleOrderKey requestKey = 5

func requestModuleOrder(r *http.Request) []string {
	order, _ := r.Context().Value(moduleOrderKey).([]string)
	return order
}

func moduleIDs(descriptors []module.Descriptor) []string {
	ids := make([]string, len(descriptors))
	for index, descriptor := range descriptors {
		ids[index] = descriptor.ID
	}
	return ids
}

func validModuleOrder(order []string, descriptors []module.Descriptor) bool {
	if len(order) > len(descriptors) {
		return false
	}
	known := make(map[string]bool, len(descriptors))
	for _, descriptor := range descriptors {
		known[descriptor.ID] = true
	}
	for _, id := range order {
		if !known[id] {
			return false
		}
		known[id] = false
	}
	return true
}

// orderedModules copies the registry before ordering. Missing/new modules stay
// reachable in registry order, and temporarily disabled modules keep their place.
func orderedModules(descriptors []module.Descriptor, order []string) []module.Descriptor {
	byID := make(map[string]module.Descriptor, len(descriptors))
	for _, descriptor := range descriptors {
		byID[descriptor.ID] = descriptor
	}
	result := make([]module.Descriptor, 0, len(descriptors))
	for _, id := range order {
		if descriptor, ok := byID[id]; ok {
			result = append(result, descriptor)
			delete(byID, id)
		}
	}
	for _, descriptor := range descriptors {
		if _, ok := byID[descriptor.ID]; ok {
			result = append(result, descriptor)
		}
	}
	return result
}

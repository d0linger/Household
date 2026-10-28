package store

import "encoding/json"

// FuelModuleCommand describes one explicit, version-bound vehicle operation.
type FuelModuleCommand struct {
	legacyHash        []byte
	Action            string                     `json:"action"`
	Generation        int64                      `json:"generation"`
	VehicleID         string                     `json:"vehicle_id,omitempty"`
	RecordID          string                     `json:"record_id,omitempty"`
	Version           int64                      `json:"version,omitempty"`
	SourceStamp       string                     `json:"source_stamp,omitempty"`
	TargetGenerations map[string]int64           `json:"target_generations,omitempty"`
	Data              map[string]json.RawMessage `json:"data"`
}

// FuelModuleQuery selects a bounded module workspace or explicit export.
type FuelModuleQuery struct {
	Pane      string
	VehicleID string
	JobID     string
	From      string
	To        string
	Search    string
	Cursor    string
	Archived  bool
	Unit      string
	Kind      string
	Limit     int
}

// FuelModuleContext is the authenticated context of a module response.
type FuelModuleContext struct {
	AuthScope  string `json:"auth_scope"`
	Generation int64  `json:"generation"`
	Timezone   string `json:"timezone"`
	Currency   string `json:"currency_code"`
	Today      string `json:"today"`
	CanWrite   bool   `json:"can_write"`
	Stamp      string `json:"stamp"`
}

// FuelModuleWarning explains a limitation without changing original facts.
type FuelModuleWarning struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	NextAction string `json:"next_action,omitempty"`
	RecordID   string `json:"record_id,omitempty"`
}

// FuelModuleView contains bounded rows and explicitly scoped totals.
type FuelModuleView struct {
	Context  FuelModuleContext   `json:"context"`
	Pane     string              `json:"pane"`
	Records  []Record            `json:"records"`
	Totals   Record              `json:"totals,omitempty"`
	Options  Record              `json:"options,omitempty"`
	Warnings []FuelModuleWarning `json:"warnings"`
	Next     string              `json:"next_cursor,omitempty"`
	More     bool                `json:"more"`
}

// FuelModulePreview binds normalized input to the source state shown for review.
type FuelModulePreview struct {
	Context  FuelModuleContext   `json:"context"`
	Command  FuelModuleCommand   `json:"command"`
	Before   Record              `json:"before,omitempty"`
	After    Record              `json:"after,omitempty"`
	Effects  []Record            `json:"effects"`
	Warnings []FuelModuleWarning `json:"warnings"`
}

// FuelModuleResult is a historical accepted receipt, not a current-state claim.
type FuelModuleResult struct {
	OperationID string `json:"operation_id"`
	Action      string `json:"action"`
	AcceptedAt  string `json:"accepted_at"`
	RecordID    string `json:"record_id,omitempty"`
	Version     int64  `json:"version,omitempty"`
	Message     string `json:"message"`
	Data        Record `json:"data,omitempty"`
}

type FuelModuleDetail struct {
	Context FuelModuleContext `json:"context"`
	Record  Record            `json:"record"`
}

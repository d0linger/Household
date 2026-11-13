package store

import (
	"math/big"
	"sort"
	"strings"
	"time"

	"household/internal/value"
)

const FuelOverviewPageSize = 20
const fuelOverviewHistoryLimit = 10000
const fuelOverviewSeriesLimit = 1000

type FuelOverviewScope struct {
	Generation int64     `json:"generation"`
	Timezone   string    `json:"timezone"`
	Today      string    `json:"today"`
	Now        time.Time `json:"now"`
}

type FuelOverviewQuery struct {
	VehicleID string    `json:"vehicle_id,omitempty"`
	Search    string    `json:"search"`
	Archived  bool      `json:"archived"`
	After     time.Time `json:"-"`
	AfterID   string    `json:"-"`
}

type FuelOverviewFill struct {
	ID                 string `json:"id"`
	VehicleID          string `json:"vehicle_id"`
	SeriesID           string `json:"series_id"`
	Date               string `json:"date"`
	Odometer           string `json:"odometer"`
	Quantity           string `json:"quantity"`
	Fuel               string `json:"fuel"`
	Unit               string `json:"unit"`
	Full               bool   `json:"full"`
	Missing            bool   `json:"missing"`
	DaySeriesAmbiguous bool   `json:"-"`
}

type FuelOverviewSeries struct {
	ID        string  `json:"id"`
	VehicleID string  `json:"vehicle_id"`
	Started   string  `json:"started"`
	Ended     *string `json:"ended"`
	Initial   string  `json:"initial"`
	Label     string  `json:"label"`
}

type FuelOverviewInterval struct {
	From           string `json:"from"`
	To             string `json:"to"`
	FromID         string `json:"from_id"`
	ToID           string `json:"to_id"`
	Distance       string `json:"distance"`
	Quantity       string `json:"quantity"`
	Per100         string `json:"per_100"`
	Unit           string `json:"unit"`
	Fuel           string `json:"fuel"`
	Historical     bool   `json:"historical"`
	BelowPrecision bool   `json:"below_precision"`
}

type FuelOverviewDue struct {
	Kind  string `json:"kind"`
	Date  string `json:"date"`
	State string `json:"state"`
	Days  int    `json:"days"`
}

type FuelOverviewVehicle struct {
	ID                    string                `json:"id"`
	Name                  string                `json:"name"`
	Registration          string                `json:"registration"`
	RegistrationTruncated bool                  `json:"registration_truncated"`
	Archived              bool                  `json:"archived"`
	Version               int64                 `json:"version"`
	Created               time.Time             `json:"created"`
	NextService           *string               `json:"next_service_on"`
	NextInspection        *string               `json:"next_inspection_on"`
	InsuranceRenewal      *string               `json:"insurance_renewal_on"`
	ResponsibleMemberID   *string               `json:"responsible_member_id"`
	Dates                 []FuelOverviewDue     `json:"dates"`
	FutureFills           bool                  `json:"future_fills"`
	Last                  *FuelOverviewFill     `json:"last"`
	LastState             string                `json:"last_state"`
	LastDay               string                `json:"last_day"`
	Series                *FuelOverviewSeries   `json:"series"`
	CurrentSeriesID       *string               `json:"current_series_id"`
	SeriesSelection       string                `json:"series_selection"`
	SeriesState           string                `json:"series_state"`
	Odometer              *string               `json:"odometer"`
	OdometerSource        string                `json:"odometer_source"`
	OdometerReason        string                `json:"odometer_reason"`
	OdometerDate          string                `json:"odometer_date"`
	Consumption           *FuelOverviewInterval `json:"consumption"`
	ConsumptionState      string                `json:"consumption_state"`
}

type FuelOverviewPage struct {
	Context        FuelOverviewScope     `json:"context"`
	Query          FuelOverviewQuery     `json:"query"`
	Vehicles       []FuelOverviewVehicle `json:"vehicles"`
	HistoryLimited bool                  `json:"history_limited"`
	SeriesLimited  bool                  `json:"series_limited"`
	More           bool                  `json:"-"`
}

func fuelOverviewDates(v *FuelOverviewVehicle, today string) error {
	current, err := time.Parse(time.DateOnly, today)
	if err != nil || !value.Date(today) {
		return Invalid{"Haushaltsdatum erneut laden"}
	}
	v.Dates = []FuelOverviewDue{}
	for _, entry := range []struct {
		kind string
		date *string
	}{{"service", v.NextService}, {"inspection", v.NextInspection}, {"insurance", v.InsuranceRenewal}} {
		if entry.date == nil {
			continue
		}
		date, err := time.Parse(time.DateOnly, *entry.date)
		if err != nil || !value.Date(*entry.date) {
			return Invalid{"Gespeicherten Fahrzeugtermin prüfen"}
		}
		days := int((date.Unix() - current.Unix()) / 86400)
		state := "upcoming"
		if days < 0 {
			state = "overdue"
		} else if days == 0 {
			state = "today"
		}
		v.Dates = append(v.Dates, FuelOverviewDue{Kind: entry.kind, Date: *entry.date, State: state, Days: days})
	}
	sort.Slice(v.Dates, func(i, j int) bool {
		if v.Dates[i].Date == v.Dates[j].Date {
			return v.Dates[i].Kind < v.Dates[j].Kind
		}
		return v.Dates[i].Date < v.Dates[j].Date
	})
	return nil
}

func fuelCurrentSeries(series []FuelOverviewSeries, today string) (*FuelOverviewSeries, string, error) {
	var current *FuelOverviewSeries
	for _, row := range series {
		initial, err := value.Decimal(row.Initial)
		if err != nil || initial.Sign() < 0 || !value.ValidUUID(row.ID) || !value.Date(row.Started) || row.Ended != nil && (!value.Date(*row.Ended) || *row.Ended < row.Started) {
			return nil, "invalid_series", Invalid{"Gespeicherte Kilometerstandsreihe prüfen"}
		}
		if row.Started > today || row.Ended != nil && *row.Ended < today {
			continue
		}
		if current != nil {
			return nil, "overlapping_series", nil
		}
		copy := row
		current = &copy
	}
	if current == nil {
		return nil, "no_current_series", nil
	}
	return current, "current", nil
}

func fuelLatestFill(rows []FuelOverviewFill) (*FuelOverviewFill, string, string, error) {
	if len(rows) == 0 {
		return nil, "no_fillups", "", nil
	}
	rows = append([]FuelOverviewFill(nil), rows...)
	for _, r := range rows {
		n, err := value.Decimal(r.Odometer)
		if err != nil || n.Sign() < 0 || !value.Date(r.Date) {
			return nil, "invalid_history", "", Invalid{"Gespeicherten Kilometerstand prüfen"}
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Date != rows[j].Date {
			return rows[i].Date > rows[j].Date
		}
		a, _ := value.Decimal(rows[i].Odometer)
		b, _ := value.Decimal(rows[j].Odometer)
		if a.Cmp(b) != 0 {
			return a.Cmp(b) > 0
		}
		return rows[i].ID > rows[j].ID
	})
	if rows[0].DaySeriesAmbiguous {
		return nil, "ambiguous_latest_series", rows[0].Date, nil
	}
	if len(rows) > 1 && rows[0].Date == rows[1].Date {
		if rows[0].SeriesID != rows[1].SeriesID {
			return nil, "ambiguous_latest_series", rows[0].Date, nil
		}
		a, _ := value.Decimal(rows[0].Odometer)
		b, _ := value.Decimal(rows[1].Odometer)
		if a.Cmp(b) == 0 {
			return nil, "ambiguous_latest", rows[0].Date, nil
		}
	}
	return &rows[0], "available", rows[0].Date, nil
}

func fuelExplainedConsumption(rows []FuelOverviewFill, series FuelOverviewSeries) (*FuelOverviewInterval, string, error) {
	if len(rows) > fuelOverviewHistoryLimit {
		return nil, "history_limit", nil
	}
	if len(rows) == 0 {
		return nil, "no_fillups", nil
	}
	rows = append([]FuelOverviewFill(nil), rows...)
	initial, err := value.Decimal(series.Initial)
	if err != nil {
		return nil, "invalid_history", Invalid{"Anfangsstand prüfen"}
	}
	for _, r := range rows {
		reading, e1 := value.Decimal(r.Odometer)
		quantity, e2 := value.Decimal(r.Quantity)
		if e1 != nil || e2 != nil || reading.Cmp(initial) < 0 || quantity.Sign() <= 0 || r.SeriesID != series.ID || r.VehicleID != series.VehicleID || !value.Date(r.Date) || r.Date < series.Started || series.Ended != nil && r.Date > *series.Ended {
			return nil, "invalid_history", Invalid{"Gespeicherte Tankhistorie ist nicht eindeutig auswertbar"}
		}
		supported := (r.Fuel == "diesel" || r.Fuel == "petrol" || r.Fuel == "lpg") && r.Unit == "l" || (r.Fuel == "cng" || r.Fuel == "hydrogen") && r.Unit == "kg"
		if !supported {
			return nil, "unsupported_unit", nil
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Date != rows[j].Date {
			return rows[i].Date < rows[j].Date
		}
		a, _ := value.Decimal(rows[i].Odometer)
		b, _ := value.Decimal(rows[j].Odometer)
		if a.Cmp(b) != 0 {
			return a.Cmp(b) < 0
		}
		return rows[i].ID < rows[j].ID
	})
	for i := 1; i < len(rows); i++ {
		a, _ := value.Decimal(rows[i-1].Odometer)
		b, _ := value.Decimal(rows[i].Odometer)
		if b.Cmp(a) < 0 {
			return nil, "invalid_history", Invalid{"Kilometerstand fällt innerhalb derselben Reihe"}
		}
	}
	var anchor *FuelOverviewFill
	var interval *FuelOverviewInterval
	total := new(big.Rat)
	state := "no_full_anchor"
	lastFuel := ""
	for i := 0; i < len(rows); i++ {
		row := rows[i]
		same := i
		for same+1 < len(rows) && rows[same+1].Date == row.Date {
			a, _ := value.Decimal(row.Odometer)
			b, _ := value.Decimal(rows[same+1].Odometer)
			if a.Cmp(b) != 0 {
				break
			}
			same++
		}
		if same > i {
			anchor = nil
			total.SetInt64(0)
			state = "ambiguous_order"
			lastFuel = ""
			i = same
			continue
		}
		reset := ""
		if row.Missing {
			reset = "missing_fill"
		} else if lastFuel != "" && lastFuel != row.Fuel {
			reset = "fuel_changed"
		}
		lastFuel = row.Fuel
		if reset != "" {
			anchor = nil
			total.SetInt64(0)
			state = reset
		}
		quantity, _ := value.Decimal(row.Quantity)
		if anchor != nil {
			total.Add(total, quantity)
		}
		if !row.Full {
			if anchor != nil {
				state = "needs_next_full"
			}
			continue
		}
		if anchor != nil {
			reading, _ := value.Decimal(row.Odometer)
			base, _ := value.Decimal(anchor.Odometer)
			distance := new(big.Rat).Sub(reading, base)
			if distance.Sign() > 0 {
				ratio := new(big.Rat).Mul(new(big.Rat).Quo(total, distance), big.NewRat(100, 1))
				interval = &FuelOverviewInterval{From: anchor.Date, To: row.Date, FromID: anchor.ID, ToID: row.ID, Distance: distance.FloatString(6), Quantity: total.FloatString(6), Per100: ratio.FloatString(2), Unit: row.Unit + "/100 km", Fuel: row.Fuel}
				interval.BelowPrecision = ratio.Sign() > 0 && ratio.Cmp(big.NewRat(1, 100)) < 0
				state = "available"
			} else {
				state = "zero_distance"
			}
		} else if reset == "" && state != "zero_distance" {
			state = "needs_next_full"
		}
		copy := row
		anchor = &copy
		total.SetInt64(0)
	}
	if interval != nil {
		interval.Historical = interval.ToID != rows[len(rows)-1].ID
	}
	return interval, state, nil
}

func normalizeFuelOverviewQuery(q FuelOverviewQuery) (FuelOverviewQuery, error) {
	q.Search = strings.TrimSpace(q.Search)
	if len(q.Search) > 300 || strings.ContainsRune(q.Search, '\x00') || q.VehicleID != "" && !value.ValidUUID(q.VehicleID) || q.AfterID != "" && (!value.ValidUUID(q.AfterID) || q.After.IsZero()) {
		return q, Invalid{"Fahrzeugauswahl erneut laden"}
	}
	return q, nil
}

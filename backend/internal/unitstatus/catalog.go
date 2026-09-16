// Package unitstatus recognizes offline status evidence and proposes associations.
// It never establishes or changes operational state.
package unitstatus

import "greenwich-fire-responder/backend/internal/unitrecognition"

type Status string

const (
	Dispatched        Status = "DISPATCHED"
	Enroute           Status = "ENROUTE"
	Onscene           Status = "ONSCENE"
	Quarters          Status = "QUARTERS"
	InService         Status = "IN SERVICE"
	OutOfService      Status = "OUT OF SERVICE"
	OnAir             Status = "ON AIR"
	Training          Status = "TRAINING"
	EnrouteToQuarters Status = "ENROUTE TO QUARTERS"
)

type phrase struct {
	text   string
	status Status
}

func phrases() []phrase {
	return []phrase{
		{"dispatched", Dispatched}, {"responding", Enroute}, {"en route", Enroute},
		{"in route", Enroute}, {"enroute", Enroute}, {"on the way", Enroute},
		{"on scene", Onscene}, {"on location", Onscene}, {"arrived", Onscene}, {"10-23", Onscene},
		{"returning", EnrouteToQuarters}, {"returning to quarters", EnrouteToQuarters},
		{"RTQ", EnrouteToQuarters}, {"back to quarters", Quarters}, {"available", Quarters},
		{"in service", InService}, {"back in service", InService}, {"out of service", OutOfService},
		{"on air", OnAir}, {"training", Training},
		// CLEAR is retained as evidence without a canonical mapping.
		{"clear", ""},
	}
}

// Matcher is immutable after New and safe for concurrent calls. Use New;
// the zero value has no validated unit catalog.
type Matcher struct{ units []unitrecognition.Unit }

func New() (*Matcher, error) {
	m, err := unitrecognition.New()
	if err != nil {
		return nil, err
	}
	return &Matcher{units: m.Catalog()}, nil
}

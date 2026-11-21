// Package fleetrow holds the fleet row as an application value type: ports.Fleets persists it and
// the use cases read and write it inside the unit of work. It lives outside ports because
// internal/application/ports holds interfaces (and sentinel errors) only.
package fleetrow

import (
	"github.com/IQVO/amr-fleet-control/internal/domain/capacity"
	"github.com/IQVO/amr-fleet-control/internal/domain/fleetmap"
)

// Record is the fleet row: the map version its reservations belong to, the reservations and
// waits, and the last published capacity snapshot.
type Record struct {
	MapVersion   string
	Reservations fleetmap.State
	Capacity     capacity.Snapshot
	HasCapacity  bool
}

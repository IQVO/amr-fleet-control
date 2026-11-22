package vda5050

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/application/vda"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// ErrInvalidMessage is a payload that is not a VDA 5050 v3 message of a valid robot.
var ErrInvalidMessage = errors.New("invalid VDA 5050 message")

// Headers keeps the headerId counters, one per topic and robot (VDA 5050 §6.2).
type Headers struct {
	mu   sync.Mutex
	next map[string]uint32
}

func NewHeaders() *Headers { return &Headers{next: map[string]uint32{}} }

// Next returns the next header of topic for id, stamped at (amr-fleet-control's clock).
func (h *Headers) Next(topic string, id robot.ID, at time.Time) Header {
	h.mu.Lock()
	defer h.mu.Unlock()
	k := topic + "|" + id.Key()
	n := h.next[k]
	h.next[k] = n + 1
	return Header{HeaderID: n, Timestamp: at.UTC().Format(TimeLayout), Version: Version,
		Manufacturer: id.Manufacturer, SerialNumber: id.SerialNumber}
}

// EncodeOrder renders an order; every node carries nodePosition and no edge carries actions.
func EncodeOrder(o vda.Order, h Header) ([]byte, error) {
	w := Order{Header: h, OrderID: o.OrderID, OrderUpdateID: o.UpdateID, Nodes: []Node{}, Edges: []Edge{}}
	for _, n := range o.Nodes {
		w.Nodes = append(w.Nodes, Node{NodeID: n.ID, SequenceID: n.Seq, Released: n.Released,
			NodePosition: &NodePosition{X: n.X, Y: n.Y, MapID: n.MapID}, Actions: actions(n.Actions)})
	}
	for _, e := range o.Edges {
		we := Edge{EdgeID: e.ID, SequenceID: e.Seq, Released: e.Released, StartNodeID: e.Start, EndNodeID: e.End, Actions: []Action{}}
		if e.MaxSpeed > 0 {
			v := e.MaxSpeed
			we.MaxSpeed = &v
		}
		w.Edges = append(w.Edges, we)
	}
	return json.Marshal(w)
}

// EncodeInstantActions renders an instantActions message.
func EncodeInstantActions(a vda.InstantActions, h Header) ([]byte, error) {
	return json.Marshal(InstantActions{Header: h, Actions: actions(a.Actions)})
}

func actions(in []vda.Action) []Action {
	out := []Action{}
	for _, a := range in {
		wa := Action{ActionType: a.Type, ActionID: a.ID, BlockingType: a.Blocking}
		keys := make([]string, 0, len(a.Params))
		for k := range a.Params {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			wa.ActionParameters = append(wa.ActionParameters, ActionParameter{Key: k, Value: a.Params[k]})
		}
		out = append(out, wa)
	}
	return out
}

// ParseTopic reads vda5050/v3/<manufacturer>/<serialNumber>/<name>.
func ParseTopic(topic string) (robot.ID, string, bool) {
	p := strings.Split(topic, "/")
	if len(p) != 5 || p[0] != InterfaceName || p[1] != MajorVersion || p[2] == "" || p[3] == "" || p[4] == "" {
		return robot.ID{}, "", false
	}
	return robot.ID{Manufacturer: p[2], SerialNumber: p[3]}, p[4], true
}

func header(h Header) (robot.ID, time.Time, error) {
	if h.Version != Version || h.Manufacturer == "" || h.SerialNumber == "" {
		return robot.ID{}, time.Time{}, fmt.Errorf("%w: version %q, robot %q/%q", ErrInvalidMessage, h.Version, h.Manufacturer, h.SerialNumber)
	}
	at, err := time.Parse(TimeLayout, h.Timestamp)
	if err != nil {
		return robot.ID{}, time.Time{}, fmt.Errorf("%w: timestamp %q", ErrInvalidMessage, h.Timestamp)
	}
	return robot.ID{Manufacturer: h.Manufacturer, SerialNumber: h.SerialNumber}, at.UTC(), nil
}

// DecodeState translates a state message into a robot.Report (decision D19: a full snapshot).
func DecodeState(b []byte) (vda.State, error) {
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return vda.State{}, fmt.Errorf("%w: %v", ErrInvalidMessage, err)
	}
	id, at, err := header(s.Header)
	if err != nil {
		return vda.State{}, err
	}
	rep := robot.Report{At: at, OrderID: s.OrderID, OrderUpdateID: s.OrderUpdateID, LastNodeID: s.LastNodeID,
		LastNodeSeq: s.LastNodeSequenceID, Driving: s.Driving, Pending: len(s.NodeStates)+len(s.EdgeStates) > 0,
		Battery: min(max(s.PowerSupply.StateOfCharge, 0), 100), Charging: s.PowerSupply.Charging, // carry (Task 13 review): the contracts publish 0-100
		Actions: states(s.ActionStates), InstantActions: states(s.InstantActionStates)}
	if p := s.MobileRobotPosition; p != nil {
		rep.X, rep.Y, rep.MapID = p.X, p.Y, p.MapID
	}
	for _, l := range s.Loads {
		rep.LoadIDs = append(rep.LoadIDs, l.LoadID)
	}
	for _, e := range s.Errors {
		rep.Errors = append(rep.Errors, robot.Error{Type: e.ErrorType, Level: e.ErrorLevel})
	}
	return vda.State{Robot: id, Report: rep}, nil
}

func states(in []ActionState) []robot.ActionState {
	var out []robot.ActionState
	for _, a := range in {
		out = append(out, robot.ActionState{ID: a.ActionID, Type: a.ActionType, Status: a.ActionStatus})
	}
	return out
}

// DecodeConnection translates a connection message.
func DecodeConnection(b []byte) (vda.Connection, error) {
	var c Connection
	if err := json.Unmarshal(b, &c); err != nil {
		return vda.Connection{}, fmt.Errorf("%w: %v", ErrInvalidMessage, err)
	}
	id, at, err := header(c.Header)
	if err != nil {
		return vda.Connection{}, err
	}
	state, err := robot.ParseConnection(c.ConnectionState)
	if err != nil {
		return vda.Connection{}, fmt.Errorf("%w: %v", ErrInvalidMessage, err)
	}
	return vda.Connection{Robot: id, State: state, At: at}, nil
}

// DecodeFactsheet keeps the factsheet subset fleet control uses.
func DecodeFactsheet(b []byte) (vda.Factsheet, error) {
	var f Factsheet
	if err := json.Unmarshal(b, &f); err != nil {
		return vda.Factsheet{}, fmt.Errorf("%w: %v", ErrInvalidMessage, err)
	}
	id, _, err := header(f.Header)
	if err != nil {
		return vda.Factsheet{}, err
	}
	return vda.Factsheet{Robot: id, Factsheet: robot.Factsheet{SeriesName: f.TypeSpecification.SeriesName,
		MaxSpeed: f.PhysicalParameters.MaximumSpeed, MaxLoadMass: f.TypeSpecification.MaximumLoadMass}}, nil
}

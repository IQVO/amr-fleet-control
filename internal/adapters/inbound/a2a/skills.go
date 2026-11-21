package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

const (
	tracerName        = "github.com/IQVO/amr-fleet-control/internal/adapters/inbound/a2a"
	contentTypeJSON   = "application/json"
	skillGetFleet     = "get_fleet"
	skillListOpenLegs = "list_open_legs"
	skillGetLeg       = "get_leg"
	skillGetRobot     = "get_robot"
)

type skillRequest struct {
	Skill     string          `json:"skill"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type args struct {
	FleetID      string `json:"fleetId"`
	LegID        string `json:"legId,omitempty"`
	Manufacturer string `json:"manufacturer,omitempty"`
	SerialNumber string `json:"serialNumber,omitempty"`
}

func answer(ctx context.Context, deps Deps, ec *a2asrv.ExecutorContext) a2a.Event {
	req, p := parse(ec)
	if p != nil {
		return reply(ec, envelope{Problem: p}, p.summary())
	}
	ctx, span := otel.Tracer(tracerName).Start(ctx, "a2a.skill "+req.Skill)
	defer span.End()
	span.SetAttributes(attribute.String("a2a.skill", req.Skill))
	data, summary, p := run(ctx, deps, req)
	if p != nil {
		span.SetStatus(codes.Error, p.Detail)
		return reply(ec, envelope{Skill: req.Skill, Problem: p}, p.summary())
	}
	return reply(ec, envelope{Skill: req.Skill, Data: data}, summary)
}

func run(ctx context.Context, d Deps, req skillRequest) (map[string]any, string, *problem) {
	// deviation (Task 19 D2, brief bug): an id the card does not offer is unknown-skill BEFORE the arguments are
	// read; the brief checked fleetId first, so {"skill":"request_leg","arguments":{}} answered invalid-argument.
	if !offered(req.Skill) {
		return nil, "", problemUnknownSkill(req.Skill)
	}
	var a args
	if err := decodeStrict(req.Arguments, &a); err != nil { // carry (plan 04 Task 21 lesson): EOF + exact keys
		return nil, "", problemInvalidArgument(fmt.Sprintf("arguments: %v", err))
	}
	if a.FleetID == "" {
		return nil, "", problemInvalidArgument("fleetId is required")
	}
	switch req.Skill {
	case skillGetFleet:
		v, err := d.Reads.Fleet(ctx, a.FleetID)
		if err != nil {
			return nil, "", problemFromUseCase(ctx, err)
		}
		return map[string]any{"fleet": fleetDTO(v)}, fmt.Sprintf("Fleet %s on map %s version %s: %d robots, %d available, %d legs queued.",
			v.FleetID, v.MapID, v.MapVersion, v.Capacity.RobotsTotal, v.Capacity.RobotsAvailable, v.Capacity.LegsQueued), nil
	case skillListOpenLegs:
		ls, err := d.Reads.OpenLegs(ctx, a.FleetID)
		if err != nil {
			return nil, "", problemFromUseCase(ctx, err)
		}
		out := make([]map[string]any, 0, len(ls))
		for _, l := range ls {
			out = append(out, legDTO(l))
		}
		return map[string]any{"legs": out}, fmt.Sprintf("%d open legs.", len(out)), nil
	case skillGetLeg:
		return getLeg(ctx, d, a)
	case skillGetRobot:
		return getRobot(ctx, d, a)
	default:
		return nil, "", problemUnknownSkill(req.Skill)
	}
}

// offered reports whether the card lists skill id.
func offered(id string) bool {
	switch id {
	case skillGetFleet, skillListOpenLegs, skillGetLeg, skillGetRobot:
		return true
	default:
		return false
	}
}

func getLeg(ctx context.Context, d Deps, a args) (map[string]any, string, *problem) {
	if a.LegID == "" {
		return nil, "", problemInvalidArgument("legId is required")
	}
	v, err := d.Reads.Leg(ctx, a.FleetID, a.LegID)
	if err != nil {
		return nil, "", problemFromUseCase(ctx, err)
	}
	return map[string]any{"leg": legDTO(v)}, fmt.Sprintf("Leg %s (%s, %s to %s) is %s.", v.LegID, v.ContainerID, v.From, v.To, v.Status), nil
}

func getRobot(ctx context.Context, d Deps, a args) (map[string]any, string, *problem) {
	if a.Manufacturer == "" || a.SerialNumber == "" {
		return nil, "", problemInvalidArgument("manufacturer and serialNumber are required")
	}
	v, err := d.Reads.Robot(ctx, a.FleetID, robot.ID{Manufacturer: a.Manufacturer, SerialNumber: a.SerialNumber})
	if err != nil {
		return nil, "", problemFromUseCase(ctx, err)
	}
	return map[string]any{"robot": robotDTO(v)}, fmt.Sprintf("Robot %s is %s at %.0f%%.", v.ID.Key(), v.State, v.Battery), nil
}

func parse(ec *a2asrv.ExecutorContext) (skillRequest, *problem) {
	if ec.Message == nil || len(ec.Message.Parts) == 0 {
		return skillRequest{}, problemMalformed("the message carries no parts")
	}
	var raw []byte
	switch c := ec.Message.Parts[0].Content.(type) {
	case a2a.Data:
		raw, _ = json.Marshal(c.Value)
	case a2a.Text:
		raw = []byte(string(c))
	default:
		return skillRequest{}, problemMalformed("the first part is neither data nor text")
	}
	var req skillRequest
	if err := decodeStrict(raw, &req); err != nil { // carry (plan 04 Task 21 lesson): EOF + exact keys
		return skillRequest{}, problemMalformed(fmt.Sprintf("the skill request is not valid: %v", err))
	}
	if req.Skill == "" {
		return skillRequest{}, problemMalformed(`the skill request carries no "skill" field`)
	}
	return req, nil
}

func reply(ec *a2asrv.ExecutorContext, env envelope, summary string) a2a.Event {
	return a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, a2a.NewDataPart(env), a2a.NewTextPart(summary))
}

func stamp(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func legDTO(v usecases.LegView) map[string]any {
	out := map[string]any{"legId": v.LegID, "fleetId": v.FleetID, "containerId": v.ContainerID, "from": v.From, "to": v.To,
		"status": v.Status, "mapVersion": v.MapVersion, "acceptedAt": stamp(v.AcceptedAt), "reason": v.Reason, "robotError": v.RobotError}
	if !v.Robot.IsZero() {
		out["robot"] = v.Robot.Key()
	}
	return out
}

func robotDTO(v usecases.RobotView) map[string]any {
	return map[string]any{"robot": v.ID.Key(), "connection": v.Connection, "state": v.State, "batteryPercent": v.Battery,
		"charging": v.Charging, "available": v.Available, "nodeId": v.Node, "missionId": v.MissionID, "errorTypes": v.ErrorTypes,
		"lastSeenAt": stamp(v.LastSeenAt)}
}

func fleetDTO(v usecases.FleetView) map[string]any {
	robots := make([]map[string]any, 0, len(v.Robots))
	for _, r := range v.Robots {
		robots = append(robots, robotDTO(r))
	}
	c := v.Capacity
	return map[string]any{"fleetId": v.FleetID, "mapId": v.MapID, "mapVersion": v.MapVersion, "handoffPoints": v.HandoffPoints,
		"robots": robots, "capacity": map[string]any{"robotsTotal": c.RobotsTotal, "robotsOnline": c.RobotsOnline,
			"robotsAvailable": c.RobotsAvailable, "robotsCharging": c.RobotsCharging, "robotsHeld": c.RobotsHeld,
			"legsQueued": c.LegsQueued, "legsActive": c.LegsActive}}
}

package mcp

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// Reads is the read side the tools need (usecases.Service built by NewReader).
type Reads interface {
	Fleet(ctx context.Context, fleetID string) (usecases.FleetView, error)
	Leg(ctx context.Context, fleetID, legID string) (usecases.LegView, error)
	OpenLegs(ctx context.Context, fleetID string) ([]usecases.LegView, error)
	Robot(ctx context.Context, fleetID string, id robot.ID) (usecases.RobotView, error)
}

// Deps are the adapter's dependencies; FleetID names the fleet in the prompt.
type Deps struct {
	Reads   Reads
	FleetID string
}

type fleetInput struct {
	FleetID string `json:"fleetId" jsonschema:"the fleet id, e.g. AMR1"`
}

type legInput struct {
	FleetID string `json:"fleetId" jsonschema:"the fleet id, e.g. AMR1"`
	LegID   string `json:"legId" jsonschema:"the leg id, e.g. leg-0f3a91"`
}

type robotInput struct {
	FleetID      string `json:"fleetId" jsonschema:"the fleet id, e.g. AMR1"`
	Manufacturer string `json:"manufacturer" jsonschema:"the robot's VDA 5050 manufacturer, e.g. EMU"`
	SerialNumber string `json:"serialNumber" jsonschema:"the robot's VDA 5050 serial number, e.g. R1"`
}

// registerTools declares every tool as a literal (the scaffold's mcp_authority fitness test reads the
// Annotations and the Meta statically: ReadOnlyHint must be the literal true, the level a constant).
func (d Deps) registerTools(server *mcp.Server) {
	closedWorld := false
	addTool(server, &mcp.Tool{Name: "get_fleet", Meta: authorityMeta(authorityL1),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Description: "Read the fleet: map id and version, the handoff points (AS/RS ports and stations), every robot with its " +
			"connection, PackML state (IDLE, EXECUTE, SUSPENDED = charging, HELD = a blocking error, STOPPED = offline or stale), " +
			"battery, node and errors, and the capacity counts. Read-only; an unknown fleet fails with fleet-not-found."}, d.getFleet)
	addTool(server, &mcp.Tool{Name: "list_open_legs", Meta: authorityMeta(authorityL1),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Description: "List the fleet's open legs (QUEUED, ASSIGNED, PICKED_UP, CANCEL_REQUESTED) in acceptance order, each the " +
			"same shape as get_leg. Internal charge and park missions are not legs. Read-only."}, d.listOpenLegs)
	addTool(server, &mcp.Tool{Name: "get_leg", Meta: authorityMeta(authorityL1),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Description: "Read one leg: container, from and to handoff points, status, the robot executing it, and once failed the " +
			"reason (CANCELLED, ROBOT_BLOCKED, ROBOT_FAULT, ROBOT_OFFLINE, ORDER_REJECTED, PICK_FAILED, DROP_FAILED, " +
			"MAP_VERSION_RETIRED) with the robot's errorType. Read-only; an unknown leg fails with leg-not-found."}, d.getLeg)
	addTool(server, &mcp.Tool{Name: "get_robot", Meta: authorityMeta(authorityL1),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Description: "Read one robot of the registry: connection, PackML state, battery, charging, availability, the node it " +
			"stands on, its mission, its active error types, when it last reported, and its factsheet. Read-only; an unknown " +
			"robot fails with robot-not-found."}, d.getRobot)
}

func (d Deps) getFleet(ctx context.Context, in fleetInput) (any, error) {
	v, err := d.Reads.Fleet(ctx, in.FleetID)
	if err != nil {
		return nil, mapError(ctx, err)
	}
	return fleetPayload(v), nil
}

func (d Deps) listOpenLegs(ctx context.Context, in fleetInput) (any, error) {
	ls, err := d.Reads.OpenLegs(ctx, in.FleetID)
	if err != nil {
		return nil, mapError(ctx, err)
	}
	out := make([]map[string]any, 0, len(ls))
	for _, l := range ls {
		out = append(out, legPayload(l))
	}
	return map[string]any{"legs": out}, nil
}

func (d Deps) getLeg(ctx context.Context, in legInput) (any, error) {
	v, err := d.Reads.Leg(ctx, in.FleetID, in.LegID)
	if err != nil {
		return nil, mapError(ctx, err)
	}
	return legPayload(v), nil
}

func (d Deps) getRobot(ctx context.Context, in robotInput) (any, error) {
	if in.Manufacturer == "" || in.SerialNumber == "" {
		return nil, toolError("validation-failed", "manufacturer and serialNumber are required")
	}
	v, err := d.Reads.Robot(ctx, in.FleetID, robot.ID{Manufacturer: in.Manufacturer, SerialNumber: in.SerialNumber})
	if err != nil {
		return nil, mapError(ctx, err)
	}
	return robotPayload(v), nil
}

func stamp(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func legPayload(v usecases.LegView) map[string]any {
	out := map[string]any{"legId": v.LegID, "fleetId": v.FleetID, "containerId": v.ContainerID, "from": v.From, "to": v.To,
		"status": v.Status, "mapVersion": v.MapVersion, "acceptedAt": stamp(v.AcceptedAt)}
	for k, s := range map[string]string{"reference": v.Reference, "reason": v.Reason, "robotError": v.RobotError} {
		if s != "" {
			out[k] = s
		}
	}
	if !v.Robot.IsZero() {
		out["robot"] = v.Robot.Key()
	}
	for k, t := range map[string]time.Time{"assignedAt": v.AssignedAt, "pickedUpAt": v.PickedUpAt, "endedAt": v.EndedAt} {
		if !t.IsZero() {
			out[k] = stamp(t)
		}
	}
	return out
}

func robotPayload(v usecases.RobotView) map[string]any {
	out := map[string]any{"robot": v.ID.Key(), "connection": v.Connection, "state": v.State, "batteryPercent": v.Battery,
		"charging": v.Charging, "available": v.Available, "errorTypes": v.ErrorTypes, "lastSeenAt": stamp(v.LastSeenAt)}
	if v.ErrorTypes == nil {
		out["errorTypes"] = []string{}
	}
	if v.Node != "" {
		out["nodeId"] = v.Node
	}
	if v.MissionID != "" {
		out["missionId"] = v.MissionID
	}
	if v.HasFactsheet {
		out["factsheet"] = map[string]any{"seriesName": v.Factsheet.SeriesName, "maxSpeed": v.Factsheet.MaxSpeed, "maxLoadMass": v.Factsheet.MaxLoadMass}
	}
	return out
}

func fleetPayload(v usecases.FleetView) map[string]any {
	robots := make([]map[string]any, 0, len(v.Robots))
	for _, r := range v.Robots {
		robots = append(robots, robotPayload(r))
	}
	c := v.Capacity
	return map[string]any{"fleetId": v.FleetID, "mapId": v.MapID, "mapVersion": v.MapVersion, "handoffPoints": v.HandoffPoints,
		"robots": robots, "capacity": map[string]any{"robotsTotal": c.RobotsTotal, "robotsOnline": c.RobotsOnline,
			"robotsAvailable": c.RobotsAvailable, "robotsCharging": c.RobotsCharging, "robotsHeld": c.RobotsHeld,
			"legsQueued": c.LegsQueued, "legsActive": c.LegsActive}}
}

func addTool[In, Out any](server *mcp.Server, tool *mcp.Tool, handle func(context.Context, In) (Out, error)) {
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		out, err := handle(ctx, in)
		if err != nil {
			var zero Out
			return nil, zero, err
		}
		return nil, out, nil
	})
}

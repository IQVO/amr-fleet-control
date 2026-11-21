// Package a2a is amr-fleet-control's read-only A2A agent (agentic standard; ADR 0006): four L1
// skills over the same queries as the REST and MCP adapters. Domain problems travel inside the
// message envelope, never as JSON-RPC errors.
package a2a

import (
	"context"
	"iter"
	"net/http"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// Config is the card's identity.
type Config struct{ AgentURL, Version string }

// Reads is the read side the skills need.
type Reads interface {
	Fleet(ctx context.Context, fleetID string) (usecases.FleetView, error)
	Leg(ctx context.Context, fleetID, legID string) (usecases.LegView, error)
	OpenLegs(ctx context.Context, fleetID string) ([]usecases.LegView, error)
	Robot(ctx context.Context, fleetID string, id robot.ID) (usecases.RobotView, error)
}

// Deps are the adapter's dependencies.
type Deps struct{ Reads Reads }

// Card is the Agent Card served at a2asrv.WellKnownAgentCardPath.
func Card(cfg Config) *a2a.AgentCard {
	return &a2a.AgentCard{
		Name: "amr-fleet-control",
		Description: "AMR Fleet Control agent of the warehouse WCS tier (read-only): the VDA 5050 master control of the " +
			"autonomous mobile robots. It answers where a leg stands, what the robots are doing and how much capacity the " +
			"fleet has. It never changes data: legs are requested and cancelled by material-flow-control over REST.",
		Version:             cfg.Version,
		Provider:            &a2a.AgentProvider{Org: "IQVO", URL: "https://github.com/IQVO/amr-fleet-control"},
		SupportedInterfaces: []*a2a.AgentInterface{a2a.NewAgentInterface(cfg.AgentURL, a2a.TransportProtocolJSONRPC)},
		DefaultInputModes:   []string{contentTypeJSON, "text/plain"},
		DefaultOutputModes:  []string{contentTypeJSON, "text/plain"},
		Capabilities:        a2a.AgentCapabilities{Streaming: false},
		// Every skill is a literal with a literal Tags list: the scaffold's mcp_authority fitness test reads
		// the authority tag statically.
		// carry (P3-R13): each description names its not-found slugs; none calls an unknown id final.
		Skills: []a2a.AgentSkill{
			{
				ID: skillGetFleet, Name: "Read the fleet",
				Description: "The map version, handoff points, every robot's PackML state, battery and errors, and the capacity counts. Arguments: fleetId (required). Answers a fleet-not-found problem for a fleet the map does not name. Persists nothing.",
				Tags:        []string{"amr-fleet-control", "fleet", "capacity", "read", "authority:L1"},
				InputModes:  []string{contentTypeJSON},
				Examples:    []string{`{"skill":"get_fleet","arguments":{"fleetId":"AMR1"}}`},
			},
			{
				ID: skillListOpenLegs, Name: "List the open legs",
				Description: "The fleet's QUEUED, ASSIGNED, PICKED_UP and CANCEL_REQUESTED legs in acceptance order. Arguments: fleetId (required). Answers a fleet-not-found problem for a fleet the map does not name (never an empty list). Persists nothing.",
				Tags:        []string{"amr-fleet-control", "leg", "read", "authority:L1"},
				InputModes:  []string{contentTypeJSON},
				Examples:    []string{`{"skill":"list_open_legs","arguments":{"fleetId":"AMR1"}}`},
			},
			{
				ID: skillGetLeg, Name: "Read one leg",
				Description: "One leg: container, from and to handoff points, status, robot, and once failed the reason and the robot's errorType. Arguments: fleetId, legId (required). Answers a fleet-not-found problem for an unknown fleet and a leg-not-found problem for an unknown leg.",
				Tags:        []string{"amr-fleet-control", "leg", "read", "authority:L1"},
				InputModes:  []string{contentTypeJSON},
				Examples:    []string{`{"skill":"get_leg","arguments":{"fleetId":"AMR1","legId":"leg-0f3a91"}}`},
			},
			{
				ID: skillGetRobot, Name: "Read one robot",
				Description: "One robot: connection, PackML state, battery, charging, availability, node, mission, errors. Arguments: fleetId, manufacturer, serialNumber (required). Answers a fleet-not-found problem for an unknown fleet and a robot-not-found problem for a robot not seen yet (a robot appears on its first report, so a later read may find it).",
				Tags:        []string{"amr-fleet-control", "robot", "read", "authority:L1"},
				InputModes:  []string{contentTypeJSON},
				Examples:    []string{`{"skill":"get_robot","arguments":{"fleetId":"AMR1","manufacturer":"EMU","serialNumber":"R1"}}`},
			},
		},
	}
}

// NewExecutor answers every message with exactly one agent message.
func NewExecutor(deps Deps) a2asrv.AgentExecutor {
	return a2asrv.AgentExecutorFunc(func(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
		return func(yield func(a2a.Event, error) bool) {
			yield(answer(ctx, deps, ec), nil)
		}
	})
}

// Handler serves JSON-RPC.
func Handler(executor a2asrv.AgentExecutor) http.Handler {
	return a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(executor))
}

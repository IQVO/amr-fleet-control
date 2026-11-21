package mcp

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const reviewLegSOP = `You are reviewing one transport leg of the AMR fleet. Use only the MCP tools of amr-fleet-control; never assume state — read it.
Procedure:
1. Call get_leg(fleetId = FLEET, legId = LEG). It is this service's own record: status, robot, and once failed the reason and the robot's errorType.
2. If a robot is named, call get_robot(fleetId = FLEET, manufacturer, serialNumber) from the robot key "<manufacturer>/<serialNumber>".
3. For the whole picture, call get_fleet(fleetId = FLEET): robots held, charging or stopped, and the queued and active legs.
Interpretation:
- QUEUED with robotsAvailable 0: every robot is busy, charging (SUSPENDED), blocked (HELD) or offline (STOPPED); the leg waits.
- FAILED ROBOT_BLOCKED: the robot was blocked or deadlocked longer than the timeout; exception-management has a case. ROBOT_OFFLINE: the robot stopped reporting. ORDER_REJECTED: the robot refused the order (errorType names why) — the map and the robot's world disagree.
- containerOnRobot is only on the event; a leg that failed after PICKED_UP left the container on the robot.
Escalate to the human fleet operator when: a robot stays HELD or STOPPED, or ORDER_REJECTED repeats. Requesting, cancelling and retrying legs are material-flow-control's REST calls — nothing in this server can perform them, so do not promise them.
Done means: you have reported the leg's status and reason as recorded, its robot's state, and named the escalation when the state warrants it.`

func (d Deps) registerPrompts(server *mcp.Server) {
	server.AddPrompt(&mcp.Prompt{Name: "review_leg",
		Description: "Standard operating procedure for reviewing one leg: read the leg, its robot and the fleet, interpret the status and failure reason, and know when to escalate to the human fleet operator.",
		Arguments:   []*mcp.PromptArgument{{Name: "legId", Description: "the leg id to review, e.g. leg-0f3a91", Required: true}}},
		func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			id := strings.TrimSpace(req.Params.Arguments["legId"])
			if id == "" {
				id = "<legId>"
			}
			text := strings.ReplaceAll(strings.ReplaceAll(reviewLegSOP, "LEG", id), "FLEET", d.FleetID)
			return &mcp.GetPromptResult{Description: "How to review a leg of the AMR fleet.",
				Messages: []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: text}}}}, nil
		})
}

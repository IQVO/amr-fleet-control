// Package mcp is amr-fleet-control's read-only MCP surface (agentic standard; ADR 0006): four L1
// tools, one resource template and one prompt over the same queries as the REST adapter.
package mcp

import (
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	serverName    = "amr-fleet-control-mcp"
	serverVersion = "1.0.0"
)

const instructions = "AMR Fleet Control of the warehouse WCS tier (read-only): the VDA 5050 master control that runs the " +
	"autonomous mobile robots. Read the fleet with get_fleet (map version, handoff points, every robot's PackML state " +
	"IDLE/EXECUTE/SUSPENDED/HELD/STOPPED with battery and errors, and the capacity counts), the open legs with " +
	"list_open_legs, one leg with get_leg (status QUEUED/ASSIGNED/PICKED_UP/CANCEL_REQUESTED/DONE/FAILED/CANCELLED, the " +
	"robot, the failure reason and the robot's errorType) and one robot with get_robot (connection, state, battery, node, " +
	"mission, errors, factsheet). The fleet resource (warehouse://amr-fleet-control/fleets/{fleetId}) loads the fleet " +
	"without a tool call, and the review_leg prompt is the operator SOP for a leg. Nothing here changes data: requesting " +
	"and cancelling legs are REST operations that material-flow-control calls."

// NewServer builds the MCP server.
func NewServer(deps Deps) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: serverName, Version: serverVersion}, &mcp.ServerOptions{Instructions: instructions})
	deps.registerTools(server)
	deps.registerResources(server)
	deps.registerPrompts(server)
	return server
}

// Handler serves the server over Streamable HTTP, stateless (no session affinity across pods).
func Handler(server *mcp.Server) http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true})
}

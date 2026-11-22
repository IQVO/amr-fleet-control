package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
)

const fleetURITemplate = "warehouse://amr-fleet-control/fleets/{fleetId}"

// fleetURIPrefix is fleetURITemplate up to its one variable.
const fleetURIPrefix = "warehouse://amr-fleet-control/fleets/"

func (d Deps) registerResources(server *mcp.Server) {
	server.AddResourceTemplate(&mcp.ResourceTemplate{URITemplate: fleetURITemplate, Name: "fleet", MIMEType: "application/json",
		Description: "The fleet: map version, handoff points, every robot's state, battery and errors, and the capacity counts."},
		func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			// carry (plan 04 Task 26 lesson): an unknown or malformed fleet URI is the protocol's
			// resource-not-found naming the URI, never a tool-style slug in a generic error.
			id, ok := parseFleetURI(req.Params.URI)
			if !ok {
				return nil, mcp.ResourceNotFoundError(req.Params.URI)
			}
			v, err := d.Reads.Fleet(ctx, id)
			if errors.Is(err, usecases.ErrFleetNotFound) {
				return nil, mcp.ResourceNotFoundError(req.Params.URI)
			}
			if err != nil {
				return nil, mapError(ctx, err)
			}
			b, err := json.Marshal(fleetPayload(v))
			if err != nil {
				return nil, mapError(ctx, err)
			}
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: "application/json", Text: string(b)}}}, nil
		})
}

// parseFleetURI reads <fleetId> off warehouse://amr-fleet-control/fleets/<fleetId>.
func parseFleetURI(uri string) (fleetID string, ok bool) {
	rest, found := strings.CutPrefix(uri, fleetURIPrefix)
	if !found || rest == "" || strings.Contains(rest, "/") {
		return "", false
	}
	return rest, true
}

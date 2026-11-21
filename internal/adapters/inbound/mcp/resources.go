package mcp

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const fleetURITemplate = "warehouse://amr-fleet-control/fleets/{fleetId}"

func (d Deps) registerResources(server *mcp.Server) {
	server.AddResourceTemplate(&mcp.ResourceTemplate{URITemplate: fleetURITemplate, Name: "fleet", MIMEType: "application/json",
		Description: "The fleet: map version, handoff points, every robot's state, battery and errors, and the capacity counts."},
		func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			id := req.Params.URI[strings.LastIndex(req.Params.URI, "/")+1:]
			v, err := d.Reads.Fleet(ctx, id)
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

import type { SidebarsConfig } from "@docusaurus/plugin-content-docs";

const sidebar: SidebarsConfig = {
  apisidebar: [
    {
      type: "doc",
      id: "api-reference/rest/amr-fleet-control-api",
    },
    {
      type: "category",
      label: "legs",
      link: {
        type: "doc",
        id: "api-reference/rest/legs",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/request-leg",
          label: "Request a transport leg",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-leg",
          label: "Read a leg",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/cancel-leg",
          label: "Cancel a leg",
          className: "api-method post",
        },
      ],
    },
    {
      type: "category",
      label: "fleet",
      link: {
        type: "doc",
        id: "api-reference/rest/fleet",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/get-fleet",
          label: "Read the fleet",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-robot",
          label: "Read a robot",
          className: "api-method get",
        },
      ],
    },
    {
      type: "category",
      label: "health",
      link: {
        type: "doc",
        id: "api-reference/rest/health",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/get-liveness",
          label: "Liveness",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-readiness",
          label: "Readiness",
          className: "api-method get",
        },
      ],
    },
  ],
};

export default sidebar.apisidebar;

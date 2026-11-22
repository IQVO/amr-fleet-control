---
id: core-domain-chart
title: Core Domain Chart
sidebar_label: Core Domain Chart
description: "amr-fleet-control on the ddd-crew Core Domain Chart: Generic, low differentiation, medium complexity."
---

# Core Domain Chart

```mermaid
quadrantChart
  title amr-fleet-control on the Core Domain Chart
  x-axis Low business differentiation --> High business differentiation
  y-axis Low model complexity --> High model complexity
  quadrant-1 Core
  quadrant-2 Decisive or complex supporting
  quadrant-3 Generic
  quadrant-4 Supporting
  amr-fleet-control: [0.2, 0.55]
```

| Axis | Placement | Why |
| --- | --- | --- |
| Business differentiation | low | VDA 5050 standardises the job; any conforming fleet manager could do it, and the WES never sees it (no equipment vocabulary crosses the seam). |
| Model complexity | medium | Traffic control by reservation, base and horizon release, wait-for cycles and re-routing, charging and parking, a one-outcome guarantee per leg and order resends over an unreliable link: real rules, but all of them the standard's or the robots'. |

Classification: **Generic**, built on purpose under the replaceability rules
([subdomain classification](/docs/ddd/subdomain-classification)).

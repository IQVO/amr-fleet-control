---
id: ddd-artifacts
title: DDD artifacts (ddd-crew)
sidebar_label: DDD artifacts
sidebar_position: 4
description: "The ddd-crew artifact pack of amr-fleet-control and where each artifact's facts come from."
---

# DDD artifacts (ddd-crew)

The pack follows the [ddd-crew](https://github.com/ddd-crew) templates. Every identifier, state, event and reason in it is
taken from the code on the branch; the strategic placement comes from the
[WCS strategic design](https://iqvo.github.io/warehouse-docs/wcs) in warehouse-docs.

| Artifact | What it shows |
| --- | --- |
| [Core Domain Chart](/docs/ddd/core-domain-chart) | Why the context is Generic |
| [Bounded Context Canvas](/docs/ddd/bounded-context-canvas) | Purpose, language, decisions, inbound and outbound communication |
| [Context Map](/docs/ddd/context-map) | The relationships with material-flow-control, the robots, facility-layout, exception-management and the read-only consumers |
| [Aggregate Design Canvas](/docs/ddd/aggregate-design-canvas) | FleetMap, Robot and Mission, and the fleet-row lock |
| [Domain Message Flow](/docs/ddd/domain-message-flow) | A leg end to end, and the failure path |
| [EventStorming](/docs/ddd/eventstorming) | Commands, policies, events and read models |
| [Class diagram](/docs/ddd/class-diagram) | The domain types |
| [Entity relationship](/docs/ddd/entity-relationship) | The Postgres schema |
| [Sequence diagrams](/docs/ddd/sequence-diagrams) | Cancellation, a deadlock re-route, charging, recovery |
| [Domain events](/docs/ddd/domain-events) | Every published event and its fields |

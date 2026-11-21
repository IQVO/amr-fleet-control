Feature: amr-fleet-control assigns legs to robots and keeps them out of each other's way
  The rules are the amr-fleet-control context page's responsibilities 1-6 and plan 03's
  decisions D3-D19. Floors: the example floor (config/map.example.yaml) and a corridor S1-M1-M2-S2
  with a bypass S1-B1-B2-S2.

  Scenario: a leg goes to the nearest robot and ends exactly once
    Given the example floor
    And robot "R1" is online at "P1" with 90% battery
    And robot "R2" is online at "P3" with 90% battery
    When container "C1" is requested from "ST1" to "ASRS1-IN1"
    Then the leg of "C1" is assigned to "R1"
    When robot "R1" drives its orders to the end
    Then the leg of "C1" is "DONE"
    And 1 "LegAssigned" event was published for "C1"
    And 1 "ContainerPickedUp" event was published for "C1"
    And 1 "LegCompleted" event was published for "C1"
    And 0 "LegFailed" events were published for "C1"
    And robot "R1" stands on "P2"

  Scenario: a robot below the charge threshold is never assigned and charges first
    Given the example floor
    And robot "R1" is online at "P1" with 15% battery
    When container "C1" is requested from "ST1" to "ASRS1-IN1"
    Then the leg of "C1" is "QUEUED"
    And robot "R1" was sent to "CH1"
    When robot "R1" drives its orders to the end
    And robot "R1" charges to 80%
    Then the leg of "C1" is assigned to "R1"

  Scenario: a robot waits where another robot holds its next node
    Given the example floor
    And robot "R1" is online at "ASRS1-OUT1" with 90% battery
    And robot "R2" is online at "P2" with 90% battery
    And robot "R1" acknowledges its order
    When container "C1" is requested from "ASRS1-OUT1" to "ST1"
    And robot "R2" acknowledges its order
    And robot "R2" reaches route index 1
    Then robot "R2" waits for "A" held by "R1"
    When robot "R1" drives its orders to the end
    Then robot "R2" is no longer waiting

  Scenario: a deadlock is published once and resolved by re-routing the youngest leg
    Given the corridor floor with a bypass
    And robot "R1" is online at "S1" with 90% battery
    And robot "R2" is online at "S2" with 90% battery
    When container "C1" is requested from "S1" to "S2"
    And container "C2" is requested from "S2" to "S1"
    And robot "R1" acknowledges its order
    And robot "R2" acknowledges its order
    And robot "R1" reaches route index 1
    And robot "R2" reaches route index 1
    Then 1 deadlock was published
    And robot "R2" was sent to "S1" through "B2"

  Scenario: a deadlock that cannot be re-routed fails the youngest leg ROBOT_BLOCKED
    Given the corridor floor without a bypass
    And robot "R1" is online at "S1" with 90% battery
    And robot "R2" is online at "S2" with 90% battery
    When container "C1" is requested from "S1" to "S2"
    And container "C2" is requested from "S2" to "S1"
    And robot "R1" acknowledges its order
    And robot "R2" acknowledges its order
    And robot "R1" reaches route index 1
    And robot "R2" reaches route index 1
    And 61 seconds pass
    Then the leg of "C2" failed with "ROBOT_BLOCKED"
    And the leg of "C1" is "ASSIGNED"

  Scenario: a queued leg is cancelled at once and a picked-up leg cannot be
    Given the example floor
    And robot "R1" is online at "P1" with 90% battery
    When container "C1" is requested from "ST1" to "ASRS1-IN1"
    And container "C2" is requested from "ST2" to "ASRS1-IN2"
    And the leg of "C2" is cancelled
    Then the leg of "C2" failed with "CANCELLED"
    When robot "R1" drives its order up to the pickup
    Then cancelling the leg of "C1" is refused with "leg-not-cancellable"

  Scenario: a rejected order fails its leg once
    Given the example floor
    And robot "R1" is online at "P1" with 90% battery
    When container "C1" is requested from "ST1" to "ASRS1-IN1"
    And robot "R1" rejects its order with "INVALID_ORDER"
    Then the leg of "C1" failed with "ORDER_REJECTED"
    And 1 "LegFailed" event was published for "C1"

  Scenario: a robot that stops reporting fails its leg ROBOT_OFFLINE
    Given the example floor
    And robot "R1" is online at "P1" with 90% battery
    When container "C1" is requested from "ST1" to "ASRS1-IN1"
    And robot "R1" acknowledges its order
    And 91 seconds pass
    And 61 seconds pass
    Then the leg of "C1" failed with "ROBOT_OFFLINE"

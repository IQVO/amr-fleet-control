package features_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cucumber/godog"

	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
	"github.com/IQVO/amr-fleet-control/internal/domain/capacity"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// carry (Task 10 review): the steps of the three acceptance rows the brief's scenarios left unpinned
// (container-busy rejection, a silent robot leaving the capacity, a PORT -> STATION leg driven to the end).
func carrySteps(sc *godog.ScenarioContext, w *world) {
	mark := 0 // index into the published events where the last "while only" step started
	sc.Step(`^requesting container "(\w+)" from "([^"]+)" to "([^"]+)" is refused with "([a-z-]+)"$`,
		func(container, from, to, code string) error {
			_, err := w.svc.RequestLeg(context.Background(), usecases.LegRequest{FleetID: "AMR1", ContainerID: container, From: from, To: to})
			if want := map[string]error{"container-busy": usecases.ErrContainerBusy}[code]; want == nil || !errors.Is(err, want) {
				return fmt.Errorf("request of %s = %v, want %s", container, err, code)
			}
			return nil
		})
	sc.Step(`^(\d+) seconds pass while only robot "(\w+)" reports$`, func(seconds int, serial string) error {
		mark = len(w.events.All())
		for left := seconds; left > 0; left -= 30 {
			w.clock.Advance(time.Duration(min(left, 30)) * time.Second)
			if err := w.bots[serial].send(func(*robot.Report) {}); err != nil {
				return err
			}
			if err := w.svc.Tick(context.Background()); err != nil {
				return err
			}
		}
		return nil
	})
	sc.Step(`^exactly 1 capacity change was published meanwhile, with (\d+) robots? online and (\d+) available$`,
		func(online, available int) error {
			var got []capacity.Snapshot
			for _, e := range w.events.All()[mark:] {
				if c, ok := e.(capacity.FleetCapacityChanged); ok {
					got = append(got, c.Snapshot)
				}
			}
			if len(got) != 1 || got[0].RobotsOnline != online || got[0].RobotsAvailable != available {
				return fmt.Errorf("capacity changes meanwhile = %+v, want exactly one with online %d available %d", got, online, available)
			}
			return nil
		})
}

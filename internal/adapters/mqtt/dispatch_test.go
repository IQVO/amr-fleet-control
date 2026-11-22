package mqtt

import (
	"context"
	"strings"
	"testing"
)

// carry (Task 11 review): a message whose header names another robot than its topic is logged
// and dropped (ErrInvalidMessage); a zero-length payload (a cleared retained message) is
// ignored at DEBUG.
func TestDispatchDropsATopicHeaderMismatchAndIgnoresEmptyPayloads(t *testing.T) {
	sink := &logSink{}
	up := &recUplink{}
	l := newFleetLink(up, sink.logger())
	ctx := context.Background()
	l.dispatch(ctx, "vda5050/v3/EMU/R2/state", []byte(`{`+hdr+`,"lastNodeId":"P1"}`))
	l.dispatch(ctx, "vda5050/v3/EMU/R2/connection", []byte(`{`+hdr+`,"connectionState":"ONLINE"}`))
	l.dispatch(ctx, "vda5050/v3/ACME/R1/factsheet", []byte(`{`+hdr+`}`))
	if got := up.all(); len(got) != 0 {
		t.Fatalf("mismatched messages were applied: %v", got)
	}
	dropped := sink.records("robot message not applied")
	if len(dropped) != 3 {
		t.Fatalf("dropped records = %v", dropped)
	}
	for _, r := range dropped {
		e, _ := r["error"].(string)
		if r["level"] != "ERROR" || !strings.Contains(e, "invalid VDA 5050 message") || !strings.Contains(e, "EMU/R1") {
			t.Fatalf("record = %v", r)
		}
	}
	l.dispatch(ctx, "vda5050/v3/EMU/R1/connection", nil)
	l.dispatch(ctx, "vda5050/v3/EMU/R1/factsheet", []byte{})
	if got := up.all(); len(got) != 0 {
		t.Fatalf("empty payloads were applied: %v", got)
	}
	if n := len(sink.records("robot message not applied")); n != 3 {
		t.Fatalf("an empty payload was logged as a failure (%d failure records)", n)
	}
	if empty := sink.records("empty payload ignored"); len(empty) != 2 || empty[0]["level"] != "DEBUG" || empty[0]["topic"] != "vda5050/v3/EMU/R1/connection" {
		t.Fatalf("empty records = %v", empty)
	}
	l.dispatch(ctx, "vda5050/v3/EMU/R1/state", []byte(`{`+hdr+`,"lastNodeId":"P1"}`))
	if got := up.all(); len(got) != 1 || got[0] != "state EMU/R1 P1" {
		t.Fatalf("a matching message: %v", got)
	}
}

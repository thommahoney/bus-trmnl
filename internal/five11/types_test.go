package five11

import (
	"encoding/json"
	"testing"
)

func TestFlexStringDecode(t *testing.T) {
	cases := map[string]string{
		`"Forest Hill"`:           "Forest Hill",
		`["Forest Hill","Other"]`: "Forest Hill",
		`[]`:                      "",
		`null`:                    "",
	}
	for in, want := range cases {
		var f FlexString
		if err := json.Unmarshal([]byte(in), &f); err != nil {
			t.Fatalf("Unmarshal(%s): %v", in, err)
		}
		if string(f) != want {
			t.Fatalf("Unmarshal(%s) = %q, want %q", in, f, want)
		}
	}
}

func TestStopMonitoringDecode(t *testing.T) {
	body := `{"ServiceDelivery":{"StopMonitoringDelivery":{"MonitoredStopVisit":[
		{"MonitoredVehicleJourney":{"LineRef":"N","DirectionRef":"IB",
		"PublishedLineName":"N","DestinationName":["Caltrain"],
		"MonitoredCall":{"ExpectedArrivalTime":"2026-06-06T15:00:00Z"}}}]}}}`
	var out StopMonitoringResponse
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	visits := out.ServiceDelivery.StopMonitoringDelivery.MonitoredStopVisit
	if len(visits) != 1 {
		t.Fatalf("got %d visits, want 1", len(visits))
	}
	if got := string(visits[0].MonitoredVehicleJourney.DestinationName); got != "Caltrain" {
		t.Fatalf("destination = %q, want Caltrain", got)
	}
}

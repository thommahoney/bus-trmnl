package five11

import (
	"bytes"
	"encoding/json"
)

// StopMonitoringResponse models the 511 SIRI StopMonitoring JSON payload.
type StopMonitoringResponse struct {
	ServiceDelivery ServiceDelivery `json:"ServiceDelivery"`
}

// ServiceDelivery is the SIRI envelope.
type ServiceDelivery struct {
	ResponseTimestamp      string                 `json:"ResponseTimestamp"`
	StopMonitoringDelivery StopMonitoringDelivery `json:"StopMonitoringDelivery"`
}

// StopMonitoringDelivery wraps the list of visits.
type StopMonitoringDelivery struct {
	MonitoredStopVisit []MonitoredStopVisit `json:"MonitoredStopVisit"`
}

// MonitoredStopVisit is one predicted arrival at a stop.
type MonitoredStopVisit struct {
	MonitoredVehicleJourney MonitoredVehicleJourney `json:"MonitoredVehicleJourney"`
}

// MonitoredVehicleJourney holds line, direction and call details.
type MonitoredVehicleJourney struct {
	LineRef           string        `json:"LineRef"`
	DirectionRef      string        `json:"DirectionRef"`
	PublishedLineName FlexString    `json:"PublishedLineName"`
	DestinationName   FlexString    `json:"DestinationName"`
	MonitoredCall     MonitoredCall `json:"MonitoredCall"`
}

// MonitoredCall holds the predicted times at the monitored stop.
type MonitoredCall struct {
	StopPointName         FlexString `json:"StopPointName"`
	ExpectedArrivalTime   string     `json:"ExpectedArrivalTime"`
	ExpectedDepartureTime string     `json:"ExpectedDepartureTime"`
	AimedArrivalTime      string     `json:"AimedArrivalTime"`
}

// FlexString decodes a JSON value that 511 sometimes returns as a string and
// sometimes as an array of strings (it uses the first element).
type FlexString string

// UnmarshalJSON accepts either a string or a non-empty array of strings.
func (f *FlexString) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		*f = ""
		return nil
	}
	if data[0] == '[' {
		var arr []string
		if err := json.Unmarshal(data, &arr); err != nil {
			return err
		}
		if len(arr) > 0 {
			*f = FlexString(arr[0])
		} else {
			*f = ""
		}
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	*f = FlexString(s)
	return nil
}

// StopsResponse models the 511 stops listing (best-effort; used by discover).
type StopsResponse struct {
	Contents struct {
		DataObjects struct {
			ScheduledStopPoint []ScheduledStopPoint `json:"ScheduledStopPoint"`
		} `json:"dataObjects"`
	} `json:"Contents"`
}

// ScheduledStopPoint is one stop in the stops listing.
type ScheduledStopPoint struct {
	ID       string     `json:"id"`
	Name     FlexString `json:"Name"`
	Location struct {
		Longitude string `json:"Longitude"`
		Latitude  string `json:"Latitude"`
	} `json:"Location"`
}

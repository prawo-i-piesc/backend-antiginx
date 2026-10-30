// Package results odbiera wyniki skanów z kolejki wynikowej daemona i
// zapisuje je do bazy paczkami.
package results

import (
	"encoding/json"
	"errors"

	"github.com/google/uuid"
)

// ScanContext to wiadomość na kolejce wynikowej, w kształcie ScanContext z
// daemona (app/internal/worker/dispatcher/event-handle/scan-result.go).
type ScanContext struct {
	Target  string     `json:"target"`
	TestId  string     `json:"testId"`
	EndFlag bool       `json:"endFlag"`
	Result  ScanResult `json:"result"`
}

type ScanResult struct {
	TestResult TestResult  `json:"testResult"`
	ScanInfo   RequestInfo `json:"scanInfo"`
	ResultType ResultType  `json:"resultType"`
}

type TestResult struct {
	Name        string          `json:"Name"`
	Certainty   int             `json:"Certainty"`
	ThreatLevel string          `json:"ThreatLevel"`
	Metadata    json.RawMessage `json:"Metadata"`
	Description string          `json:"Description"`
}

type RequestInfo struct {
	Message     string   `json:"Message"`
	Code        int      `json:"Code"`
	Protections []string `json:"Protections,omitempty"`
}

type ResultType int

const (
	// Message to komunikat silnika zamiast wyniku: test przerwany, zablokowany
	// przez ochronę antybotową albo wiadomość końcowa.
	Message ResultType = iota
	Success
)

var errInvalidTestID = errors.New("testId nie jest UUID")

// message to rozpakowana wiadomość z już sprawdzonym ID skanu.
type message struct {
	scanID uuid.UUID
	ScanContext
}

func decode(body []byte) (message, error) {
	var msg message
	if err := json.Unmarshal(body, &msg.ScanContext); err != nil {
		return message{}, err
	}
	id, err := uuid.Parse(msg.TestId)
	if err != nil {
		return message{}, errInvalidTestID
	}
	msg.scanID = id
	return msg, nil
}

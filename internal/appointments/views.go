package appointments

import (
	"encoding/json"
	"time"

	"github.com/nikenesx/aura-server/internal/store"
)

// summaryView is one row of the history list.
type summaryView struct {
	ID          string       `json:"id"`
	CreatedAt   time.Time    `json:"created_at"`
	Filename    string       `json:"filename"`
	DurationSec float64      `json:"duration_sec"`
	Status      store.Status `json:"status"`
	Progress    int          `json:"progress"`
	Error       string       `json:"error,omitempty"`
	CardError   string       `json:"card_error,omitempty"`
	PetName     string       `json:"pet_name,omitempty"`
	Species     string       `json:"species,omitempty"`
	OwnerName   string       `json:"owner_name,omitempty"`
	Diagnosis   string       `json:"diagnosis,omitempty"`
}

// detailView is everything the appointment screen shows. JSON documents pass
// through as stored.
type detailView struct {
	ID           string          `json:"id"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
	Filename     string          `json:"filename"`
	AudioBytes   int64           `json:"audio_bytes"`
	DurationSec  float64         `json:"duration_sec"`
	Status       store.Status    `json:"status"`
	Progress     int             `json:"progress"`
	Error        string          `json:"error,omitempty"`
	Transcript   json.RawMessage `json:"transcript"`
	Roles        json.RawMessage `json:"roles"`
	Waveform     json.RawMessage `json:"waveform"`
	Card         json.RawMessage `json:"card"`
	CardEditedAt *time.Time      `json:"card_edited_at"`
	CardError    string          `json:"card_error,omitempty"`
}

func detail(a store.Appointment) detailView {
	return detailView{
		ID:           a.ID,
		CreatedAt:    a.CreatedAt,
		UpdatedAt:    a.UpdatedAt,
		Filename:     a.Filename,
		AudioBytes:   a.AudioBytes,
		DurationSec:  a.DurationSec,
		Status:       a.Status,
		Progress:     a.Progress,
		Error:        a.Error,
		Transcript:   orNull(a.Transcript),
		Roles:        orNull(a.Roles),
		Waveform:     orNull(a.Waveform),
		Card:         orNull(a.Card),
		CardEditedAt: a.CardEditedAt,
		CardError:    a.CardError,
	}
}

func summary(a store.Appointment) summaryView {
	v := summaryView{
		ID:          a.ID,
		CreatedAt:   a.CreatedAt,
		Filename:    a.Filename,
		DurationSec: a.DurationSec,
		Status:      a.Status,
		Progress:    a.Progress,
		Error:       a.Error,
		CardError:   a.CardError,
	}
	fields := cardFields(a.Card)
	v.PetName, v.Species = fields["pet_name"], fields["species"]
	v.OwnerName, v.Diagnosis = fields["owner_name"], fields["diagnosis"]
	return v
}

// cardFields pulls plain field values out of a stored card by key.
func cardFields(raw json.RawMessage) map[string]string {
	var c struct {
		Sections []struct {
			Fields []struct {
				Key   string  `json:"key"`
				Value *string `json:"value"`
			} `json:"fields"`
		} `json:"sections"`
	}
	out := map[string]string{}
	if json.Unmarshal(raw, &c) != nil {
		return out
	}
	for _, s := range c.Sections {
		for _, f := range s.Fields {
			if f.Value != nil {
				out[f.Key] = *f.Value
			}
		}
	}
	return out
}

func orNull(v json.RawMessage) json.RawMessage {
	if len(v) == 0 {
		return json.RawMessage("null")
	}
	return v
}

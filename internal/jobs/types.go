package jobs

type Phrase struct {
	Text    string  `json:"text"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Speaker string  `json:"speaker"`
}

// Result is what the ASR service returns for one recording.
type Result struct {
	Text    string   `json:"text"`
	Phrases []Phrase `json:"phrases"`
	// Duration of the recording in seconds.
	Duration float64 `json:"duration,omitempty"`
	// Waveform is loudness peaks in 0..1 across the recording, for the player.
	Waveform []float32 `json:"waveform,omitempty"`
}

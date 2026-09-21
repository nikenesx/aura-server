package jobs

import (
	"sync"
	"time"
)

type Status string

const (
	StatusQueued     Status = "queued"
	StatusProcessing Status = "processing"
	StatusDone       Status = "done"
	StatusFailed     Status = "failed"
)

type Phrase struct {
	Text    string  `json:"text"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Speaker string  `json:"speaker"`
}

type Result struct {
	Text    string   `json:"text"`
	Phrases []Phrase `json:"phrases"`
}

type Job struct {
	ID      string  `json:"job_id"`
	Status  Status  `json:"status"`
	Error   string  `json:"error,omitempty"`
	Result  *Result `json:"result,omitempty"`
	Created time.Time
	Audio   []byte
	Name    string
	Bytes   int
}

type Store struct {
	mu   sync.Mutex
	jobs map[string]*Job
	ttl  time.Duration
}

func NewStore(ttl time.Duration) *Store {
	if ttl <= 0 {
		ttl = 2 * time.Hour
	}
	return &Store{jobs: map[string]*Job{}, ttl: ttl}
}

func (s *Store) Create(id, name string, audio []byte) *Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	job := &Job{
		ID:      id,
		Status:  StatusQueued,
		Created: time.Now(),
		Audio:   audio,
		Name:    name,
		Bytes:   len(audio),
	}
	s.jobs[id] = job
	return copyJob(job)
}

func (s *Store) Get(id string) (*Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok {
		return nil, false
	}
	return copyJob(job), true
}

func (s *Store) TakeAudio(id string) (name string, audio []byte, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, found := s.jobs[id]
	if !found || job.Audio == nil {
		return "", nil, false
	}
	job.Status = StatusProcessing
	name = job.Name
	audio = job.Audio
	job.Audio = nil
	return name, audio, true
}

func (s *Store) Finish(id string, result Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok {
		return
	}
	job.Status = StatusDone
	job.Result = &result
	job.Audio = nil
}

func (s *Store) Fail(id, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok {
		return
	}
	job.Status = StatusFailed
	job.Error = message
	job.Audio = nil
}

func (s *Store) Sweep(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, job := range s.jobs {
		if now.Sub(job.Created) > s.ttl {
			delete(s.jobs, id)
		}
	}
}

func copyJob(job *Job) *Job {
	out := *job
	out.Audio = nil
	if job.Result != nil {
		copied := *job.Result
		if copied.Phrases == nil {
			copied.Phrases = []Phrase{}
		}
		out.Result = &copied
	}
	return &out
}

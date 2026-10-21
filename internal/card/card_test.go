package card

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nikenesx/aura-server/internal/llm"
)

type fakeModel struct {
	reply    string
	failures int
	err      error
	calls    int
	messages []llm.Message
	schema   any
}

func (f *fakeModel) JSONStream(_ context.Context, messages []llm.Message, _ string, schema any, _ llm.Sampling, onToken func(int)) (json.RawMessage, error) {
	for i := 1; onToken != nil && i <= 5000; i += 500 {
		onToken(i)
	}
	f.calls++
	f.messages, f.schema = messages, schema
	if f.calls <= f.failures {
		return nil, f.err
	}
	return json.RawMessage(f.reply), nil
}

var lines = []Line{
	{Speaker: "speaker_0", Text: "Здравствуйте, что случилось с Мусей?"},
	{Speaker: "speaker_1", Text: "Чешется и трясёт головой."},
	{Speaker: "speaker_0", Text: "Преднизолон 5 мг, по половине таблетки через день, семь дней."},
}

const reply = `{
	"roles": {"speaker_0": "doctor", "speaker_1": "owner"},
	"owner_name": {"value": "Иванова Анна Сергеевна", "sources": []},
	"pet_name": {"value": "Муся", "sources": [0]},
	"species": {"value": "  ", "sources": [0]},
	"age": {"value": null, "sources": []},
	"sex": {"value": null, "sources": []},
	"breed": {"value": null, "sources": []},
	"weight": {"value": null, "sources": []},
	"temperature": {"value": null, "sources": []},
	"complaints": {"value": "Зуд, трясёт головой.", "sources": [1, 1, 9, -1]},
	"anamnesis": {"value": null, "sources": []},
	"examination": {"value": null, "sources": []},
	"diagnosis": {"value": null, "sources": []},
	"differential_diagnosis": {"value": null, "sources": []},
	"prognosis": {"value": null, "sources": []},
	"tests": {"value": null, "sources": []},
	"recommendations": [
		{"text": "Преднизолон 5 мг в табл., по 1/2 табл. внутрь через день, 7 дней.", "sources": [2]},
		{"text": "Курс антибиотика 10 дней.", "sources": []},
		{"text": " ", "sources": []}
	]
}`

func newTestGenerator(m Model) *Generator {
	g := NewGenerator(m, VetAppointment)
	g.retryMinDelay = time.Millisecond
	g.retryMaxDelay = time.Millisecond
	return g
}

func field(t *testing.T, c *Card, key string) FieldValue {
	t.Helper()
	for _, s := range c.Sections {
		for _, f := range s.Fields {
			if f.Key == key {
				return f
			}
		}
	}
	t.Fatalf("field %s missing", key)
	return FieldValue{}
}

func TestMakeFillsTemplate(t *testing.T) {
	m := &fakeModel{reply: reply}
	c, err := newTestGenerator(m).Make(context.Background(), lines, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if c.Template != "vet_appointment_v1" || c.Roles["speaker_0"] != "doctor" || c.Roles["speaker_1"] != "owner" {
		t.Fatalf("card header: %+v", c)
	}
	if f := field(t, c, "pet_name"); f.Value == nil || *f.Value != "Муся" {
		t.Fatalf("pet_name: %+v", f)
	}
	if f := field(t, c, "species"); f.Value != nil || len(f.Sources) != 0 {
		t.Fatalf("blank value should become null: %+v", f)
	}
	// A value the model cannot point at in the transcript is made up.
	if f := field(t, c, "owner_name"); f.Value != nil {
		t.Fatalf("uncited value should be dropped: %+v", f)
	}
	if f := field(t, c, "complaints"); len(f.Sources) != 1 || f.Sources[0] != 1 {
		t.Fatalf("sources should be deduplicated and in range: %+v", f.Sources)
	}
	if f := field(t, c, "recommendations"); len(f.Items) != 1 || f.Items[0].Sources[0] != 2 {
		t.Fatalf("uncited and blank items should be dropped: %+v", f.Items)
	}

	for _, want := range []string{
		"Питомец: Муся\n",
		"Порода: —\n",
		"Рекомендации и назначения:\nДиагноз: —\n",
		"Рекомендации:\n1) Преднизолон 5 мг",
	} {
		if !strings.Contains(c.Text, want) {
			t.Errorf("text lacks %q:\n%s", want, c.Text)
		}
	}
}

// Seen from T-lite on a clip that ended before any treatment was discussed:
// it invented a whole plan and pointed every item at one unrelated phrase.
func TestInventedItemsWithBogusSourcesAreDropped(t *testing.T) {
	lines := []Line{
		{Speaker: "speaker_1", Text: "Прививки и обработки от блох не делали."},
		{Speaker: "speaker_0", Text: "Хорошо, давайте посмотрим. Сейчас взвесим."},
	}
	reply := `{"roles":{"speaker_0":"doctor","speaker_1":"owner"},"recommendations":[
		{"text":"Обработка от блох и клещей (например, фипронил-содержащий препарат)","sources":[1]},
		{"text":"Повторный осмотр через 10–14 дней","sources":[1]}
	]}`
	m := &fakeModel{reply: reply}
	c, err := newTestGenerator(m).Make(context.Background(), lines, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if f := field(t, c, "recommendations"); len(f.Items) != 0 {
		t.Fatalf("items unrelated to their sources should be dropped: %+v", f.Items)
	}
}

// Seen from T-lite: an owner's name nobody said, cited to a greeting.
func TestInventedNameIsDropped(t *testing.T) {
	lines := []Line{
		{Speaker: "speaker_0", Text: "Здравствуйте, проходите."},
		{Speaker: "speaker_1", Text: "Кошку зовут Муся, ей девять месяцев."},
	}
	reply := `{"roles":{"speaker_0":"doctor","speaker_1":"owner"},
		"owner_name":{"value":"Иванова Анна Сергеевна","sources":[0]},
		"pet_name":{"value":"Муся","sources":[1]},
		"age":{"value":"9 мес.","sources":[1]}}`
	c, err := newTestGenerator(&fakeModel{reply: reply}).Make(context.Background(), lines, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if f := field(t, c, "owner_name"); f.Value != nil {
		t.Fatalf("a name never said must be dropped: %q", *f.Value)
	}
	if f := field(t, c, "pet_name"); f.Value == nil || *f.Value != "Муся" {
		t.Fatalf("a name that was said must stay: %+v", f)
	}
	// Not a name field: the model may summarise ("девять месяцев" → "9 мес.").
	if f := field(t, c, "age"); f.Value == nil {
		t.Fatal("a summarised value in an ordinary field must stay")
	}
}

// Seen from T-lite: "null" as a string instead of a JSON null.
func TestPlaceholderValuesAreEmpty(t *testing.T) {
	lines := []Line{{Speaker: "speaker_0", Text: "Температуру измерить не удалось."}}
	reply := `{"temperature":{"value":"null","sources":[0]},"weight":{"value":" — ","sources":[0]},
		"recommendations":[{"text":"null","sources":[0]}]}`
	c, err := newTestGenerator(&fakeModel{reply: reply}).Make(context.Background(), lines, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if field(t, c, "temperature").Value != nil || field(t, c, "weight").Value != nil || len(field(t, c, "recommendations").Items) != 0 {
		t.Fatalf("placeholders must become empty:\n%s", c.Text)
	}
	if strings.Contains(c.Text, "null") {
		t.Fatalf("text shows a placeholder:\n%s", c.Text)
	}
}

func TestWordsInSources(t *testing.T) {
	lines := []Line{{Text: "Здравствуйте, что случилось с Мусей?"}, {Text: "Иванова Анна Петровна, хозяйка."}}
	cases := []struct {
		text string
		src  []int
		want bool
	}{
		{"Муся", []int{0}, true},
		{"Иванова Анна Петровна", []int{1}, true},
		{"Ивановой Анне", []int{1}, true},
		{"Иванова Анна Сергеевна", []int{1}, false},
		{"Смирнова", []int{0, 1}, false},
	}
	for _, c := range cases {
		if got := wordsInSources(c.text, c.src, lines); got != c.want {
			t.Errorf("wordsInSources(%q, %v) = %v", c.text, c.src, got)
		}
	}
}

func TestQuotesSource(t *testing.T) {
	lines := []Line{
		{Text: "Преднизолон 5 мг в таблетках, по половине таблетки внутрь через день."},
		{Text: "Хорошо, давайте посмотрим. Сейчас взвесим."},
	}
	cases := []struct {
		text    string
		sources []int
		want    bool
	}{
		{"Преднизолон 5 мг, по 1/2 таблетки внутрь через день", []int{0}, true},
		{"Обработка помещения против блох", []int{1}, false},
		{"Преднизолон 5 мг", []int{1, 0}, true},
	}
	for _, tc := range cases {
		if got := quotesSource(tc.text, tc.sources, lines); got != tc.want {
			t.Errorf("quotesSource(%q, %v) = %v", tc.text, tc.sources, got)
		}
	}
}

func TestPromptNumbersPhrasesAndListsFields(t *testing.T) {
	m := &fakeModel{reply: reply}
	if _, err := newTestGenerator(m).Make(context.Background(), lines, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.messages[1].Content, "[2] speaker_0: Преднизолон") {
		t.Fatalf("transcript prompt:\n%s", m.messages[1].Content)
	}
	for _, f := range []string{"owner_name — Ф.И.О. владельца", "recommendations — Рекомендации (список"} {
		if !strings.Contains(m.messages[0].Content, f) {
			t.Errorf("system prompt lacks %q", f)
		}
	}
}

func TestSchemaCoversEveryField(t *testing.T) {
	schema := responseSchema(VetAppointment, []string{"speaker_0", "speaker_1"}, nil)
	required := schema["required"].([]string)
	if required[0] != "roles" {
		t.Fatalf("roles should come first: %v", required)
	}
	n := 0
	for _, s := range VetAppointment.Sections {
		n += len(s.Fields)
	}
	if len(required) != n+1 {
		t.Fatalf("required %d keys, want %d", len(required), n+1)
	}
	roles := schema["properties"].(orderedProps).values["roles"].(map[string]any)
	if got := roles["required"].([]string); len(got) != 2 {
		t.Fatalf("roles required: %v", got)
	}
}

// vLLM generates properties in schema order, so the serialized schema must
// keep roles first, fields in template order, and each value before its sources.
func TestSchemaJSONKeepsOrder(t *testing.T) {
	raw, err := json.Marshal(responseSchema(VetAppointment, []string{"speaker_0"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	order := []string{`"roles"`, `"owner_name"`, `"complaints"`, `"examination"`, `"diagnosis"`, `"recommendations"`}
	last := -1
	for _, key := range order {
		i := strings.Index(s, key+":")
		if i <= last {
			t.Fatalf("%s out of order in %s", key, s)
		}
		last = i
	}
	if strings.Index(s, `"value":`) > strings.Index(s, `"sources":`) {
		t.Fatalf("value should precede sources: %s", s)
	}
	if strings.Index(s, `"text":`) > strings.LastIndex(s, `"sources":`) {
		t.Fatalf("text should precede sources: %s", s)
	}
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
}

func TestTemplateKeysAreUnique(t *testing.T) {
	seen := map[string]bool{"roles": true}
	for _, s := range VetAppointment.Sections {
		for _, f := range s.Fields {
			if seen[f.Key] {
				t.Fatalf("duplicate key %s", f.Key)
			}
			seen[f.Key] = true
		}
	}
}

func TestMakeRetriesWhileUnavailable(t *testing.T) {
	m := &fakeModel{reply: reply, failures: 2, err: llm.ErrUnavailable}
	if _, err := newTestGenerator(m).Make(context.Background(), lines, nil, nil); err != nil {
		t.Fatal(err)
	}
	if m.calls != 3 {
		t.Fatalf("calls %d", m.calls)
	}
}

func TestMakeFailsFastOnOtherErrors(t *testing.T) {
	m := &fakeModel{failures: 5, err: errors.New("context too long")}
	if _, err := newTestGenerator(m).Make(context.Background(), lines, nil, nil); err == nil || m.calls != 1 {
		t.Fatalf("err=%v calls=%d", err, m.calls)
	}
}

func TestEmptyTranscriptSkipsModel(t *testing.T) {
	m := &fakeModel{}
	c, err := newTestGenerator(m).Make(context.Background(), nil, nil, nil)
	if err != nil || m.calls != 0 || !strings.Contains(c.Text, "Диагноз: —") {
		t.Fatalf("err=%v calls=%d text=%q", err, m.calls, c.Text)
	}
}

func TestFieldJSONShape(t *testing.T) {
	v := "Муся"
	plain, _ := json.Marshal(FieldValue{Key: "pet_name", Title: "Питомец", Value: &v})
	empty, _ := json.Marshal(FieldValue{Key: "breed", Title: "Порода"})
	list, _ := json.Marshal(FieldValue{Key: "recommendations", Title: "Рекомендации", List: true})

	want := map[string]string{
		string(plain): `{"key":"pet_name","title":"Питомец","value":"Муся","sources":[]}`,
		string(empty): `{"key":"breed","title":"Порода","value":null,"sources":[]}`,
		string(list):  `{"key":"recommendations","title":"Рекомендации","items":[]}`,
	}
	for got, w := range want {
		if got != w {
			t.Errorf("got %s want %s", got, w)
		}
	}
}

func TestKnownRolesAreFixedInSchema(t *testing.T) {
	m := &fakeModel{reply: reply}
	roles := map[string]string{"speaker_0": RoleDoctor}
	if _, err := newTestGenerator(m).Make(context.Background(), lines, roles, nil); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(m.schema)
	var schema struct {
		Properties struct {
			Roles struct {
				Properties map[string]struct {
					Enum []string `json:"enum"`
				} `json:"properties"`
			} `json:"roles"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	got := schema.Properties.Roles.Properties
	if e := got["speaker_0"].Enum; len(e) != 1 || e[0] != RoleDoctor {
		t.Fatalf("known role should be the only choice: %v", e)
	}
	if e := got["speaker_1"].Enum; len(e) != 3 {
		t.Fatalf("unknown speaker should keep every choice: %v", e)
	}
	if !strings.Contains(m.messages[1].Content, "speaker_0 — врач") {
		t.Fatalf("prompt should state the known roles:\n%s", m.messages[1].Content)
	}
}

func TestCardProgressStaysBelowDone(t *testing.T) {
	m := &fakeModel{reply: reply}
	var seen []float64
	if _, err := newTestGenerator(m).Make(context.Background(), lines, nil, func(f float64) { seen = append(seen, f) }); err != nil {
		t.Fatal(err)
	}
	if len(seen) == 0 {
		t.Fatal("no progress reported")
	}
	for i, f := range seen {
		if f > 0.95 || (i > 0 && f < seen[i-1]) {
			t.Fatalf("progress must grow and stay under 0.95: %v", seen)
		}
	}
}

func TestRoles(t *testing.T) {
	m := &fakeModel{reply: `{"speaker_0":"doctor","speaker_1":"owner","speaker_2":"boss"}`}
	three := append(lines, Line{Speaker: "speaker_2", Text: "Здравствуйте"})
	roles, err := newTestGenerator(m).Roles(context.Background(), three)
	if err != nil {
		t.Fatal(err)
	}
	if roles["speaker_0"] != RoleDoctor || roles["speaker_1"] != RoleOwner {
		t.Fatalf("roles: %v", roles)
	}
	if _, ok := roles["speaker_2"]; ok {
		t.Fatalf("an unknown role must be dropped: %v", roles)
	}
}

func TestRolesExcerptKeepsLateSpeakers(t *testing.T) {
	var many []Line
	for i := 0; i < 100; i++ {
		many = append(many, Line{Speaker: "speaker_0", Text: "врач"})
	}
	many = append(many, Line{Speaker: "speaker_1", Text: "поздний владелец"})
	got := rolesLines(many)
	if n := strings.Count(got, "\n["); n > rolesExcerpt+1 {
		t.Fatalf("excerpt too long: %d lines", n)
	}
	if !strings.Contains(got, "[100] speaker_1: поздний владелец") || !strings.Contains(got, "...") {
		t.Fatalf("late speaker missing:\n%s", got)
	}
}

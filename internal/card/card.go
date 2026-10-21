// Package card turns an appointment transcript into a filled appointment card.
package card

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/nikenesx/aura-server/internal/llm"
)

// Line is one phrase of the transcript. Its position is the number the model
// cites in sources.
type Line struct {
	Speaker string
	Text    string
}

type Card struct {
	Template string            `json:"template"`
	Roles    map[string]string `json:"roles"`
	Sections []SectionValue    `json:"sections"`
	// Text is the card laid out like the printed form, for printing or pasting.
	// It is rendered from the fields unless TextEdited: then it is the
	// doctor's own wording and field edits leave it alone.
	Text       string `json:"text"`
	TextEdited bool   `json:"text_edited,omitempty"`
}

type SectionValue struct {
	Title  string       `json:"title"`
	Fields []FieldValue `json:"fields"`
}

// FieldValue holds either Value (plain field) or Items (list field). Sources
// are indices into the transcript phrases the value came from.
type FieldValue struct {
	Key     string
	Title   string
	List    bool
	Value   *string
	Sources []int
	Items   []Item
}

type Item struct {
	Text    string `json:"text"`
	Sources []int  `json:"sources"`
}

func (f FieldValue) MarshalJSON() ([]byte, error) {
	if f.List {
		items := f.Items
		if items == nil {
			items = []Item{}
		}
		return json.Marshal(struct {
			Key   string `json:"key"`
			Title string `json:"title"`
			Items []Item `json:"items"`
		}{f.Key, f.Title, items})
	}
	sources := f.Sources
	if sources == nil {
		sources = []int{}
	}
	return json.Marshal(struct {
		Key     string  `json:"key"`
		Title   string  `json:"title"`
		Value   *string `json:"value"`
		Sources []int   `json:"sources"`
	}{f.Key, f.Title, f.Value, sources})
}

// Model is the part of llm.Client the generator needs. A nil onToken makes a
// plain request; otherwise it is told how many tokens the reply has so far.
type Model interface {
	JSONStream(ctx context.Context, messages []llm.Message, name string, schema any, s llm.Sampling, onToken func(tokens int)) (json.RawMessage, error)
}

// Roles a speaker can have.
const (
	RoleDoctor = "doctor"
	RoleOwner  = "owner"
	RoleOther  = "other"
)

var roleNames = []string{RoleDoctor, RoleOwner, RoleOther}

// rolesExcerpt is how many opening phrases the roles call reads. Who is the
// doctor is plain from the greeting and the first questions, and a short
// prompt answers in seconds even for an hour-long recording.
const rolesExcerpt = 60

type Generator struct {
	model    Model
	template Template
	sampling llm.Sampling
	roles    llm.Sampling

	retryMinDelay time.Duration
	retryMaxDelay time.Duration
	retryGiveUp   time.Duration
}

func NewGenerator(model Model, template Template) *Generator {
	return &Generator{
		model:    model,
		template: template,
		sampling: llm.Sampling{Temperature: 0.2, TopP: 0.8, TopK: 20, MaxTokens: 4096},
		roles:    llm.Sampling{Temperature: 0, TopP: 1, TopK: 1, MaxTokens: 200},
		// vLLM needs minutes to load the model after a restart; wait for it.
		retryMinDelay: 2 * time.Second,
		retryMaxDelay: 30 * time.Second,
		retryGiveUp:   15 * time.Minute,
	}
}

// Make fills the card. Known roles (from Roles) are fixed in the schema so the
// card cannot disagree with them; speakers missing from roles are left to the
// model. onProgress, when set, gets an estimate in 0..1 as the reply streams.
func (g *Generator) Make(ctx context.Context, lines []Line, roles map[string]string, onProgress func(float64)) (*Card, error) {
	if len(lines) == 0 {
		return g.fill(nil, lines), nil
	}
	messages := []llm.Message{
		{Role: "system", Content: systemPrompt(g.template)},
		{Role: "user", Content: transcriptPrompt(lines, roles)},
	}
	schema := responseSchema(g.template, speakers(lines), roles)

	var onToken func(int)
	if onProgress != nil {
		expected := float64(expectedCardTokens(len(lines)))
		onToken = func(n int) { onProgress(min(float64(n)/expected, 0.95)) }
	}
	raw, err := g.call(ctx, func() (json.RawMessage, error) {
		return g.model.JSONStream(ctx, messages, "appointment_card", schema, g.sampling, onToken)
	})
	if err != nil {
		return nil, err
	}
	var reply map[string]json.RawMessage
	if err := json.Unmarshal(raw, &reply); err != nil {
		return nil, fmt.Errorf("decode card: %w", err)
	}
	return g.fill(reply, lines), nil
}

// expectedCardTokens guesses the card's length for the progress bar: a short
// visit still fills most fields, a long one adds prescriptions and history.
func expectedCardTokens(phrases int) int {
	return min(max(600+20*phrases, 900), 2500)
}

// Roles says who is who from the opening of the conversation, so the
// transcript can be labelled before the card is written.
func (g *Generator) Roles(ctx context.Context, lines []Line) (map[string]string, error) {
	all := speakers(lines)
	if len(all) == 0 {
		return map[string]string{}, nil
	}

	props := orderedProps{}
	for _, s := range all {
		props.set(s, map[string]any{"type": "string", "enum": roleNames})
	}
	schema := map[string]any{"type": "object", "properties": props, "required": all, "additionalProperties": false}
	messages := []llm.Message{
		{Role: "system", Content: rolesPrompt},
		{Role: "user", Content: rolesLines(lines)},
	}
	raw, err := g.call(ctx, func() (json.RawMessage, error) {
		return g.model.JSONStream(ctx, messages, "speaker_roles", schema, g.roles, nil)
	})
	if err != nil {
		return nil, err
	}
	var got map[string]string
	if err := json.Unmarshal(raw, &got); err != nil {
		return nil, fmt.Errorf("decode roles: %w", err)
	}
	out := map[string]string{}
	for _, s := range all {
		if slices.Contains(roleNames, got[s]) {
			out[s] = got[s]
		}
	}
	return out, nil
}

const rolesPrompt = `Ты определяешь роли говорящих в автоматической расшифровке ветеринарного приёма.
doctor — ветеринарный врач: расспрашивает о симптомах, осматривает, ставит диагноз, назначает лечение.
owner — владелец животного: рассказывает о питомце и жалобах, отвечает на вопросы.
other — кто-то ещё: ассистент, второй владелец, посторонний.
Каждому говорящему из списка назначь одну роль. Врач на приёме обычно один.`

// rolesLines is the opening of the transcript, plus the first few phrases of
// any speaker who only joins later.
func rolesLines(lines []Line) string {
	var b strings.Builder
	b.WriteString("Начало расшифровки. Формат строки: [номер фразы] говорящий: текст.\n\n")
	seen := map[string]int{}
	skipped := false
	for i, line := range lines {
		if i < rolesExcerpt || seen[line.Speaker] < 3 {
			if skipped {
				b.WriteString("...\n")
				skipped = false
			}
			fmt.Fprintf(&b, "[%d] %s: %s\n", i, line.Speaker, line.Text)
		} else {
			skipped = true
		}
		seen[line.Speaker]++
	}
	return b.String()
}

// call retries while the LLM is unavailable: vLLM needs minutes to load the
// model after a restart.
func (g *Generator) call(ctx context.Context, do func() (json.RawMessage, error)) (json.RawMessage, error) {
	deadline := time.Now().Add(g.retryGiveUp)
	delay := g.retryMinDelay
	for {
		raw, err := do()
		if err == nil {
			return raw, nil
		}
		if !errors.Is(err, llm.ErrUnavailable) || time.Now().Add(delay).After(deadline) {
			return nil, err
		}
		slog.Warn("llm unavailable, retrying", "delay", delay, "error", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		delay = min(delay*2, g.retryMaxDelay)
	}
}

func systemPrompt(t Template) string {
	var b strings.Builder
	b.WriteString(`Ты помогаешь ветеринарному врачу оформить карту приёма по автоматической расшифровке разговора врача с владельцем животного.

Правила:
1. Используй только то, что сказано в расшифровке. Ничего не добавляй от себя: ни препаратов, ни доз, ни исследований, ни диагнозов, ни предупреждений.
2. Если сведений для поля нет, ставь value: null, а для списка — пустой список. Особенно это касается Ф.И.О. владельца, породы, возраста, веса и температуры: их часто не произносят вслух, и догадки здесь недопустимы.
3. Каждое заполненное поле обязано ссылаться хотя бы на одну фразу расшифровки. Значение без ссылки считается выдуманным и отбрасывается.
4. В рекомендации попадают только назначения и указания врача. Если врач ничего не назначил — например, запись обрывается на осмотре, — список должен быть пустым. Не пересказывай в нём вопросы врача и реплики владельца.
5. Названия препаратов, дозы, единицы, способ введения, кратность и длительность переноси точно как сказал врач. Не пересчитывай дозы и не исправляй их.
6. Расшифровка может содержать ошибки распознавания речи. Если название препарата явно искажено, запиши его в правильном написании, но не заменяй одно лекарство другим.
7. Пиши кратко, в деловом медицинском стиле, без разговорных слов.
8. В sources укажи номера фраз расшифровки (числа в квадратных скобках), из которых взяты сведения.
9. В roles укажи роль каждого говорящего: doctor — врач, owner — владелец, other — кто-то ещё.

Поля карты:
`)
	for _, section := range t.Sections {
		for _, f := range section.Fields {
			kind := ""
			if f.List {
				kind = " (список, каждый пункт отдельно)"
			}
			fmt.Fprintf(&b, "- %s — %s%s: %s\n", f.Key, f.Title, kind, f.Hint)
		}
	}
	return b.String()
}

var roleTitles = map[string]string{RoleDoctor: "врач", RoleOwner: "владелец", RoleOther: "другой участник"}

func transcriptPrompt(lines []Line, roles map[string]string) string {
	var b strings.Builder
	if len(roles) > 0 {
		b.WriteString("Роли говорящих уже определены:")
		for _, s := range speakers(lines) {
			if r, ok := roles[s]; ok {
				fmt.Fprintf(&b, " %s — %s;", s, roleTitles[r])
			}
		}
		b.WriteString("\n\n")
	}
	b.WriteString("Расшифровка приёма. Формат строки: [номер фразы] говорящий: текст.\n\n")
	for i, line := range lines {
		fmt.Fprintf(&b, "[%d] %s: %s\n", i, line.Speaker, line.Text)
	}
	return b.String()
}

func speakers(lines []Line) []string {
	var out []string
	for _, line := range lines {
		if !slices.Contains(out, line.Speaker) {
			out = append(out, line.Speaker)
		}
	}
	return out
}

// orderedProps marshals as a JSON object with keys in insertion order. vLLM
// generates properties in the order the schema lists them, and a Go map
// would sort them alphabetically.
type orderedProps struct {
	keys   []string
	values map[string]any
}

func (o *orderedProps) set(key string, value any) {
	if o.values == nil {
		o.values = map[string]any{}
	}
	o.keys = append(o.keys, key)
	o.values[key] = value
}

func (o orderedProps) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		key, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		value, err := json.Marshal(o.values[k])
		if err != nil {
			return nil, err
		}
		b.Write(key)
		b.WriteByte(':')
		b.Write(value)
	}
	b.WriteByte('}')
	return []byte(b.String()), nil
}

// responseSchema is the JSON schema vLLM constrains generation to. Roles go
// first, since deciding who the doctor is helps with everything after; the
// fields follow in template order, so the diagnosis comes after the exam.
func responseSchema(t Template, speakers []string, known map[string]string) map[string]any {
	sources := map[string]any{"type": "array", "items": map[string]any{"type": "integer"}}
	// The value comes before its sources: write first, then cite.
	var item, value orderedProps
	item.set("text", map[string]any{"type": "string"})
	item.set("sources", sources)
	value.set("value", map[string]any{"type": []string{"string", "null"}})
	value.set("sources", sources)

	var roles orderedProps
	for _, s := range speakers {
		allowed := roleNames
		if r, ok := known[s]; ok {
			// A one-value enum rather than const: every guided-decoding
			// backend in vLLM understands enum.
			allowed = []string{r}
		}
		roles.set(s, map[string]any{"type": "string", "enum": allowed})
	}
	var props orderedProps
	props.set("roles", map[string]any{
		"type":                 "object",
		"properties":           roles,
		"required":             speakers,
		"additionalProperties": false,
	})
	required := []string{"roles"}

	for _, section := range t.Sections {
		for _, f := range section.Fields {
			if f.List {
				props.set(f.Key, map[string]any{
					"type": "array",
					"items": map[string]any{
						"type":                 "object",
						"properties":           item,
						"required":             []string{"text", "sources"},
						"additionalProperties": false,
					},
				})
			} else {
				props.set(f.Key, map[string]any{
					"type":                 "object",
					"properties":           value,
					"required":             []string{"value", "sources"},
					"additionalProperties": false,
				})
			}
			required = append(required, f.Key)
		}
	}
	return map[string]any{
		"type":                 "object",
		"properties":           props,
		"required":             required,
		"additionalProperties": false,
	}
}

// fill lays the model's reply over the template, dropping empty values and
// sources that point outside the transcript.
func (g *Generator) fill(reply map[string]json.RawMessage, lines []Line) *Card {
	c := &Card{Template: g.template.ID, Roles: map[string]string{}}
	var roles map[string]string
	_ = json.Unmarshal(reply["roles"], &roles)
	for _, s := range speakers(lines) {
		c.Roles[s] = "other"
		if r := roles[s]; r == RoleDoctor || r == RoleOwner {
			c.Roles[s] = r
		}
	}

	for _, section := range g.template.Sections {
		sv := SectionValue{Title: section.Title}
		for _, f := range section.Fields {
			fv := FieldValue{Key: f.Key, Title: f.Title, List: f.List}
			if f.List {
				var items []Item
				_ = json.Unmarshal(reply[f.Key], &items)
				for _, it := range items {
					text := cleanValue(it.Text)
					sources := cleanSources(it.Sources, len(lines))
					// Nothing the model cannot point at in the transcript: a
					// value with no usable source is treated as made up.
					switch {
					case text == "":
					case len(sources) == 0:
						slog.Warn("dropped card item without sources", "field", f.Key, "text", text)
					case !quotesSource(text, sources, lines):
						slog.Warn("dropped card item unrelated to its sources", "field", f.Key, "text", text)
					default:
						fv.Items = append(fv.Items, Item{Text: text, Sources: sources})
					}
				}
			} else {
				var v struct {
					Value   *string `json:"value"`
					Sources []int   `json:"sources"`
				}
				_ = json.Unmarshal(reply[f.Key], &v)
				if v.Value != nil {
					text := cleanValue(*v.Value)
					sources := cleanSources(v.Sources, len(lines))
					switch {
					case text == "":
					case len(sources) == 0:
						slog.Warn("dropped card value without sources", "field", f.Key, "value", text)
					case f.Verbatim && !wordsInSources(text, sources, lines):
						slog.Warn("dropped card value not said in its sources", "field", f.Key, "value", text)
					default:
						fv.Value = &text
						fv.Sources = sources
					}
				}
			}
			sv.Fields = append(sv.Fields, fv)
		}
		c.Sections = append(c.Sections, sv)
	}
	c.Text = render(c)
	return c
}

var notWord = regexp.MustCompile(`[^\p{L}\p{Nd}]+`)

// stems cuts words down to a comparable prefix, so "обработка" and
// "обработки" match. Short words carry no evidence and are dropped.
func stems(text string) map[string]bool {
	out := map[string]bool{}
	for _, word := range notWord.Split(strings.ToLower(strings.ReplaceAll(text, "ё", "е")), -1) {
		if r := []rune(word); len(r) >= 5 {
			out[string(r[:5])] = true
		}
	}
	return out
}

// quotesSource reports whether a prescription shares any word with the phrases
// it cites. A model that invents a prescription tends to cite a phrase at
// random, and such an item has nothing in common with it.
func quotesSource(text string, sources []int, lines []Line) bool {
	cited := map[string]bool{}
	for _, i := range sources {
		for stem := range stems(lines[i].Text) {
			cited[stem] = true
		}
	}
	for stem := range stems(text) {
		if cited[stem] {
			return true
		}
	}
	return false
}

// wordsInSources reports whether every word of a name was said in the cited
// phrases. Names cannot be inferred: the model has been seen inventing an
// owner's full name and citing an unrelated phrase. Words are compared by
// prefix so case endings may differ: four letters for longer words
// ("Иванова", "Ивановой"), three for short ones ("Муся", "Мусей").
func wordsInSources(text string, sources []int, lines []Line) bool {
	said := map[string]bool{}
	for _, i := range sources {
		for _, w := range words(lines[i].Text) {
			for n := 3; n <= 4 && n <= len(w); n++ {
				said[string(w[:n])] = true
			}
		}
	}
	for _, w := range words(text) {
		if len(w) < 3 {
			continue
		}
		n := 3
		if len(w) >= 5 {
			n = 4
		}
		if !said[string(w[:n])] {
			return false
		}
	}
	return true
}

func words(text string) [][]rune {
	var out [][]rune
	for _, w := range notWord.Split(strings.ToLower(strings.ReplaceAll(text, "ё", "е")), -1) {
		if w != "" {
			out = append(out, []rune(w))
		}
	}
	return out
}

// cleanValue trims a value and empties the placeholders the model writes
// instead of leaving a field null ("null", "—").
func cleanValue(v string) string {
	v = strings.TrimSpace(v)
	switch strings.ToLower(v) {
	case "null", "none", "nil", "n/a", "-", "—", "–":
		return ""
	}
	return v
}

func cleanSources(in []int, n int) []int {
	out := []int{}
	for _, i := range in {
		if i >= 0 && i < n && !slices.Contains(out, i) {
			out = append(out, i)
		}
	}
	slices.Sort(out)
	return out
}

func render(c *Card) string {
	var blocks []string
	for _, s := range c.Sections {
		var b strings.Builder
		if s.Title != "" {
			b.WriteString(s.Title + ":\n")
		}
		for _, f := range s.Fields {
			switch {
			case f.List && len(f.Items) > 0:
				b.WriteString(f.Title + ":\n")
				for i, it := range f.Items {
					fmt.Fprintf(&b, "%d) %s\n", i+1, it.Text)
				}
			case f.List || f.Value == nil:
				b.WriteString(f.Title + ": —\n")
			default:
				b.WriteString(f.Title + ": " + *f.Value + "\n")
			}
		}
		blocks = append(blocks, b.String())
	}
	return strings.Join(blocks, "\n")
}

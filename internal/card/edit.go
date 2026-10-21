package card

import (
	"encoding/json"
	"fmt"
	"strings"
)

var templates = map[string]Template{VetAppointment.ID: VetAppointment}

// TemplateByID finds the template a stored card was made from.
func TemplateByID(id string) (Template, bool) {
	t, ok := templates[id]
	return t, ok
}

// Edit is a card as the client sends it back after the doctor changed it:
// the same sections and fields it received.
type Edit struct {
	Sections []struct {
		Fields []EditField `json:"fields"`
	} `json:"sections"`
	// With TextEdited the doctor rewrote the final text by hand and Text is
	// kept as sent; otherwise the text is rendered from the fields.
	Text       string `json:"text"`
	TextEdited bool   `json:"text_edited"`
}

type EditField struct {
	Key     string  `json:"key"`
	Value   *string `json:"value"`
	Sources []int   `json:"sources"`
	Items   []Item  `json:"items"`
}

// ApplyEdit lays the doctor's values over base and returns the card to store.
// Fields the edit leaves out keep base's values; unknown keys are ignored;
// sources outside the transcript are dropped. Unlike the model's values, the
// doctor's need no source: the doctor is the source.
func ApplyEdit(base *Card, edit Edit, phrases int) (*Card, bool) {
	t, ok := TemplateByID(base.Template)
	if !ok {
		return nil, false
	}
	byKey := map[string]EditField{}
	for _, s := range edit.Sections {
		for _, f := range s.Fields {
			byKey[f.Key] = f
		}
	}
	current := map[string]FieldValue{}
	for _, s := range base.Sections {
		for _, f := range s.Fields {
			current[f.Key] = f
		}
	}

	out := &Card{Template: base.Template, Roles: base.Roles}
	for _, section := range t.Sections {
		sv := SectionValue{Title: section.Title}
		for _, f := range section.Fields {
			fv := current[f.Key]
			fv.Key, fv.Title, fv.List = f.Key, f.Title, f.List
			if e, ok := byKey[f.Key]; ok {
				fv.Value, fv.Sources, fv.Items = nil, nil, nil
				if f.List {
					for _, it := range e.Items {
						if text := strings.TrimSpace(it.Text); text != "" {
							fv.Items = append(fv.Items, Item{Text: text, Sources: cleanSources(it.Sources, phrases)})
						}
					}
				} else if e.Value != nil {
					if text := strings.TrimSpace(*e.Value); text != "" {
						fv.Value = &text
						fv.Sources = cleanSources(e.Sources, phrases)
					}
				}
			}
			sv.Fields = append(sv.Fields, fv)
		}
		out.Sections = append(out.Sections, sv)
	}
	if edit.TextEdited && strings.TrimSpace(edit.Text) != "" {
		out.Text, out.TextEdited = strings.TrimRight(edit.Text, " \t\r\n"), true
	} else {
		out.Text = render(out)
	}
	return out, true
}

// Decode reads a stored card back. FieldValue has its own JSON shape, so it
// goes through Edit.
func Decode(raw []byte) (*Card, error) {
	var head struct {
		Template   string            `json:"template"`
		Roles      map[string]string `json:"roles"`
		Text       string            `json:"text"`
		TextEdited bool              `json:"text_edited"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, err
	}
	var body Edit
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	t, ok := TemplateByID(head.Template)
	if !ok {
		return nil, fmt.Errorf("unknown card template %q", head.Template)
	}
	byKey := map[string]EditField{}
	for _, s := range body.Sections {
		for _, f := range s.Fields {
			byKey[f.Key] = f
		}
	}
	c := &Card{Template: head.Template, Roles: head.Roles}
	for _, section := range t.Sections {
		sv := SectionValue{Title: section.Title}
		for _, f := range section.Fields {
			e := byKey[f.Key]
			sv.Fields = append(sv.Fields, FieldValue{
				Key: f.Key, Title: f.Title, List: f.List,
				Value: e.Value, Sources: e.Sources, Items: e.Items,
			})
		}
		c.Sections = append(c.Sections, sv)
	}
	if head.TextEdited {
		c.Text, c.TextEdited = head.Text, true
	} else {
		c.Text = render(c)
	}
	return c, nil
}

package card

import (
	"encoding/json"
	"strings"
	"testing"
)

func baseCard(t *testing.T) *Card {
	t.Helper()
	c, err := Decode([]byte(`{"template":"vet_appointment_v1","roles":{"speaker_0":"doctor"},"sections":[
		{"fields":[{"key":"pet_name","value":"Муся","sources":[1]},{"key":"species","value":"Кошка","sources":[1]}]},
		{"fields":[{"key":"recommendations","items":[{"text":"Преднизолон 5 мг","sources":[4]}]}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDecodeRoundTrip(t *testing.T) {
	c := baseCard(t)
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if again.Text != c.Text || again.Roles["speaker_0"] != "doctor" || !strings.Contains(c.Text, "Питомец: Муся") {
		t.Fatalf("round trip changed the card:\n%s\n---\n%s", c.Text, again.Text)
	}
	if _, err := Decode([]byte(`{"template":"nope","sections":[]}`)); err == nil {
		t.Fatal("unknown template should fail")
	}
}

func TestApplyEdit(t *testing.T) {
	var edit Edit
	_ = json.Unmarshal([]byte(`{"sections":[{"fields":[
		{"key":"pet_name","value":"  Мурка ","sources":[1]},
		{"key":"species","value":"   "},
		{"key":"made_up","value":"x"},
		{"key":"recommendations","items":[{"text":"Преднизолон 2,5 мг","sources":[4,77]},{"text":"Чистка ушей"},{"text":"  "}]}
	]}]}`), &edit)

	c, ok := ApplyEdit(baseCard(t), edit, 10)
	if !ok {
		t.Fatal("template not found")
	}
	got := map[string]FieldValue{}
	for _, s := range c.Sections {
		for _, f := range s.Fields {
			got[f.Key] = f
		}
	}
	if v := got["pet_name"].Value; v == nil || *v != "Мурка" {
		t.Fatalf("pet_name: %v", v)
	}
	if got["species"].Value != nil {
		t.Fatal("a blanked field must become empty")
	}
	if _, ok := got["made_up"]; ok {
		t.Fatal("unknown keys must be ignored")
	}
	items := got["recommendations"].Items
	if len(items) != 2 || items[0].Text != "Преднизолон 2,5 мг" || len(items[0].Sources) != 1 || items[1].Text != "Чистка ушей" {
		t.Fatalf("items: %+v", items)
	}
	if c.Roles["speaker_0"] != "doctor" || !strings.Contains(c.Text, "1) Преднизолон 2,5 мг") || !strings.Contains(c.Text, "2) Чистка ушей") {
		t.Fatalf("text not re-rendered:\n%s", c.Text)
	}
}

func TestHandEditedTextIsKept(t *testing.T) {
	var edit Edit
	_ = json.Unmarshal([]byte(`{"sections":[{"fields":[{"key":"species","value":"Собака"}]}],
		"text":"Свой текст врача\nвторая строка\n\n","text_edited":true}`), &edit)
	c, _ := ApplyEdit(baseCard(t), edit, 10)
	if !c.TextEdited || c.Text != "Свой текст врача\nвторая строка" {
		t.Fatalf("hand-edited text must be kept as sent: %q", c.Text)
	}

	// It survives being stored and read back.
	raw, _ := json.Marshal(c)
	back, err := Decode(raw)
	if err != nil || !back.TextEdited || back.Text != c.Text {
		t.Fatalf("decode lost the doctor's text: %v %+v", err, back)
	}

	// Dropping the flag renders the text from the fields again.
	edit.TextEdited = false
	again, _ := ApplyEdit(back, edit, 10)
	if again.TextEdited || !strings.Contains(again.Text, "Вид животного: Собака") {
		t.Fatalf("text should be rendered from fields: %q", again.Text)
	}
}

func TestApplyEditKeepsFieldsLeftOut(t *testing.T) {
	var edit Edit
	_ = json.Unmarshal([]byte(`{"sections":[{"fields":[{"key":"species","value":"Собака"}]}]}`), &edit)
	c, _ := ApplyEdit(baseCard(t), edit, 10)
	if !strings.Contains(c.Text, "Питомец: Муся") || !strings.Contains(c.Text, "Вид животного: Собака") {
		t.Fatalf("%s", c.Text)
	}
}

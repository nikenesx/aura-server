package card

// Template describes the fields of an appointment card. Templates will live in
// the database; until then VetAppointment is the only one.
type Template struct {
	ID       string
	Sections []Section
}

type Section struct {
	// Title is printed above the section in the text card; empty prints none.
	Title  string
	Fields []Field
}

type Field struct {
	// Key names the field in the JSON the model writes and the API returns.
	Key   string
	Title string
	// Hint tells the model what belongs in the field.
	Hint string
	// List fields hold numbered items, like prescriptions.
	List bool
	// Verbatim fields are names: every word must have been said in the cited
	// phrases. Other fields may be the model's summary of what was said.
	Verbatim bool
}

// VetAppointment follows the clinic's printed card: patient header,
// complaints and exam, then "Рекомендации и назначения".
var VetAppointment = Template{
	ID: "vet_appointment_v1",
	Sections: []Section{
		{
			Fields: []Field{
				{Key: "owner_name", Title: "Ф.И.О. владельца", Hint: "Фамилия, имя и отчество владельца животного, если их назвали.", Verbatim: true},
				{Key: "pet_name", Title: "Питомец", Hint: "Кличка животного.", Verbatim: true},
				{Key: "species", Title: "Вид животного", Hint: "Кошка, собака, кролик и т. п."},
				{Key: "age", Title: "Возраст", Hint: "Как назван: «9 мес.», «5 лет»."},
				{Key: "sex", Title: "Пол", Hint: "«Ж» или «М»; кастрация или стерилизация, если сказано."},
				{Key: "breed", Title: "Порода", Hint: "Порода, если её назвали. «б/п» (без породы) — если сказали прямо или если животное описано как беспородное, подобранное, дворовое."},
				{Key: "weight", Title: "Вес животного", Hint: "С единицами, как назван: «2,4 кг»."},
				{Key: "temperature", Title: "Температура животного", Hint: "Измеренная на приёме, в °C."},
			},
		},
		{
			Fields: []Field{
				{Key: "complaints", Title: "Жалобы", Hint: "С чем обратился владелец, по его словам."},
				{Key: "anamnesis", Title: "Анамнез", Hint: "Длительность и развитие симптомов, питание, содержание, вакцинация, обработки от паразитов, перенесённые болезни, препараты, которые животное уже получает."},
				{Key: "examination", Title: "Данные осмотра", Hint: "Что врач обнаружил при осмотре: общее состояние, слизистые, кожа, уши, пальпация, аускультация и т. д."},
			},
		},
		{
			Title: "Рекомендации и назначения",
			Fields: []Field{
				{Key: "diagnosis", Title: "Диагноз", Hint: "Диагноз или предварительный диагноз, как его сформулировал врач."},
				{Key: "differential_diagnosis", Title: "Дифференциальный диагноз", Hint: "Заболевания, которые врач назвал для исключения."},
				{Key: "prognosis", Title: "Прогноз", Hint: "Например, «благоприятный» или «осторожный»."},
				{Key: "tests", Title: "Рекомендованные исследования", Hint: "Анализы и диагностика, которые врач назначил или рекомендовал."},
				{
					Key:   "recommendations",
					Title: "Рекомендации",
					Hint:  "Каждое назначение отдельным пунктом: препарат с дозой, способом, кратностью и длительностью; обработка помещения; обработки от паразитов; уход; кормление; повторный приём.",
					List:  true,
				},
			},
		},
	},
}

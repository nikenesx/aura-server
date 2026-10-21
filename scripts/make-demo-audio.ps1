param(
    [string]$Out = "demo-appointment.m4a",
    # Repeat the dialog up to this many minutes (0 = once, about 5 minutes).
    [double]$Minutes = 0
)

# A synthetic vet appointment for trying the app without real recordings:
# Windows text-to-speech reads both parts, the owner's voice pitch-shifted.
# Good for checking the pipeline and the UI, not for judging recognition quality.

$ErrorActionPreference = "Stop"
Add-Type -AssemblyName System.Speech

$lines = @(
    "A|Здравствуйте, проходите, садитесь. Рассказывайте, что случилось.",
    "B|Здравствуйте. Кошку зовут Муся, ей девять месяцев. У неё залысины на голове и она постоянно трясёт головой и чешет уши.",
    "A|Как давно это началось?",
    "B|Залысины я заметила недели три назад, сначала маленькое пятно за ухом, потом стало больше. А уши чешет примерно дней десять.",
    "A|Чем кормите, где содержится, выходит ли на улицу?",
    "B|Кормим сухим кормом для котят, живёт в квартире, на улицу не выходит. Но мы её подобрали на улице в июне.",
    "A|Прививки и обработки от блох и глистов делали?",
    "B|Нет, ничего не делали, мы только недавно её взяли.",
    "A|Другие животные или дети в доме есть?",
    "B|Есть сын, ему шесть лет, они всё время вместе играют.",
    "A|Хорошо, давайте посмотрим. Сейчас взвесим. Вес два килограмма четыреста граммов.",
    "A|Температуру измерить не удалось, кошка сильно сопротивляется, отложим.",
    "A|Смотрю кожу. На голове и за левым ухом участки облысения с чешуйками, волос обламывается. Это характерно для лишая.",
    "A|Теперь уши. В обоих слуховых проходах коричневый налёт и покраснение, справа умеренно, слева сильнее.",
    "A|Слева барабанная перепонка не просматривается из-за отёка, есть болезненность при надавливании на основание уха.",
    "B|Это опасно? Сын может заразиться?",
    "A|Лишай заразен для человека, поэтому обработку дома нужно будет делать обязательно, и за ребёнком следите.",
    "A|Диагноз: дерматофития, двусторонний наружный бактериальный отит, левосторонний средний отит.",
    "A|Дифференциальный диагноз нужно провести по вирусу иммунодефицита и вирусу лейкоза кошек.",
    "A|Прогноз осторожный, потому что процесс уже распространённый и есть средний отит.",
    "A|Из исследований назначаю: цитологию в динамике, видеоотоскопию, общий анализ крови с лейкоформулой, тест на ВИК и ВЛК.",
    "A|Теперь по лечению. Тербинафин двести пятьдесят миллиграммов в таблетках, по одной трети таблетки внутрь с едой, один раз в день.",
    "A|Схема пульс-терапии: приём препарата через день, либо неделя через неделю.",
    "A|Важно: тербинафин может вызывать расстройство желудочно-кишечного тракта. Если появится рвота, диарея, отказ от еды или вялость, приём прекратить и приехать к нам.",
    "A|Преднизолон пять миллиграммов в таблетках, по половине таблетки внутрь через день, то есть раз в сорок восемь часов, ещё семь дней, потом отмена.",
    "A|Обработка внешней среды обязательна: Экоцид С по инструкции, хлорная известь десять процентов, раствор формалина десять процентов.",
    "A|Текстиль стирайте в машине, ковры пылесосьте, пылесборник выбрасывайте.",
    "A|Обработка от эктопаразитов: Стронгхолд или Адвокат, что-то одно по инструкции. От гельминтов Мильбемакс или Гельмимакс.",
    "A|Чистка ушей: тёплый физиологический раствор натрия хлорида ноль девять процентов или лосьон ПреОтик, один-три раза в неделю, объёмом три-восемь миллилитров, шприцем без иглы.",
    "A|Технику покажу: оттягиваете ухо от головы под углом сорок пять градусов, заливаете раствор, массируете основание уха тридцать-шестьдесят секунд, потом даёте кошке потрясти головой.",
    "B|А правое ухо тоже так чистить?",
    "A|Правое можно лосьоном, раз в два-четыре дня, объём пять-десять миллилитров, там перфорации нет.",
    "B|Поняла. Когда приходить повторно?",
    "A|Повторный приём через десять дней с результатами анализов. Если станет хуже, слух ухудшится или появится выделение из уха, приходите раньше.",
    "B|Хорошо, спасибо большое.",
    "A|Всего доброго, выздоравливайте."
)

if (-not (Get-Command ffmpeg -ErrorAction SilentlyContinue)) {
    throw "ffmpeg is not on PATH"
}
$voice = New-Object System.Speech.Synthesis.SpeechSynthesizer
$russian = $voice.GetInstalledVoices() | Where-Object { $_.VoiceInfo.Culture.Name -eq "ru-RU" } | Select-Object -First 1
if (-not $russian) {
    throw "no Russian text-to-speech voice is installed (Settings > Time & language > Speech)"
}
$voice.SelectVoice($russian.VoiceInfo.Name)

$tmp = Join-Path ([IO.Path]::GetTempPath()) ("aura-demo-" + [guid]::NewGuid())
New-Item -ItemType Directory $tmp | Out-Null
try {
    $list = @()
    $i = 0
    foreach ($line in $lines) {
        $speaker, $text = $line.Split("|", 2)
        $raw = Join-Path $tmp "raw$i.wav"
        $voice.SetOutputToWaveFile($raw)
        $voice.Speak($text)
        $voice.SetOutputToNull()

        # Owner: lower pitch so diarization hears two people; a pause after each line.
        $filter = if ($speaker -eq "B") {
            "aresample=22050,asetrate=22050*0.78,aresample=16000,atempo=1.1,apad=pad_dur=0.7"
        } else {
            "aresample=16000,apad=pad_dur=0.5"
        }
        $clip = Join-Path $tmp "clip$i.wav"
        & ffmpeg -hide_banner -loglevel error -y -i $raw -af $filter -ac 1 -ar 16000 -c:a pcm_s16le $clip
        if ($LASTEXITCODE -ne 0) { throw "ffmpeg failed on line $i" }
        $list += "file '$($clip -replace '\\', '/')'"
        $i++
    }
    $listPath = Join-Path $tmp "list.txt"
    [IO.File]::WriteAllLines($listPath, $list)
    $once = Join-Path $tmp "once.wav"
    & ffmpeg -hide_banner -loglevel error -y -f concat -safe 0 -i $listPath -c:a pcm_s16le $once
    if ($LASTEXITCODE -ne 0) { throw "ffmpeg concat failed" }

    $loop = @()
    if ($Minutes -gt 0) {
        $loop = @("-stream_loop", "-1")
    }
    $limit = @()
    if ($Minutes -gt 0) {
        $limit = @("-t", [string]([int]($Minutes * 60)))
    }
    & ffmpeg -hide_banner -loglevel error -y @loop -i $once @limit -ac 1 -ar 16000 $Out
    if ($LASTEXITCODE -ne 0) { throw "ffmpeg encode failed" }
    Write-Host "demo appointment written to $Out"
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}

FROM tinkoffcreditsystems/t-one:0.1.0

USER root
RUN apt-get update \
	&& apt-get install -y --no-install-recommends ffmpeg wget ca-certificates \
	&& rm -rf /var/lib/apt/lists/*

# Base image venv is created without pip. Install sherpa-onnx into that venv.
RUN PY=/venv/bin/python \
	&& if [ ! -x "$PY" ]; then PY=python3; fi \
	&& wget -qO /tmp/get-pip.py https://bootstrap.pypa.io/get-pip.py \
	&& "$PY" /tmp/get-pip.py \
	&& "$PY" -m pip install --no-cache-dir sherpa-onnx \
	&& rm -f /tmp/get-pip.py

RUN mkdir -p /models \
	&& wget -qO /models/speaker_embedding.onnx \
		https://github.com/k2-fsa/sherpa-onnx/releases/download/speaker-recongition-models/3dspeaker_speech_campplus_sv_en_voxceleb_16k.onnx

WORKDIR /app
COPY asr/app.py asr/audio.py asr/vad.py asr/diarize.py /app/
COPY asr/models/silero_vad.onnx /models/silero_vad.onnx

USER python
EXPOSE 8090

HEALTHCHECK --interval=10s --timeout=5s --start-period=180s --retries=18 \
	CMD python -c "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8090/health')"

ENTRYPOINT ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "8090"]

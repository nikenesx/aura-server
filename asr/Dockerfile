FROM tinkoffcreditsystems/t-one:0.1.0

USER root
RUN apt-get update \
	&& apt-get install -y --no-install-recommends ffmpeg \
	&& rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY asr/app.py asr/audio.py /app/

USER python
EXPOSE 8090

HEALTHCHECK --interval=10s --timeout=5s --start-period=180s --retries=18 \
	CMD python -c "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8090/health')"

ENTRYPOINT ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "8090"]

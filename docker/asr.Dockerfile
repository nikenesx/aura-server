# syntax=docker/dockerfile:1

FROM nvidia/cuda:12.8.1-cudnn-runtime-ubuntu24.04

ENV DEBIAN_FRONTEND=noninteractive \
    PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PATH="/opt/venv/bin:$PATH" \
    HOME=/cache \
    HF_HOME=/cache/huggingface \
    HUGGINGFACE_HUB_CACHE=/cache/huggingface \
    TORCH_HOME=/cache/torch \
    XDG_CACHE_HOME=/cache \
    NVIDIA_VISIBLE_DEVICES=all \
    NVIDIA_DRIVER_CAPABILITIES=compute,utility

RUN apt-get update && apt-get install -y --no-install-recommends \
        python3 \
        python3-venv \
        python3-dev \
        python3-pip \
        ffmpeg \
        git \
        ca-certificates \
        libsndfile1 \
        build-essential \
    && rm -rf /var/lib/apt/lists/* \
    && python3 -m venv /opt/venv \
    && pip install --upgrade pip

COPY asr/requirements.txt /tmp/requirements.txt
RUN --mount=type=cache,target=/root/.cache/pip \
    pip install torch torchaudio --index-url https://download.pytorch.org/whl/cu128 \
    && pip install -r /tmp/requirements.txt

WORKDIR /app
COPY asr/*.py /app/

EXPOSE 8090
CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "8090"]

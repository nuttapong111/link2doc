FROM python:3.12-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends tesseract-ocr tesseract-ocr-tha tesseract-ocr-eng \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY app.py .
ENV DATA_DIR=/data
# Railway sets PORT; bind on IPv6 too so the private network (*.railway.internal) can reach it.
CMD ["sh", "-c", "uvicorn app:app --host :: --port ${PORT:-8000} --workers ${WEB_CONCURRENCY:-2}"]

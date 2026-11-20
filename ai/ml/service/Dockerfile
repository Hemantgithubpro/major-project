FROM python:3.12-slim
WORKDIR /app
COPY requirements.txt /app/requirements.txt
RUN pip install --no-cache-dir -r /app/requirements.txt
COPY features.py /app/features.py
COPY service /app/service
COPY models /app/models
ENV MODEL_DIR=/app/models
CMD ["uvicorn", "service.app:app", "--host", "0.0.0.0", "--port", "9000"]

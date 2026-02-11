FROM python:3.12-alpine
RUN pip install --no-cache-dir paho-mqtt requests
WORKDIR /app
COPY app.py /app/app.py
CMD ["python", "-u", "/app/app.py"]
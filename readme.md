# Prueba Modbus TCP – Guía paso a paso (modo humano)

Este README explica **qué hacer, cuándo hacerlo y por qué**, sin asumir que recuerdas nada.
Si sigues los pasos **en orden**, el sistema funciona.

---

## 📦 Qué es este proyecto (muy corto)

Este proyecto levanta:

- 🧠 **Modbus TCP MASTER** (server)
- 🤖 **Modbus TCP SLAVES** (clients)
- 📡 **MQTT (Mosquitto)** como bus de mensajes

Se puede ejecutar:
- sin Docker (binarios Go)
- con Docker local
- con Docker Compose
- en un servidor remoto por SSH

---

## 📁 Estructura importante del repo

```
Prueba-Modbus-TCP/
├── server/          # Código Go del MASTER
├── client/          # Código Go del SLAVE
├── Dockerfile       # Build de imágenes
├── docker-compose.yml
├── Makefile
└── README.md
```

---

## 🛠️ Requisitos

### Local (PC)
- Go 1.22+
- Docker
- Docker Compose (plugin: `docker compose`)
- Make

### Servidor (SSH)
- Docker
- Docker Compose plugin
- Carpeta fija:

```
/opt/pruebatcp1
```

(Eso lo crea el `playbook.yml`, **no inventes otra ruta**)

---

## 🧠 REGLA DE ORO (léela)

> **Si cambias código Go → hay que recompilar y reconstruir imagen**  
> **Si cambias Dockerfile / Makefile → hay que reconstruir imagen**  
> **Si cambias docker-compose.yml → NO hace falta rebuild**

---

## 1️⃣ Compilar binarios Go (sin Docker)

Desde la raíz del repo:

```bash
make
```

Esto:
- compila `server`
- compila `client`
- deja binarios listos

---

## 2️⃣ Ejecutar SIN Docker (modo simple)

### Terminal 1 – Mosquitto

```bash
mosquitto -p 1883
```

### Terminal 2 – Server

```bash
./server
```

### Terminal 3 – Clientes

```bash
./client 1
./client 2
```

---

## 3️⃣ Probar rápido con curl

### Ver estado

```bash
curl http://127.0.0.1:8080/stats
```

### Conectar slave

```bash
curl "http://127.0.0.1:8080/connect?id=1&port=5021"
```

---

## 4️⃣ Ejecutar con Docker (local)

### Construir imagen

```bash
docker build -t galvarez0/pruebatcp1:modbus-server .
```

### Mosquitto

```bash
docker run -d \
  --name mosquitto \
  -p 1883:1883 \
  eclipse-mosquitto:2
```

### Server

```bash
docker run -d \
  --name modbus-server \
  --network host \
  -e MQTT_BROKER=tcp://127.0.0.1:1883 \
  galvarez0/pruebatcp1:modbus-server
```

---

## 5️⃣ Ejecutar con Docker Compose (LOCAL o SSH)

```bash
docker compose up -d
```

Ver logs:

```bash
docker compose logs -f
```

Parar todo:

```bash
docker compose down
```

---

## 6️⃣ Ejecutar en SERVIDOR por SSH (importante)

### Conectarte

```bash
ssh root@<IP>
```

### Ir a la carpeta correcta

```bash
cd /opt/pruebatcp1
```

### Levantar servicios

```bash
docker compose up -d
```

### Ver logs

```bash
docker compose logs -f
```

---

## 7️⃣ Qué hacer cuando CAMBIAS ALGO

### 🔁 Cambias archivos `.go`

1. Compila y construye imágenes:

```bash
make
make provision
```

2. En el servidor:

```bash
docker compose pull
docker compose up -d
```

---

### 🔁 Cambias el `Makefile`

👉 Igual que Go

```bash
make
make provision
```

Luego en servidor:

```bash
docker compose pull
docker compose up -d
```

---

### 🔁 Cambias `docker-compose.yml`

👉 **NO recompilas**

Solo:

```bash
docker compose up -d
```

---

## 8️⃣ Subir imágenes a Docker Hub

👉 **SIEMPRE con:**

```bash
make provision
```

Eso:
- build
- tag
- push

No hagas `docker push` a mano.

---

## 9️⃣ Errores comunes (y qué significan)

### ❌ `connection refused :1883`

➡️ Mosquitto no estaba listo
➡️ Solución: docker-compose + depends_on

---

### ❌ `/stats` devuelve `{}`

➡️ No hay slaves conectados
➡️ Normal

---

### ❌ Server entra en restart loop

➡️ MQTT no estaba disponible aún
➡️ Solucionado con retry + compose

---

## 🧾 Resumen ultra corto

| Acción | Qué hacer |
|------|----------|
| Cambio Go | make → make provision → pull + up |
| Cambio compose | up -d |
| Local rápido | binarios |
| Producción | docker compose |

---

🟢 **Si sigues este README sin improvisar, funciona.**
🛑 **Si improvisas rutas, nombres o comandos, no.**


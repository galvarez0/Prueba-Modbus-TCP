# Prueba Modbus TCP
Guía de instalación, compilación, despliegue y verificación

Este documento describe cómo instalar, compilar, desplegar y verificar el proyecto **Prueba Modbus TCP**, tanto en local como en un servidor remoto usando Docker, Docker Compose, Ansible y GitHub Actions.

---

## 1. Descripción general

El proyecto implementa un sistema compuesto por:

- Modbus TCP MASTER (server)
- Modbus TCP SLAVES (clients)
- Mosquitto (MQTT)
- API HTTP para control y pruebas
- Caddy como reverse proxy HTTPS
- Docker / Docker Compose para despliegue
- Ansible para provisión remota
- GitHub Actions para build y push de imágenes

---

## 2. Requisitos

### Local
- Go 1.22+
- Docker
- Docker Compose (plugin `docker compose`)
- Make
- curl
- jq (opcional, para pretty print de JSON)

### Servidor remoto
- Ubuntu
- Acceso root por SSH mediante clave
- Dominio apuntando al servidor (ej. dm-server-test.datamecanic.com)
- Ruta fija de despliegue: `/opt/pruebatcp1`

---

## 3. Compilación local de binarios (sin Docker)

### Server
```bash
cd server
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o modbus-server .
```

### Client
```bash
cd client
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o modbus-client .
```

Estos comandos generan binarios estáticos compatibles con Alpine Linux.

---

## 4. Ejecución local sin Docker (modo desarrollo)

### Mosquitto
```bash
mosquitto -p 1883
```

### Server
```bash
./modbus-server
```

### Clients
```bash
./modbus-client 1
./modbus-client 2
```

---

## 5. Ejecución con Docker Compose (local o servidor)

```bash
docker compose up -d
docker compose logs -f
```

Detener el stack:
```bash
docker compose down
```

---

## 6. Acceso SSH al servidor remoto

El acceso al servidor se realiza exclusivamente mediante clave SSH:

```bash
ssh root@138.197.101.64 -i mykey
```

Una vez conectado, todo el despliegue ocurre en:

```bash
cd /opt/pruebatcp1
```

---

## 7. Recompilación y redeploy en el servidor remoto

### Rebuild completo del server usando Docker (multi-stage)

```bash
cd /opt/pruebatcp1
docker compose build --no-cache modbus-server
docker compose up -d --force-recreate modbus-server
```

### Ver logs del server
```bash
docker compose logs -f modbus-server
```

---

## 8. Validación de la API HTTP

### 8.1 Estado inicial del sistema

```bash
curl -sS "https://dm-server-test.datamecanic.com/stats"
```

Salida esperada cuando no hay slaves conectados:
```json
{}
```

### 8.2 Conexión de slaves

```bash
curl "https://dm-server-test.datamecanic.com/connect?id=1&host=modbus-client-1&port=5021"
curl "https://dm-server-test.datamecanic.com/connect?id=2&host=modbus-client-2&port=5021"
```

### 8.3 Verificación de estado (JSON)

```bash
curl -sS "https://dm-server-test.datamecanic.com/stats"
```

### 8.4 Pretty print del JSON (opcional)

```bash
curl -sS "https://dm-server-test.datamecanic.com/stats" | jq .
```

---

## 9. Producción de tráfico Modbus (prueba de flujo)

Secuencia recomendada de pruebas:

1. Verificar estado inicial:
```bash
curl -sS "https://dm-server-test.datamecanic.com/stats"
```

2. Conectar slave 1:
```bash
curl "https://dm-server-test.datamecanic.com/connect?id=1&host=modbus-client-1&port=5021"
```

3. Conectar slave 2:
```bash
curl "https://dm-server-test.datamecanic.com/connect?id=2&host=modbus-client-2&port=5021"
```

4. Verificar estado final:
```bash
curl -sS "https://dm-server-test.datamecanic.com/stats"
```

### Generación de tráfico Modbus

```bash
curl "https://dm-server-test.datamecanic.com/test?id=1"
curl "https://dm-server-test.datamecanic.com/read?id=1&addr=0&qty=1"
curl "https://dm-server-test.datamecanic.com/write?id=1&addr=0&values=10,11,12"
```

---

## 10. Ansible

### Bootstrap (una sola vez)
```bash
ansible-playbook -i ansible/inventory.ini ansible/bootstrap.yml
```

### Despliegue normal
```bash
make provision
```

Este comando:
- Sincroniza archivos
- Actualiza imágenes
- Levanta el stack completo

---

## 11. GitHub Actions

El repositorio incluye un workflow de **Build & Docker Push** que:

- Compila server y client
- Construye imágenes Docker
- Publica las imágenes en Docker Hub

### Secrets requeridos

- `DOCKERHUB_USERNAME`
- `DOCKERHUB_TOKEN`

Ruta:
```
Repository → Settings → Security → Secrets and variables → Actions
```

---

## 12. Reglas importantes

- Cambios en archivos `.go` requieren recompilar binarios y reconstruir imágenes
- Cambios en Dockerfile requieren rebuild de imágenes
- Cambios en `docker-compose.yml` no requieren recompilar binarios
- El despliegue remoto siempre ocurre en `/opt/pruebatcp1`

---

## 13. Errores comunes

- `/stats` devuelve `{}`: no hay slaves conectados
- `/stats` vacío con slaves conectados: binario no recompilado
- `connection refused`: Mosquitto no está healthy
- Error DNS Docker: clientes no levantados en la red

---

## 14. Resumen rápido

| Acción | Comando |
|------|--------|
| Acceso SSH | `ssh root@138.197.101.64 -i mykey` |
| Rebuild server | `docker compose build --no-cache modbus-server` |
| Redeploy server | `docker compose up -d --force-recreate modbus-server` |
| Ver logs | `docker compose logs -f modbus-server` |
| Estado | `GET /stats` |

---

Fin del documento.


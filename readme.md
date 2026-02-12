# Prueba Modbus TCP

Guía completa de instalación, despliegue y verificación del stack Modbus TCP + ChirpStack (LoRaWAN).

---

## 1. Descripción general

Este proyecto implementa un stack de pruebas y desarrollo que integra:

- Modbus TCP Server (master) con API HTTP
- Múltiples Modbus TCP Clients (slaves)
- Mosquitto (broker MQTT)
- ChirpStack LoRaWAN Network Server (multi-región)
- Caddy como reverse proxy HTTPS
- Docker y Docker Compose
- Ansible para provisión remota
- GitHub Actions para build y push de imágenes

El despliegue estándar se realiza en `/opt/pruebatcp1`.

---

## 2. Requisitos

### Local
- Go 1.22+
- Docker
- Docker Compose (plugin `docker compose`)
- Make
- curl
- jq (opcional)

### Servidor remoto
- Ubuntu
- Acceso root por SSH
- Dominio apuntando al servidor
- Docker + Docker Compose

---

## 3. Compilación local (sin Docker)

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

## 4. Ejecución local sin Docker

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

## 5. Ejecución con Docker Compose

```bash
docker compose up -d
docker compose ps
```

### Logs (solo recientes)
```bash
docker compose logs -f --since 5s modbus-server
docker compose logs -f --since 5s mosquitto
```

### Parada
```bash
docker compose down
```

---

## 6. Acceso SSH al servidor

```bash
ssh root@138.197.101.64 -i mykey
```

Una vez conectado, todo el despliegue ocurre en:

```bash
cd /opt/pruebatcp1
```

---

## 7. Rebuild y redeploy remoto

```bash
docker compose build --no-cache modbus-server
docker compose up -d --force-recreate modbus-server
docker compose logs -f --since 5s modbus-server
```

---

## 8. API HTTP Modbus

### Estado
```bash
curl -sS "https://dm-server-test.datamecanic.com/stats"
```

Salida esperada cuando no hay slaves conectados:
```json
{}
```

### Conectar clientes
```bash
curl "https://dm-server-test.datamecanic.com/connect?id=1&host=modbus-client-1&port=5021"
curl "https://dm-server-test.datamecanic.com/connect?id=2&host=modbus-client-2&port=5021"
```

### Tráfico Modbus
```bash
curl -sS "https://dm-server-test.datamecanic.com/stats"
```

### 8.4 Pretty print del JSON (opcional)

```bash
curl -sS "https://dm-server-test.datamecanic.com/stats" | jq .
```

---

## 9. ChirpStack / LoRaWAN

### Regiones activas
- US915 (default)
- EU868
- AU915

Definidas en:
```
chirpstack/configuration/chirpstack/chirpstack.toml
```

### Reinicio tras cambios de configuración
```bash
docker compose up -d --force-recreate chirpstack
docker compose logs -f --since 5s chirpstack
```

### Logs esperados correctos
- Connecting to MQTT broker tcp://mosquitto:1883
- Setting up gateway backend for region ...
- Sin errores Connection refused

Si aparecen errores:
- Revisar que ninguna región apunte a localhost / 127.0.0.1
- Revisar que no haya regiones habilitadas sin su region_*.toml

---

## 10. UI de ChirpStack

Accesible vía Caddy en el subdominio configurado.
Usuario inicial:
- admin / admin

---

## 11. Ansible

### Bootstrap (una vez)
```bash
ansible-playbook -i ansible/inventory.ini ansible/bootstrap.yml
```

### Despliegue completo
```bash
make provision
```

---

## 12. GitHub Actions

Pipeline automático para:
- Build de server y client
- Build de imágenes Docker
- Push a Docker Hub

Secrets requeridos:
- DOCKERHUB_USERNAME
- DOCKERHUB_TOKEN

---

## 13. Problemas comunes

- Logs viejos: usar siempre `--since`
- MQTT refused: revisar backend MQTT de regiones
- Cambios no aplican: reiniciar contenedor correspondiente
- HTTPS caído: validar Caddyfile y recargar Caddy

---

## 14. Comandos rápidos

```bash
docker compose up -d --force-recreate chirpstack
docker compose logs -f --since 5s chirpstack

docker compose logs -f --since 5s modbus-server
docker compose ps
```
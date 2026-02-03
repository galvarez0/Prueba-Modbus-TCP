# Prueba Modbus TCP
Guía de instalación, compilación y ejecución

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

### Servidor remoto
- Ubuntu
- Acceso root
- Dominio apuntando al servidor (ej. dm-server-test.datamecanic.com)
- Ruta fija de despliegue: `/opt/pruebatcp1`

---

## 3. Compilación local (sin Docker)

```bash
make
```

Compila server y client como binarios locales.

---

## 4. Ejecución local sin Docker

### Mosquitto
```bash
mosquitto -p 1883
```

### Server
```bash
./server
```

### Clients
```bash
./client 1
./client 2
```

---

## 5. Ejecución con Docker Compose (local o servidor)

```bash
docker compose up -d
docker compose logs -f
```

Detener:
```bash
docker compose down
```

---

## 6. Validación por navegador y curl

### Estado del server
```bash
curl https://dm-server-test.datamecanic.com/stats
```

### Conectar slaves
```bash
curl "https://dm-server-test.datamecanic.com/connect?id=1&host=modbus-client-1&port=5021"
curl "https://dm-server-test.datamecanic.com/connect?id=2&host=modbus-client-2&port=5021"
```

### Verificar
```bash
curl https://dm-server-test.datamecanic.com/stats
```

---

## 7. Producción de tráfico Modbus (pruebas)

El tráfico Modbus no se genera automáticamente.

Ejemplos:
```bash
curl "https://dm-server-test.datamecanic.com/test?id=1"
curl "https://dm-server-test.datamecanic.com/read?id=1&addr=0&qty=1"
curl "https://dm-server-test.datamecanic.com/write?id=1&addr=0&values=10,11,12"
```

---

## 8. Despliegue en servidor remoto (SSH)

```bash
ssh root@IP_DEL_SERVIDOR
cd /opt/pruebatcp1
docker compose up -d
docker compose logs -f
```

---

## 9. Ansible

### Bootstrap (una sola vez)
```bash
ansible-playbook -i ansible/inventory.ini ansible/bootstrap.yml
```

### Despliegue normal
```bash
make provision
```

Copia archivos, actualiza imágenes y levanta el stack.

---

## 10. GitHub Actions

El repositorio incluye un workflow de **Build & Docker Push** que:

- Compila server y client
- Construye imágenes Docker
- Publica las imágenes en Docker Hub

### Secrets requeridos

El workflow necesita los siguientes secrets configurados en GitHub:

- `DOCKERHUB_USERNAME`
- `DOCKERHUB_TOKEN` (o password)

Ruta para configurarlos:

```
Repository → Settings → Security → Secrets and variables → Actions
```

### Fallos en build-and-push

Si el workflow falla durante el paso de login o push:

1. Ir a:
   ```
   Settings → Security → Secrets and variables → Actions
   ```
2. Verificar que:
   - El usuario es correcto
   - El token/password no esté expirado
3. Guardar nuevamente los secrets
4. Re-ejecutar el workflow desde la pestaña **Actions**

No es necesario modificar el workflow para este tipo de fallos.

---

## 11. Reglas importantes

- Cambios en archivos `.go` requieren recompilar y reconstruir imágenes
- Cambios en Dockerfile o Makefile requieren `make provision`
- Cambios en docker-compose.yml no requieren recompilar binarios
- Todo el despliegue remoto ocurre en `/opt/pruebatcp1`

---

## 12. Errores comunes

- `/stats` devuelve `{}`: no hay slaves conectados
- Server reinicia: Mosquitto no estaba listo
- `connection refused`: revisar healthcheck de Mosquitto

---

## 13. Resumen rápido

| Acción | Comando |
|------|--------|
Compilar | make |
Build + push + deploy | make provision |
Ver logs | docker compose logs -f |
Producción | HTTP / MQTT |

---

Fin del documento.

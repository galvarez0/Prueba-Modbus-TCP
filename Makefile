.PHONY: \
	provision \
	build-server build-client \
	build-dockerserver build-dockerclient \
	build-images \
	push-images \
	all

APP_NAME=pruebatcp1
REGISTRY=galvarez0

SERVER_BIN=server/modbus-server
CLIENT_BIN=client/modbus-client

SERVER_IMG=$(REGISTRY)/$(APP_NAME):modbus-server
CLIENT_IMG=$(REGISTRY)/$(APP_NAME):modbus-client

build-server:
	cd server && go build -o modbus-server server.go

build-client:
	cd client && go build -o modbus-client client.go

build-dockerserver: build-server
	docker build -f server/Dockerfile.server -t $(SERVER_IMG) server

build-dockerclient: build-client
	docker build -f client/Dockerfile.client -t $(CLIENT_IMG) client

build-images: build-dockerserver build-dockerclient

push-images:
	docker push $(SERVER_IMG)
	docker push $(CLIENT_IMG)

provision:
	ansible-playbook -i ansible/inventory.ini ansible/playbook.yml

all: build-images provision
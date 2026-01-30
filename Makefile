.PHONY: bootstrap provision build-server build-client build-images push

APP_NAME=pruebatcp1
REGISTRY=galvarez0

SERVER_BIN=server/modbus-server
CLIENT_BIN=client/modbus-client

SERVER_IMG=$(REGISTRY)/$(APP_NAME):modbus-server
CLIENT_IMG=$(REGISTRY)/$(APP_NAME):modbus-client


bootstrap:
	ansible-playbook -i ansible/inventory.ini ansible/bootstrap.yml

provision:
	ansible-playbook -i ansible/inventory.ini ansible/playbook.yml


build-server:
	cd server && go build -o modbus-server .

build-client:
	cd client && go build -o modbus-client .


build-images: build-server build-client
	docker build -t $(SERVER_IMG) -f server/Dockerfile.server server
	docker build -t $(CLIENT_IMG) -f client/Dockerfile.client client


push:
	docker push $(SERVER_IMG)
	docker push $(CLIENT_IMG)

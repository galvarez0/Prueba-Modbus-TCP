.PHONY: bootstrap provision provision-sync provision-remote-up provision-remote-build build-server build-client build-images push

APP_NAME=pruebatcp1
REGISTRY=galvarez0

SERVER_BIN=server/modbus-server
CLIENT_BIN=client/modbus-client

SERVER_IMG=$(REGISTRY)/$(APP_NAME):modbus-server
CLIENT_IMG=$(REGISTRY)/$(APP_NAME):modbus-client

REMOTE_HOST=138.197.101.64
REMOTE_USER=root
REMOTE_DIR=/opt/$(APP_NAME)

SSH_KEY ?= ~/.ssh/id_ed25519

SSH_OPTS :=
ifneq ("$(wildcard $(SSH_KEY))","")
SSH_OPTS += -i $(SSH_KEY)
endif
SSH_OPTS += -o StrictHostKeyChecking=accept-new

SSH = ssh $(SSH_OPTS) $(REMOTE_USER)@$(REMOTE_HOST)
RSYNC = rsync -avz --delete -e "ssh $(SSH_OPTS)"

RSYNC_EXCLUDES= \
	--exclude ".git/" \
	--exclude ".github/" \
	--exclude "node_modules/" \
	--exclude "**/.DS_Store" \
	--exclude "**/*.log"

bootstrap:
	ansible-playbook -i ansible/inventory.ini ansible/bootstrap.yml

provision-sync:
	$(RSYNC) $(RSYNC_EXCLUDES) ./ $(REMOTE_USER)@$(REMOTE_HOST):$(REMOTE_DIR)/

provision-remote-up:
	$(SSH) 'cd $(REMOTE_DIR) && docker compose up -d --pull always --remove-orphans'
	$(SSH) 'cd $(REMOTE_DIR) && docker compose ps'

provision-remote-build:
	$(SSH) 'cd $(REMOTE_DIR) && docker compose build --no-cache modbus-server'
	$(SSH) 'cd $(REMOTE_DIR) && docker compose up -d --force-recreate modbus-server --remove-orphans'
	$(SSH) 'cd $(REMOTE_DIR) && docker compose ps'

provision: provision-sync bootstrap
	ansible-playbook -i ansible/inventory.ini ansible/playbook.yml
	$(MAKE) provision-remote-up


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
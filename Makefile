IMAGE=registry.nkit.top/openclaw/github-webhook-relay
TAG?=latest

.PHONY: build push build-push run logs clean test

build:
	docker build -t $(IMAGE):$(TAG) .

push:
	docker push $(IMAGE):$(TAG)

build-push:
	docker build -t $(IMAGE):$(TAG) .
	docker push $(IMAGE):$(TAG)

run:
	docker compose up -d

logs:
	docker logs -f github-webhook-relay

clean:
	docker compose down

test:
	go test ./... -v -count=1

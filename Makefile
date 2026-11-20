.PHONY: up down demo demo-final logs ps smoke

up:
	docker compose up -d --build
	@echo "waiting for services..."; sleep 8; docker compose ps

down:
	docker compose down

ps:
	docker compose ps

logs:
	docker compose logs -f --tail=100

smoke:
	@./scripts/smoke.sh

demo:
	@./scripts/demo.sh

demo-final:
	@./scripts/demo-final.sh

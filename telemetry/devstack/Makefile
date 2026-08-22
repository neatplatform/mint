.PHONY: up
up:
	@podman compose -f compose.yaml up -d

.PHONY: list
list:
	@podman compose -f compose.yaml ps --format "table {{.ID}}\t{{.Image}}\t{{.Status}}\t{{.Names}}"

.PHONY: down
down:
	@podman compose -f compose.yaml down

.PHONY: logs
logs:
	@containers=$$(podman compose -f compose.yaml ps --format "{{.Names}}"); \
	 printf "🐳 Containers:\n\n"; \
	 PS3=$$'\n👉 Select a container by number: '; \
	 select container in $$containers; do \
	   if [ -n "$$container" ]; then \
	     echo; \
	     podman container logs --follow $$container; \
	     break; \
	   else \
	     printf "❌ Invalid selection. Try again.\n"; \
	   fi; \
	 done

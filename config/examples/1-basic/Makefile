.PHONY: run
run:
	go build -o app && \
	export LOG_LEVEL=info && \
	export TIMEOUT=4m && \
	export ENDPOINTS_FILE=$(shell pwd)/endpoints && \
	/bin/echo -n "service-1:8080,service-2:8080,service-3:8080" > endpoints && \
	./app --port 3000 && \
	rm app endpoints

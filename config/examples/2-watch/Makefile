.PHONY: run
run:
	go build -o app && \
	/bin/echo -n "Warn" > log_level && \
	export LOG_LEVEL_FILE="$(shell pwd)/log_level" && \
	./app

.PHONY: debug
debug:
	/bin/echo -n "Debug" > log_level

.PHONY: info
info:
	/bin/echo -n "Info" > log_level

.PHONY: warn
warn:
	/bin/echo -n "Warn" > log_level

.PHONY: error
error:
	/bin/echo -n "Error" > log_level

.PHONY: clean
clean:
	rm app log_level

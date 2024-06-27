.PHONY: run clean

# Target to run the application
run:
	go run main.go
	$(MAKE) clean

# Target to clean up the log directory
clean:
	rm -rf deployment-logs

lint:
	golangci-lint run
# cdn-sim — Phase 1 modeled transport CDN simulator
.PHONY: build test test-short bench run-modeled sweep lint clean \
        certs docker-build docker-up docker-down run-emulated validate \
        full-suite analysis

BIN := bin/cdnsim

build:
	@mkdir -p bin
	go build -o $(BIN) ./cmd/cdnsim

test:
	go test ./... -race -count=1

test-short:
	go test ./... -short -count=1

bench:
	go test ./... -bench=. -run=^$$ -benchmem

run-modeled: build
	$(BIN) run --config configs/reproduce_35pct.yaml

baseline: build
	$(BIN) run --config configs/baseline.yaml

lossy: build
	$(BIN) run --config configs/lossy.yaml

mobile: build
	$(BIN) run --config configs/mobile_3g.yaml

satellite: build
	$(BIN) run --config configs/satellite.yaml

sweep: build
	@for cfg in configs/*.yaml; do \
	  echo "==> $$cfg"; \
	  $(BIN) run --config $$cfg || exit 1; \
	done

lint:
	gofmt -l .
	go vet ./...

clean:
	rm -rf bin results

# Phase 2: Emulated mode targets

certs:
	bash docker/certs/generate.sh

docker-build: certs
	docker compose -f docker/docker-compose.yml build

docker-up: docker-build
	docker compose -f docker/docker-compose.yml up -d
	@sleep 3
	@docker compose -f docker/docker-compose.yml ps

docker-down:
	docker compose -f docker/docker-compose.yml down -v

run-emulated: docker-up
	bash scripts/netem/apply_topology.sh asia_deployment || true
	docker compose -f docker/docker-compose.yml exec -T client \
	    /app/cdnsim run --config /configs/emulated_lossy.yaml --output-dir /results/emulated_lossy
	@echo "Results in results/emulated_lossy/"

validate: build
	bin/cdnsim run --config configs/reproduce_35pct.yaml --output-dir results/reproduce_35pct
	$(MAKE) run-emulated
	@echo "Cross-validation: see results/emulated_lossy/cross_validation.json (if produced)"

analysis:
	@which python3 >/dev/null || { echo "python3 required"; exit 1; }
	python3 scripts/analysis/compare.py results/reproduce_35pct

full-suite:
	@echo "=== Phase 1: Modeled experiments ==="
	$(MAKE) run-modeled
	@echo "=== Phase 2: Emulated validation ==="
	$(MAKE) run-emulated
	@echo "=== Analysis ==="
	$(MAKE) analysis
	@echo "=== Complete ==="

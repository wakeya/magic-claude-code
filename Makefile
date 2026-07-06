.PHONY: build run test clean icon docker docker-run docker-stop

# 默认后端地址
DEFAULT_BACKEND ?= https://open.bigmodel.cn/api/anthropic

build:
	CGO_ENABLED=0 go build -o bin/mcc ./cmd/server

run:
	go run ./cmd/server

test:
	go test -v -race -coverprofile=coverage.out ./...

clean:
	rm -rf bin/ coverage.out

ICON_SRC ?= internal/frontend/public/logo-login.png
ICON_ICO := cmd/server/icon.ico
ICON_SYSO := cmd/server/icon_windows.syso
ICON_FIT ?= 230

# icon regenerates the Windows app icon from ICON_SRC (default: logo-login.png).
# Trims transparent margins and fits the content to ICON_FIT px on a 256 canvas
# (default 230 ≈ 90%, leaving a 5% safety margin so edges aren't clipped by the
# taskbar/start-menu). Requires ImageMagick. The .syso is committed, so only run
# when the logo changes. Override with: make icon ICON_SRC=foo.png ICON_FIT=240
icon:
	@command -v convert >/dev/null 2>&1 || { echo "error: ImageMagick (convert) is required"; exit 1; }
	@test -f "$(ICON_SRC)" || { echo "error: source image not found: $(ICON_SRC)"; exit 1; }
	@echo "==> Generating icon (trim + fit $(ICON_FIT)px on 256 canvas): $(ICON_SRC) -> $(ICON_ICO)"
	convert "$(ICON_SRC)" -background none -trim +repage \
		-resize $(ICON_FIT)x$(ICON_FIT) \
		-gravity center -extent 256x256 \
		-define icon:auto-resize=256,128,64,48,32,16 "$(ICON_ICO)"
	@echo "==> Compiling Windows resource: $(ICON_ICO) -> $(ICON_SYSO)"
	go run github.com/akavel/rsrc@latest -ico "$(ICON_ICO)" -arch amd64 -o "$(ICON_SYSO)"
	@echo "==> Done. Rebuild via 'make build' (Windows) or release.sh to pick up the new icon."

docker:
	docker build -t magic-claude-code .

docker-run:
	docker compose up -d

docker-stop:
	docker compose down

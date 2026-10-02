# bluetag-go — B037 价签写卡服务 (ACR122U)
# Windows: 无 cgo, make build 即可
# Linux:   需 cgo (gcc + pkg-config + libpcsclite-dev), make build-linux
# 用法: make build / build-linux / run / test / vet / fmt / clean

GO      ?= go
APP     := bluetag-go
BIN     := bin
EXT     := $(shell $(GO) env GOEXE)
TARGET  := $(BIN)/$(APP)$(EXT)
MAIN    := ./cmd/bluetag
GOFLAGS ?= -trimpath
LDFLAGS ?= -s -w
LINUX_ARCH ?= amd64

.PHONY: all build build-linux run test vet fmt clean

all: vet test build

## build: 编译单一 exe 到 bin/ (前端页面已 go:embed 内嵌)
build: $(TARGET)
## build-linux: 编译 Linux 版 (cgo)。在 Linux 本机 (装好 gcc/pkg-config/libpcsclite-dev)
##              直接运行; 交叉编译需配置工具链, 如:
##              make build-linux CC=x86_64-linux-gnu-gcc
build-linux:
	CGO_ENABLED=1 GOOS=linux GOARCH=$(LINUX_ARCH) CC=$(CC) \
		$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" \
		-o $(BIN)/$(APP)-linux-$(LINUX_ARCH) $(MAIN)

## run: 本地开发运行 (go run)

$(TARGET):
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $@ $(MAIN)

## run: 本地开发运行 (go run)
run:
	$(GO) run $(MAIN)

## test: 运行全部单元测试
test:
	$(GO) test ./...

## vet: 静态检查
vet:
	$(GO) vet ./...

## fmt: gofmt 全部代码
fmt:
	$(GO) fmt ./...

## clean: 清理构建产物
clean:
	-$(RM) -r $(BIN)

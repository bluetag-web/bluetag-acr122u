# bluetag-go — B037 价签写卡服务 (Windows + ACR122U)
# 用法: make build / make run / make test / make vet / make fmt / make clean

GO      ?= go
APP     := bluetag-go
BIN     := bin
EXT     := $(shell $(GO) env GOEXE)
TARGET  := $(BIN)/$(APP)$(EXT)
MAIN    := ./cmd/bluetag
GOFLAGS ?= -trimpath
LDFLAGS ?= -s -w

.PHONY: all build run test vet fmt clean

all: vet test build

## build: 编译单一 exe 到 bin/ (前端页面已 go:embed 内嵌)
build: $(TARGET)

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

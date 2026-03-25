# so-study Makefile
# 完整演示: 编译目标库 → 生成 Go 转发器 → 编译转发器 → 替换 → 测试

.PHONY: all clean demo build-target build-generator generate-proxy build-proxy test

BUILD_DIR := build

all: demo

# ---- 第1步: 编译示例目标库 ----
build-target:
	@echo "===> [1/5] 编译目标库 libtarget.so ..."
	@mkdir -p $(BUILD_DIR)
	cd example/target && CGO_ENABLED=1 go build -buildmode=c-shared -o ../../$(BUILD_DIR)/libtarget.so target.go
	@echo "     完成: $(BUILD_DIR)/libtarget.so"

# ---- 第2步: 编译代码生成器 ----
build-generator:
	@echo "===> [2/5] 编译代码生成器 so-proxy-gen ..."
	@mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/so-proxy-gen ./cmd/so-proxy-gen
	@echo "     完成: $(BUILD_DIR)/so-proxy-gen"

# ---- 第3步: 生成 Go 转发器源码 ----
generate-proxy: build-target build-generator
	@echo ""
	@echo "===> [3/5] 生成 Go 转发器源码 ..."
	@# 备份原始 .so
	cp $(BUILD_DIR)/libtarget.so $(BUILD_DIR)/libtarget_backup.so
	@# 生成转发器 Go 项目
	$(BUILD_DIR)/so-proxy-gen \
		-so $(BUILD_DIR)/libtarget.so \
		-backup libtarget_backup.so \
		-out $(BUILD_DIR)/proxy

# ---- 第4步: 编译 Go 转发器 ----
build-proxy: generate-proxy
	@echo ""
	@echo "===> [4/5] 编译 Go 转发器 ..."
	cd $(BUILD_DIR)/proxy && CGO_ENABLED=1 go build -buildmode=c-shared -o ../libtarget.so .
	@echo "     转发器已编译: $(BUILD_DIR)/libtarget.so"

# ---- 第5步: 测试 ----
test: build-proxy
	@echo ""
	@echo "===> [5/5] 编译并运行测试 ..."
	gcc -o $(BUILD_DIR)/test_proxy example/test/test_proxy.c -ldl
	@echo ""
	@echo "--- 运行测试（观察 stderr 中的 [so-proxy] 日志）---"
	@echo ""
	cd $(BUILD_DIR) && ./test_proxy ./libtarget.so

# ---- 完整演示 ----
demo: test
	@echo ""
	@echo "===> 演示完成!"
	@echo "     [so-proxy] 开头的行即为 Go 转发器打印的日志"

# ---- 清理 ----
clean:
	rm -rf $(BUILD_DIR)
	@echo "已清理"

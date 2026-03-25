# so-study Makefile
# 完整演示流程: 编译目标库 → 生成转发器 → 替换 → 测试

.PHONY: all clean demo build-target build-generator generate-proxy build-proxy test

# 输出目录
BUILD_DIR := build

all: demo

# ---- 第1步: 编译示例目标库 ----
build-target:
	@echo "===> [1/4] 编译目标库 libtarget.so ..."
	@mkdir -p $(BUILD_DIR)
	cd example/target && CGO_ENABLED=1 go build -buildmode=c-shared -o ../../$(BUILD_DIR)/libtarget.so target.go
	@echo "     完成: $(BUILD_DIR)/libtarget.so"

# ---- 第2步: 编译代码生成器 ----
build-generator:
	@echo "===> [2/4] 编译代码生成器 so-proxy-gen ..."
	@mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/so-proxy-gen ./cmd/so-proxy-gen
	@echo "     完成: $(BUILD_DIR)/so-proxy-gen"

# ---- 第3步: 生成转发器 C 源码 + 编译 ----
generate-proxy: build-target build-generator
	@echo ""
	@echo "===> [3/4] 生成转发器源码并编译 ..."
	@# 备份原始 .so
	cp $(BUILD_DIR)/libtarget.so $(BUILD_DIR)/libtarget_backup.so
	@# 生成 proxy.c
	$(BUILD_DIR)/so-proxy-gen \
		-so $(BUILD_DIR)/libtarget.so \
		-backup libtarget_backup.so \
		-out $(BUILD_DIR)/proxy.c
	@echo ""
	@# 编译转发器，替换原始 .so
	gcc -shared -fPIC -o $(BUILD_DIR)/libtarget.so $(BUILD_DIR)/proxy.c -ldl
	@echo "     转发器已编译并替换 libtarget.so"

# ---- 第4步: 编译测试程序并运行 ----
test: generate-proxy
	@echo ""
	@echo "===> [4/4] 编译并运行测试 ..."
	gcc -o $(BUILD_DIR)/test_proxy example/test/test_proxy.c -ldl
	@echo ""
	@echo "--- 运行测试（观察 stderr 中的 [so-proxy] 日志）---"
	@echo ""
	cd $(BUILD_DIR) && ./test_proxy ./libtarget.so

# ---- 完整演示 ----
demo: test
	@echo ""
	@echo "===> 演示完成!"
	@echo "     查看上方 [so-proxy] 开头的日志行，即为转发器打印的日志"

# ---- 清理 ----
clean:
	rm -rf $(BUILD_DIR)
	@echo "已清理"

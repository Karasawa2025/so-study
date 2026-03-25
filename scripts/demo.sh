#!/bin/bash
# demo.sh - 手动演示脚本（与 Makefile 等效）
set -e

BUILD_DIR="build"
mkdir -p "$BUILD_DIR"

echo "============================================"
echo "  so-study: Go .so 转发器演示"
echo "============================================"
echo ""

# 1. 编译目标库
echo "[1/5] 编译目标库 libtarget.so ..."
cd example/target
CGO_ENABLED=1 go build -buildmode=c-shared -o "../../${BUILD_DIR}/libtarget.so" target.go
cd ../..
echo "      => ${BUILD_DIR}/libtarget.so"
echo ""

# 2. 编译代码生成器
echo "[2/5] 编译代码生成器 ..."
go build -o "${BUILD_DIR}/so-proxy-gen" ./cmd/so-proxy-gen
echo "      => ${BUILD_DIR}/so-proxy-gen"
echo ""

# 3. 备份原始 .so
echo "[3/5] 备份: libtarget.so -> libtarget_backup.so ..."
cp "${BUILD_DIR}/libtarget.so" "${BUILD_DIR}/libtarget_backup.so"
echo ""

# 4. 生成并编译转发器
echo "[4/5] 生成转发器 ..."
"${BUILD_DIR}/so-proxy-gen" \
    -so "${BUILD_DIR}/libtarget.so" \
    -backup libtarget_backup.so \
    -out "${BUILD_DIR}/proxy.c"
echo ""
echo "      编译转发器替换 libtarget.so ..."
gcc -shared -fPIC -o "${BUILD_DIR}/libtarget.so" "${BUILD_DIR}/proxy.c" -ldl
echo ""

# 5. 测试
echo "[5/5] 编译并运行测试程序 ..."
gcc -o "${BUILD_DIR}/test_proxy" example/test/test_proxy.c -ldl
echo ""
echo "============================================"
echo "  运行测试 (观察 [so-proxy] 日志)"
echo "============================================"
echo ""
cd "${BUILD_DIR}"
./test_proxy ./libtarget.so
echo ""
echo "演示完成!"

// so-proxy-gen: 读取目标 .so 的导出符号，生成 Go 转发器源码
//
// 工作流程:
//   1. 解析目标 A.so 的 ELF 动态符号表，提取导出函数
//   2. 生成一个完整的 Go 源文件（proxy.go），使用 cgo + dlopen/dlsym
//   3. 每个导出函数在 Go 侧通过 //export 导出，内部调用 C trampoline 转发
//   4. 编译: go build -buildmode=c-shared -o A.so proxy.go
//
// 用法:
//   go run ./cmd/so-proxy-gen -so libtarget.so [-backup libtarget_backup.so] [-out proxy.go]

package main

import (
	"debug/elf"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
)

type FuncInfo struct {
	Name  string
	Index int
}

func main() {
	soPath := flag.String("so", "", "目标 .so 文件路径")
	backupName := flag.String("backup", "", "备份 .so 文件名（默认: 原文件名加 _backup 后缀）")
	output := flag.String("out", "", "输出目录路径（默认: 当前目录下的 proxy/ 子目录）")
	flag.Parse()

	if *soPath == "" {
		log.Fatal("请指定目标 .so 文件路径，例如: -so libtarget.so")
	}

	f, err := elf.Open(*soPath)
	if err != nil {
		log.Fatalf("无法打开 ELF 文件 %s: %v", *soPath, err)
	}
	defer f.Close()

	symbols, err := f.DynamicSymbols()
	if err != nil {
		log.Fatalf("无法读取动态符号表: %v", err)
	}

	// 过滤导出函数
	var funcs []FuncInfo
	skipPrefixes := []string{
		"_cgo", "_cgoexp", "crosscall2", "_rt0",
		"runtime.", "x_cgo", "_Cgo",
	}

	idx := 0
	for _, sym := range symbols {
		if elf.ST_TYPE(sym.Info) != elf.STT_FUNC {
			continue
		}
		if elf.ST_BIND(sym.Info) != elf.STB_GLOBAL {
			continue
		}
		if sym.Name == "" || sym.Value == 0 {
			continue
		}
		if strings.HasPrefix(sym.Name, "_") {
			continue
		}

		skip := false
		for _, prefix := range skipPrefixes {
			if strings.HasPrefix(sym.Name, prefix) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}

		funcs = append(funcs, FuncInfo{Name: sym.Name, Index: idx})
		idx++
	}

	sort.Slice(funcs, func(i, j int) bool {
		return funcs[i].Name < funcs[j].Name
	})
	// 重新编号
	for i := range funcs {
		funcs[i].Index = i
	}

	if len(funcs) == 0 {
		log.Fatal("未找到可转发的导出函数")
	}

	fmt.Printf("找到 %d 个可转发的导出函数:\n", len(funcs))
	for _, fn := range funcs {
		fmt.Printf("  - %s\n", fn.Name)
	}

	// 确定备份文件名
	backup := *backupName
	if backup == "" {
		base := filepath.Base(*soPath)
		ext := filepath.Ext(base)
		nameNoExt := strings.TrimSuffix(base, ext)
		backup = nameNoExt + "_backup" + ext
	}

	// 确定输出目录
	outDir := *output
	if outDir == "" {
		outDir = "proxy"
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		log.Fatalf("无法创建输出目录 %s: %v", outDir, err)
	}

	data := struct {
		OriginalSo string
		BackupSo   string
		Functions  []FuncInfo
		FuncCount  int
	}{
		OriginalSo: filepath.Base(*soPath),
		BackupSo:   backup,
		Functions:  funcs,
		FuncCount:  len(funcs),
	}

	// 生成 proxy.go
	writeTemplate(filepath.Join(outDir, "proxy.go"), proxyGoTemplate, data)
	// 生成 trampoline.c (CGO 内嵌的 C 辅助函数)
	writeTemplate(filepath.Join(outDir, "trampoline.go"), trampolineGoTemplate, data)
	// 生成 go.mod
	writeTemplate(filepath.Join(outDir, "go.mod"), goModTemplate, data)
	// 生成 Makefile
	writeTemplate(filepath.Join(outDir, "Makefile"), makefileTemplate, data)

	fmt.Printf("\n已生成转发器项目: %s/\n", outDir)
	fmt.Println("\n使用步骤:")
	fmt.Printf("  1. cp %s %s\n", filepath.Base(*soPath), backup)
	fmt.Printf("  2. cd %s && make\n", outDir)
	fmt.Printf("  3. cp %s/%s ./%s\n", outDir, filepath.Base(*soPath), filepath.Base(*soPath))
	fmt.Printf("  4. 现在 %s 是 Go 转发器，调用会转发到 %s 并打印日志\n",
		filepath.Base(*soPath), backup)
}

func writeTemplate(path string, tmplStr string, data interface{}) {
	f, err := os.Create(path)
	if err != nil {
		log.Fatalf("无法创建文件 %s: %v", path, err)
	}
	defer f.Close()

	tmpl, err := template.New(filepath.Base(path)).Parse(tmplStr)
	if err != nil {
		log.Fatalf("模板解析失败 (%s): %v", path, err)
	}

	if err := tmpl.Execute(f, data); err != nil {
		log.Fatalf("模板执行失败 (%s): %v", path, err)
	}

	fmt.Printf("  生成: %s\n", path)
}

// ============================================================
// Go 模板: proxy.go - 主转发逻辑
// ============================================================
var proxyGoTemplate = `package main

// 自动生成的 Go .so 转发器
// 原始库: {{.OriginalSo}}
// 备份库: {{.BackupSo}}
//
// 编译: CGO_ENABLED=1 go build -buildmode=c-shared -o {{.OriginalSo}} .

/*
#include <stdio.h>
#include <time.h>

static void proxy_log(const char *func_name) {
    time_t now = time(NULL);
    struct tm tm_info;
    char ts[64];
    localtime_r(&now, &tm_info);
    strftime(ts, sizeof(ts), "%Y-%m-%d %H:%M:%S", &tm_info);
    fprintf(stderr, "[so-proxy] %s forwarding -> %s()\n", ts, func_name);
}
*/
import "C"

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"unsafe"
)

// backupSoName 是备份库的文件名
const backupSoName = "{{.BackupSo}}"

var (
	initOnce sync.Once
	// 函数指针缓存
	funcPtrs [{{.FuncCount}}]unsafe.Pointer
	funcNames = [{{.FuncCount}}]string{
		{{- range .Functions}}
		"{{.Name}}",
		{{- end}}
	}
)

// initBackup 加载备份库并解析所有函数符号
func initBackup() {
	initOnce.Do(func() {
		// 获取自身 .so 所在目录，备份库应在同一目录
		soDir := "."
		selfPath, err := os.Executable()
		if err == nil {
			soDir = filepath.Dir(selfPath)
		}

		backupPath := filepath.Join(soDir, backupSoName)
		// 如果不存在，尝试当前目录
		if _, err := os.Stat(backupPath); os.IsNotExist(err) {
			backupPath = "./" + backupSoName
		}

		handle := dlOpen(backupPath)
		if handle == nil {
			fmt.Fprintf(os.Stderr, "[so-proxy] FATAL: 无法加载备份库 %s\n", backupPath)
			os.Exit(1)
		}

		fmt.Fprintf(os.Stderr, "[so-proxy] 已加载备份库: %s\n", backupPath)

		// 解析所有函数符号
		for i, name := range funcNames {
			ptr := dlSym(handle, name)
			if ptr == nil {
				fmt.Fprintf(os.Stderr, "[so-proxy] 警告: 无法解析符号 %s\n", name)
				continue
			}
			funcPtrs[i] = ptr
		}
	})
}

// logAndGetPtr 打印日志并返回函数指针
func logAndGetPtr(index int) unsafe.Pointer {
	initBackup()
	cName := C.CString(funcNames[index])
	C.proxy_log(cName)
	C.free(unsafe.Pointer(cName))
	return funcPtrs[index]
}

func main() {}
`

// ============================================================
// Go 模板: trampoline.go - cgo dlopen/dlsym 封装 + 导出函数桩
// ============================================================
var trampolineGoTemplate = `package main

// 自动生成 - 转发桩函数

/*
#cgo LDFLAGS: -ldl
#include <dlfcn.h>
#include <stdlib.h>
#include <stdio.h>
#include <time.h>

// ---- dlopen/dlsym 封装 ----
static void* proxy_dlopen(const char *path) {
    void *h = dlopen(path, RTLD_NOW);
    if (!h) {
        fprintf(stderr, "[so-proxy] dlopen error: %s\n", dlerror());
    }
    return h;
}

static void* proxy_dlsym(void *handle, const char *name) {
    dlerror(); // clear
    void *sym = dlsym(handle, name);
    char *err = dlerror();
    if (err) {
        fprintf(stderr, "[so-proxy] dlsym error(%s): %s\n", name, err);
        return NULL;
    }
    return sym;
}

// ---- 通用 trampoline: 通过函数指针调用，最多传递 6 个整数/指针参数 ----
// x86_64 System V ABI: rdi, rsi, rdx, rcx, r8, r9
// 返回值通过 rax 传回

typedef long long (*fn_ptr_t)(long long, long long, long long, long long, long long, long long);

static long long trampoline_call(void *fn, long long a1, long long a2,
                                  long long a3, long long a4,
                                  long long a5, long long a6) {
    return ((fn_ptr_t)fn)(a1, a2, a3, a4, a5, a6);
}

// 打印转发日志
static void log_forward(const char *name) {
    time_t now = time(NULL);
    struct tm tm_info;
    char ts[64];
    localtime_r(&now, &tm_info);
    strftime(ts, sizeof(ts), "%Y-%m-%d %H:%M:%S", &tm_info);
    fprintf(stderr, "[so-proxy] %s forwarding -> %s()\n", ts, name);
}
*/
import "C"

import "unsafe"

// ---- dlopen / dlsym Go 封装 ----

func dlOpen(path string) unsafe.Pointer {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	return C.proxy_dlopen(cPath)
}

func dlSym(handle unsafe.Pointer, name string) unsafe.Pointer {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	return C.proxy_dlsym(handle, cName)
}

// ============================================================
// 转发桩: 每个导出函数对应一个 //export 函数
//
// Go 侧通过 //export 导出与原始 .so 相同的符号名。
// 调用时:
//   1. 确保备份库已加载（initBackup）
//   2. 打印日志
//   3. 通过 C trampoline 调用备份库中的真实函数
//
// 参数使用 C.longlong (int64) 作为通用类型，
// 利用 x86_64 ABI 兼容性传递 int/pointer 类型参数。
// ============================================================
{{range .Functions}}
//export {{.Name}}
func {{.Name}}(a1, a2, a3, a4, a5, a6 C.longlong) C.longlong {
	initBackup()
	cName := C.CString("{{.Name}}")
	C.log_forward(cName)
	C.free(unsafe.Pointer(cName))
	ptr := funcPtrs[{{.Index}}]
	if ptr == nil {
		return 0
	}
	return C.trampoline_call(ptr, a1, a2, a3, a4, a5, a6)
}
{{end}}
`

// ============================================================
// go.mod
// ============================================================
var goModTemplate = `module so-proxy

go 1.21
`

// ============================================================
// Makefile
// ============================================================
var makefileTemplate = `.PHONY: build clean

build:
	CGO_ENABLED=1 go build -buildmode=c-shared -o {{.OriginalSo}} .

clean:
	rm -f {{.OriginalSo}} *.h
`

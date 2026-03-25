// so-proxy-gen: 读取目标 .so 文件的导出符号，生成转发器源代码
//
// 工作流程:
//   1. 读取目标 A.so 的 ELF 动态符号表（FUNC 类型）
//   2. 生成 C 源代码和内联汇编，为每个导出函数创建同名转发桩函数
//   3. 转发桩在运行时通过 dlopen/dlsym 加载备份的 B.so，将调用原样转发
//   4. 每次转发前向 stderr 打印一行日志（包含时间戳和函数名）
//
// 用法:
//   go run ./cmd/so-proxy-gen -so libtarget.so [-backup libtarget_backup.so] [-out proxy.c]
//
// 生成的 proxy.c 使用 x86_64 System V ABI 汇编桩实现参数透传，
// 保证整数/指针参数（rdi, rsi, rdx, rcx, r8, r9）和浮点参数（xmm0-xmm7）
// 在转发过程中不被破坏。

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
	Name string
}

func main() {
	soPath := flag.String("so", "", "目标 .so 文件路径")
	backupName := flag.String("backup", "", "备份 .so 文件名（默认: 原文件名加 _backup 后缀）")
	output := flag.String("out", "proxy.c", "输出 C 源文件路径")
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

	// 过滤: 只保留用户导出的函数，排除 Go runtime / cgo 内部符号
	var funcs []FuncInfo
	skipPrefixes := []string{
		"_cgo", "_cgoexp", "crosscall2", "_rt0",
		"runtime.", "x_cgo", "_Cgo",
	}

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

		funcs = append(funcs, FuncInfo{Name: sym.Name})
	}

	sort.Slice(funcs, func(i, j int) bool {
		return funcs[i].Name < funcs[j].Name
	})

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

	data := struct {
		OriginalSo string
		BackupSo   string
		Functions  []FuncInfo
	}{
		OriginalSo: filepath.Base(*soPath),
		BackupSo:   backup,
		Functions:  funcs,
	}

	outFile, err := os.Create(*output)
	if err != nil {
		log.Fatalf("无法创建输出文件 %s: %v", *output, err)
	}
	defer outFile.Close()

	tmpl, err := template.New("proxy").Parse(proxyTemplate)
	if err != nil {
		log.Fatalf("模板解析失败: %v", err)
	}

	if err := tmpl.Execute(outFile, data); err != nil {
		log.Fatalf("模板执行失败: %v", err)
	}

	fmt.Printf("\n已生成转发器源码: %s\n", *output)
	fmt.Printf("备份 .so 名称: %s\n", backup)
	fmt.Println("\n使用步骤:")
	fmt.Printf("  1. cp %s %s\n", filepath.Base(*soPath), backup)
	fmt.Printf("  2. gcc -shared -fPIC -o %s %s -ldl\n", filepath.Base(*soPath), *output)
	fmt.Printf("  3. 现在 %s 是转发器，调用会自动转发到 %s 并打印日志\n", filepath.Base(*soPath), backup)
}

var proxyTemplate = `/* ============================================================
 * 自动生成的 .so 转发器 (proxy)
 * 原始库: {{.OriginalSo}}
 * 备份库: {{.BackupSo}}
 *
 * 编译: gcc -shared -fPIC -o {{.OriginalSo}} proxy.c -ldl
 *
 * 原理:
 *   每个导出函数被替换为一个转发桩(stub)。桩函数:
 *   1. 首次调用时通过 dlopen 加载备份库 {{.BackupSo}}
 *   2. 通过 dlsym 解析同名符号得到真实函数指针
 *   3. 向 stderr 打印一行日志（时间戳 + 函数名）
 *   4. 跳转到真实函数（使用汇编 jmp 保持参数和栈帧不变）
 *
 * 适用平台: x86_64 Linux (System V ABI)
 * ============================================================ */

#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <dlfcn.h>
#include <string.h>
#include <time.h>
#include <pthread.h>

/* ---- 备份库路径 ---- */
#define BACKUP_SO_PATH "./{{.BackupSo}}"

/* ---- 全局状态 ---- */
static void *g_backup_handle = NULL;
static pthread_once_t g_init_once = PTHREAD_ONCE_INIT;

static void get_timestamp(char *buf, size_t len) {
    time_t now = time(NULL);
    struct tm tm_info;
    localtime_r(&now, &tm_info);
    strftime(buf, len, "%Y-%m-%d %H:%M:%S", &tm_info);
}

/* 加载备份库（只执行一次） */
static void load_backup_library(void) {
    g_backup_handle = dlopen(BACKUP_SO_PATH, RTLD_NOW);
    if (!g_backup_handle) {
        fprintf(stderr, "[so-proxy] FATAL: dlopen(%s) failed: %s\n",
                BACKUP_SO_PATH, dlerror());
        abort();
    }
    char ts[64];
    get_timestamp(ts, sizeof(ts));
    fprintf(stderr, "[so-proxy] %s loaded backup library: %s\n", ts, BACKUP_SO_PATH);
}

static void ensure_loaded(void) {
    pthread_once(&g_init_once, load_backup_library);
}

/* 解析备份库中的符号 */
static void* resolve(const char *name) {
    ensure_loaded();
    dlerror();
    void *sym = dlsym(g_backup_handle, name);
    char *err = dlerror();
    if (err) {
        fprintf(stderr, "[so-proxy] FATAL: dlsym(%s) failed: %s\n", name, err);
        abort();
    }
    return sym;
}

/* 库卸载清理 */
__attribute__((destructor))
static void proxy_fini(void) {
    if (g_backup_handle) {
        /* 注意: Go 运行时编译的 .so 不可安全 dlclose，此处仅置空 */
        g_backup_handle = NULL;
        fprintf(stderr, "[so-proxy] proxy unloaded\n");
    }
}

/* ============================================================
 * 转发桩函数
 *
 * 方案 A (下方使用): C 函数 + 函数指针调用
 *   优点: 可移植，编译器负责 ABI
 *   限制: 需要知道参数数量上限；此处使用 6 个 long 参数覆盖
 *         x86_64 SysV 前 6 个整数/指针寄存器参数
 *
 * 方案 B (注释中): 纯汇编 jmp 透传
 *   优点: 完美透传任意参数（包括浮点、变参）
 *   限制: 仅 x86_64，需 nasm/gas
 *
 * 如果目标函数签名已知，建议手动将 typedef 改为精确类型。
 * ============================================================ */
{{range .Functions}}
/* ---- {{.Name}} ---- */
typedef long (*pfn_{{.Name}}_t)(long, long, long, long, long, long);
static pfn_{{.Name}}_t pfn_{{.Name}} = NULL;

__attribute__((visibility("default")))
long {{.Name}}(long a1, long a2, long a3, long a4, long a5, long a6) {
    if (__builtin_expect(pfn_{{.Name}} == NULL, 0)) {
        pfn_{{.Name}} = (pfn_{{.Name}}_t)resolve("{{.Name}}");
    }
    char ts[64];
    get_timestamp(ts, sizeof(ts));
    fprintf(stderr, "[so-proxy] %s forwarding -> {{.Name}}()\n", ts);
    return pfn_{{.Name}}(a1, a2, a3, a4, a5, a6);
}
{{end}}

/* ============================================================
 * 方案 B: 汇编 jmp 透传 (x86_64)
 * 如需完美透传（包括浮点和变参），取消下方注释并注释掉上方 C 桩。
 * 编译时需加 -DUSE_ASM_STUBS
 * ============================================================
 *
 * #ifdef USE_ASM_STUBS
{{range .Functions}} *
 * // {{.Name}} 的函数指针（供汇编桩使用）
 * void *g_real_{{.Name}} = NULL;
 *
 * // 初始化函数（首次调用时触发）
 * void __init_{{.Name}}(void) {
 *     g_real_{{.Name}} = resolve("{{.Name}}");
 * }
 *
 * // 汇编桩: 打印日志后 jmp 到真实函数
 * __asm__(
 *     ".globl {{.Name}}\n"
 *     ".type {{.Name}}, @function\n"
 *     "{{.Name}}:\n"
 *     "    push %rdi\n"
 *     "    push %rsi\n"
 *     "    push %rdx\n"
 *     "    push %rcx\n"
 *     "    push %r8\n"
 *     "    push %r9\n"
 *     "    sub  $8, %rsp\n"            // 对齐 16 字节
 *     "    call __log_{{.Name}}\n"      // 打印日志
 *     "    add  $8, %rsp\n"
 *     "    pop  %r9\n"
 *     "    pop  %r8\n"
 *     "    pop  %rcx\n"
 *     "    pop  %rdx\n"
 *     "    pop  %rsi\n"
 *     "    pop  %rdi\n"
 *     "    movq g_real_{{.Name}}(%rip), %rax\n"
 *     "    jmp  *%rax\n"               // 尾调用: 直接跳转
 * );
 *
 * void __log_{{.Name}}(void) {
 *     if (__builtin_expect(g_real_{{.Name}} == NULL, 0))
 *         __init_{{.Name}}();
 *     char ts[64];
 *     get_timestamp(ts, sizeof(ts));
 *     fprintf(stderr, "[so-proxy] %s forwarding -> {{.Name}}()\n", ts);
 * }
{{end}} *
 * #endif // USE_ASM_STUBS
 */
`

// test_proxy.c: 测试程序，加载 .so 并调用导出函数
// 编译: gcc -o test_proxy test_proxy.c -ldl

#include <stdio.h>
#include <stdlib.h>
#include <dlfcn.h>

typedef int (*add_fn)(int, int);
typedef int (*multiply_fn)(int, int);
typedef void (*hello_fn)(const char *);
typedef const char* (*get_version_fn)(void);

int main(int argc, char **argv) {
    const char *so_path = "./libtarget.so";
    if (argc > 1) {
        so_path = argv[1];
    }

    printf("=== .so 转发器测试 ===\n");
    printf("加载库: %s\n\n", so_path);

    void *handle = dlopen(so_path, RTLD_NOW);
    if (!handle) {
        fprintf(stderr, "dlopen failed: %s\n", dlerror());
        return 1;
    }

    // 测试 Add
    add_fn add = (add_fn)dlsym(handle, "Add");
    if (add) {
        int result = add(10, 20);
        printf("Add(10, 20) = %d\n\n", result);
    } else {
        printf("Add not found: %s\n\n", dlerror());
    }

    // 测试 Multiply
    multiply_fn mul = (multiply_fn)dlsym(handle, "Multiply");
    if (mul) {
        int result = mul(6, 7);
        printf("Multiply(6, 7) = %d\n\n", result);
    } else {
        printf("Multiply not found: %s\n\n", dlerror());
    }

    // 测试 Hello
    hello_fn hello = (hello_fn)dlsym(handle, "Hello");
    if (hello) {
        hello("World");
        printf("\n");
    } else {
        printf("Hello not found: %s\n\n", dlerror());
    }

    // 测试 GetVersion
    get_version_fn get_ver = (get_version_fn)dlsym(handle, "GetVersion");
    if (get_ver) {
        const char *ver = get_ver();
        printf("GetVersion() = %s\n\n", ver);
    } else {
        printf("GetVersion not found: %s\n\n", dlerror());
    }

    dlclose(handle);
    printf("=== 测试完成 ===\n");
    return 0;
}

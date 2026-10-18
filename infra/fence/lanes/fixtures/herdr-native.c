/* Native foreground parent for the credential-free plugin-lab fake.
 * Herdr inspects this real process; it is not a provider implementation. */
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/wait.h>
#include <unistd.h>

int main(int argc, char **argv) {
    if (argc == 2 && strcmp(argv[1], "--version") == 0) {
        puts("2.1.63 (Claude Code)");
        return 0;
    }
    const char *script = getenv("H_FAKE_SCRIPT");
    if (!script || !*script) {
        fputs("herdr-native: H_FAKE_SCRIPT is required\n", stderr);
        return 2;
    }
    char **child = calloc((size_t)argc + 2, sizeof(char *));
    if (!child) { perror("herdr-native: allocate argv"); return 2; }
    child[0] = "/bin/bash";
    child[1] = (char *)script;
    for (int i = 1; i < argc; i++) child[i + 1] = argv[i];
    pid_t pid = fork();
    if (pid < 0) { perror("herdr-native: fork"); free(child); return 2; }
    if (pid == 0) {
        execv(child[0], child);
        perror("herdr-native: exec plugin fake");
        _exit(127);
    }
    free(child);
    int status;
    while (waitpid(pid, &status, 0) < 0) {
        if (errno == EINTR) continue;
        perror("herdr-native: wait plugin fake");
        return 2;
    }
    return WIFEXITED(status) ? WEXITSTATUS(status) : 128 + WTERMSIG(status);
}

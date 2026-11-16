static int helper(void) { return 1; }
int run(void) { return helper(); }
#include <stdio.h>
int other(int (*fp)(void)) { printf("x"); return fp() + run(); }

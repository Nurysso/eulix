struct S {
  int get() { return inner(); }
  int inner() { return 1; }
};
int helper() { return 2; }
int run() { S s; return s.get() + helper(); }
#include <cstdio>
struct B { virtual int f() { return 0; } };
struct D : B { int f() override { return 1; } };
int use(B* b) { S t = S(); printf("x"); return b->f() + t.get(); }

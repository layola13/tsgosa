class C {
  arr: number[] = [1, 2];
}
function f(c: C): number {
  return c.arr;
}
function main(): i32 {
  const c = new C();
  console.log(f(c));
  return 0;
}

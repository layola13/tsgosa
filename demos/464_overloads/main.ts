function f(a: i32): i32;
function f(a: string): string;
function f(a: unknown): unknown {
  return a;
}
function main(): i32 {
  console.log(f(7));
  return 0;
}
